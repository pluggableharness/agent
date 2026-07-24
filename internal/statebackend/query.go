package statebackend

import (
	"context"
	"database/sql"
	"fmt"
	"iter"

	"github.com/pluggableharness/agent/internal/telemetry"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// rowScanner is the subset of *sql.Row and *sql.Rows this package's scan
// helpers need, letting one scan function serve both a single-row
// QueryRowContext caller and a multi-row QueryContext loop.
type rowScanner interface {
	Scan(dest ...any) error
}

// Meta returns this session's session_meta row. Returns ErrClosed if Close
// has already been called.
func (s *Session) Meta(ctx context.Context) (_ SessionMeta, err error) {
	if s.closed.Load() {
		return SessionMeta{}, ErrClosed
	}
	ctx, span := s.telemetry.StartStateBackendMetaQuery(ctx, s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: querying session meta", "session_id", s.id)

	meta, metaErr := querySessionMeta(ctx, s.db, s.id)
	if metaErr != nil {
		err = metaErr
		return SessionMeta{}, err
	}
	return meta, nil
}

// Events returns every event in this session's file as a sequence-ordered
// iter.Seq2 — sequence is the sole ordering authority
// (docs/specifications/state-backend.md#ordering--concurrency,
// determinism.md), never wall-clock time. Each Event's Sequence is
// populated and Payload is byte-identical to what was appended. A decode
// or read error surfaces through the error side of the pair and stops
// iteration — the caller sees no further events after that point. If
// Close has already been called, the sequence yields exactly one
// (Event{}, ErrClosed) pair.
func (s *Session) Events(ctx context.Context) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		if s.closed.Load() {
			yield(Event{}, ErrClosed)
			return
		}

		ctx, span := s.telemetry.StartStateBackendEventsQuery(ctx, s.id)
		var err error
		defer func() { telemetry.EndSpan(span, err) }()
		s.logger.DebugContext(ctx, "statebackend: querying events", "session_id", s.id)

		const q = `SELECT sequence, id, timestamp, kind, producer_category, producer_name, producer_version, schema_version, payload FROM events ORDER BY sequence`
		rows, queryErr := s.db.QueryContext(ctx, q)
		if queryErr != nil {
			err = fmt.Errorf("statebackend: query events: %w", queryErr)
			yield(Event{}, err)
			return
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			ev, scanErr := scanEvent(rows)
			if scanErr != nil {
				err = fmt.Errorf("statebackend: query events: %w", scanErr)
				yield(Event{}, err)
				return
			}
			if !yield(ev, nil) {
				return
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			err = fmt.Errorf("statebackend: query events: %w", rowsErr)
			yield(Event{}, err)
		}
	}
}

// scanEvent decodes one events row, translating its stored TEXT
// kind/producer_category back into their proto enum values.
func scanEvent(row rowScanner) (Event, error) {
	var (
		ev                          Event
		timestampText, kindText     string
		categoryText, name, version string
	)
	if err := row.Scan(&ev.Sequence, &ev.ID, &timestampText, &kindText, &categoryText, &name, &version, &ev.SchemaVersion, &ev.Payload); err != nil {
		return Event{}, err
	}

	timestamp, err := parseTimestamp(timestampText)
	if err != nil {
		return Event{}, fmt.Errorf("timestamp: %w", err)
	}
	ev.Timestamp = timestamp

	kind, err := decodeEventKind(kindText)
	if err != nil {
		return Event{}, err
	}
	ev.Kind = kind

	category, err := decodeProducerCategory(categoryText)
	if err != nil {
		return Event{}, err
	}
	ev.Producer = &commonv1.ProducerRef{Category: category, Name: name, Version: version}

	return ev, nil
}

// Producers returns the distinct set of producers that have written to
// this session's file (docs/specifications/state-backend.md#producers —
// the "install X to re-render this" preflight list), ordered
// deterministically by (category, name, version). Returns ErrClosed if
// Close has already been called.
func (s *Session) Producers(ctx context.Context) (_ []*commonv1.ProducerRef, err error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	ctx, span := s.telemetry.StartStateBackendProducersQuery(ctx, s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: querying producers", "session_id", s.id)

	const q = `SELECT category, name, version FROM producers ORDER BY category, name, version`
	rows, queryErr := s.db.QueryContext(ctx, q)
	if queryErr != nil {
		err = fmt.Errorf("statebackend: query producers: %w", queryErr)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var producers []*commonv1.ProducerRef
	for rows.Next() {
		var categoryText, name, version string
		if scanErr := rows.Scan(&categoryText, &name, &version); scanErr != nil {
			err = fmt.Errorf("statebackend: query producers: %w", scanErr)
			return nil, err
		}
		category, decErr := decodeProducerCategory(categoryText)
		if decErr != nil {
			err = fmt.Errorf("statebackend: query producers: %w", decErr)
			return nil, err
		}
		producers = append(producers, &commonv1.ProducerRef{Category: category, Name: name, Version: version})
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		err = fmt.Errorf("statebackend: query producers: %w", rowsErr)
		return nil, err
	}
	return producers, nil
}

// TotalCostUSD returns SUM(cost_ledger.cost_usd), or 0 if the session has
// no cost_ledger rows yet. Returns ErrClosed if Close has already been
// called.
func (s *Session) TotalCostUSD(ctx context.Context) (_ float64, err error) {
	if s.closed.Load() {
		return 0, ErrClosed
	}
	ctx, span := s.telemetry.StartStateBackendCostQuery(ctx, s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: querying total cost", "session_id", s.id)

	var total sql.NullFloat64
	row := s.db.QueryRowContext(ctx, "SELECT SUM(cost_usd) FROM cost_ledger")
	if scanErr := row.Scan(&total); scanErr != nil {
		err = fmt.Errorf("statebackend: query total cost: %w", scanErr)
		return 0, err
	}
	// SUM over zero rows is SQL NULL, not 0 — total.Valid is false in that
	// case and total.Float64's zero value is exactly the "0 for none"
	// this method documents.
	return total.Float64, nil
}

// CostLedger returns every cost_ledger row, in append order (sequence —
// the sole ordering authority). Returns ErrClosed if Close has already
// been called.
func (s *Session) CostLedger(ctx context.Context) (_ []CostEntry, err error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	ctx, span := s.telemetry.StartStateBackendCostLedgerQuery(ctx, s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: querying cost ledger", "session_id", s.id)

	const q = `SELECT provider_name, model_id, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens, cost_usd FROM cost_ledger ORDER BY sequence`
	rows, queryErr := s.db.QueryContext(ctx, q)
	if queryErr != nil {
		err = fmt.Errorf("statebackend: query cost ledger: %w", queryErr)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []CostEntry
	for rows.Next() {
		var c CostEntry
		if scanErr := rows.Scan(&c.ProviderName, &c.ModelID, &c.InputTokens, &c.OutputTokens, &c.CacheWriteTokens, &c.CacheReadTokens, &c.CostUSD); scanErr != nil {
			err = fmt.Errorf("statebackend: query cost ledger: %w", scanErr)
			return nil, err
		}
		entries = append(entries, c)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		err = fmt.Errorf("statebackend: query cost ledger: %w", rowsErr)
		return nil, err
	}
	return entries, nil
}

// PlanItems returns every plan_items row, in append order (sequence — the
// sole ordering authority). Returns ErrClosed if Close has already been
// called.
func (s *Session) PlanItems(ctx context.Context) (_ []PlanItem, err error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	ctx, span := s.telemetry.StartStateBackendPlanItemsQuery(ctx, s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: querying plan items", "session_id", s.id)

	const q = `SELECT turn_id, tool_call_id, provider_name, tool_name, decision, decided_by FROM plan_items ORDER BY sequence`
	rows, queryErr := s.db.QueryContext(ctx, q)
	if queryErr != nil {
		err = fmt.Errorf("statebackend: query plan items: %w", queryErr)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []PlanItem
	for rows.Next() {
		var item PlanItem
		var decisionText string
		if scanErr := rows.Scan(&item.TurnID, &item.ToolCallID, &item.ProviderName, &item.ToolName, &decisionText, &item.DecidedBy); scanErr != nil {
			err = fmt.Errorf("statebackend: query plan items: %w", scanErr)
			return nil, err
		}
		decision, decErr := decodePlanDecision(decisionText)
		if decErr != nil {
			err = fmt.Errorf("statebackend: query plan items: %w", decErr)
			return nil, err
		}
		item.Decision = decision
		items = append(items, item)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		err = fmt.Errorf("statebackend: query plan items: %w", rowsErr)
		return nil, err
	}
	return items, nil
}

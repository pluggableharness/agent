package statebackend

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"strings"

	"github.com/pluggableharness/agent/internal/telemetry"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
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

// EventQuery filters a Session.EventsMatching call, mirroring
// docs/specifications/kernel-callbacks.md#readevents' ReadEventsRequest
// field for field. Its zero value is the "everything" query.
type EventQuery struct {
	// Kinds, when non-empty, restricts results to these kinds. Order here
	// does not affect result order — results are always sequence-ascending
	// regardless — and duplicates are ignored. An EVENT_KIND_UNSPECIFIED or
	// otherwise unrecognized entry fails the query with ErrInvalidKind
	// rather than being silently dropped.
	Kinds []kernelv1.EventKind

	// FromSequence, when non-nil, restricts results to sequence >=
	// *FromSequence ("from the start of the session's log" when omitted).
	FromSequence *int64

	// Limit, when non-nil, caps the number of returned rows ("no limit"
	// when omitted). Zero is a legitimate limit and returns no rows; a
	// negative value fails the query, because sqlite would silently read it
	// as "no limit" — the opposite of what any caller passing one means.
	Limit *int32
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
//
// This is exactly EventsMatching with a zero-value EventQuery; there is one
// read path, not two.
func (s *Session) Events(ctx context.Context) iter.Seq2[Event, error] {
	return s.EventsMatching(ctx, EventQuery{})
}

// EventsMatching returns this session's persisted events matching q as a
// sequence-ordered iter.Seq2 — ascending by sequence always, never by
// timestamp (determinism.md#ordering,
// docs/specifications/kernel-callbacks.md#readevents), whatever order q's
// own fields are in. A zero-value EventQuery matches every event and is
// identical to Events. Error and early-break behavior match Events: a
// closed session yields exactly one (Event{}, ErrClosed) pair, and a build,
// query, or decode failure surfaces through the error side of the pair and
// stops iteration.
func (s *Session) EventsMatching(ctx context.Context, q EventQuery) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		if s.closed.Load() {
			yield(Event{}, ErrClosed)
			return
		}

		ctx, span := s.telemetry.StartStateBackendEventsQuery(ctx, s.id)
		var err error
		defer func() { telemetry.EndSpan(span, err) }()
		s.logger.DebugContext(ctx, "statebackend: querying events", "session_id", s.id, "kind_filters", len(q.Kinds))

		query, args, buildErr := buildEventsQuery(q)
		if buildErr != nil {
			err = fmt.Errorf("statebackend: query events: %w", buildErr)
			yield(Event{}, err)
			return
		}

		rows, queryErr := s.db.QueryContext(ctx, query, args...)
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

// eventsSelect is the column list every events read shares — the same
// columns, in the same order, scanEvent expects.
const eventsSelect = `SELECT sequence, id, timestamp, kind, producer_category, producer_name, producer_version, schema_version, payload FROM events`

// buildEventsQuery renders q as a parameterized SELECT plus its bind
// arguments. Only fixed SQL fragments and `?` placeholders are ever
// concatenated into the statement — every value from q is bound, never
// interpolated.
func buildEventsQuery(q EventQuery) (string, []any, error) {
	var (
		conditions []string
		args       []any
	)

	if len(q.Kinds) > 0 {
		kindTexts, err := encodeEventKinds(q.Kinds)
		if err != nil {
			return "", nil, err
		}
		conditions = append(conditions, "kind IN ("+placeholders(len(kindTexts))+")")
		for _, text := range kindTexts {
			args = append(args, text)
		}
	}
	if q.FromSequence != nil {
		conditions = append(conditions, "sequence >= ?")
		args = append(args, *q.FromSequence)
	}

	var b strings.Builder
	b.WriteString(eventsSelect)
	if len(conditions) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(conditions, " AND "))
	}
	b.WriteString(" ORDER BY sequence")
	if q.Limit != nil {
		if *q.Limit < 0 {
			return "", nil, fmt.Errorf("statebackend: limit must not be negative, got %d", *q.Limit)
		}
		b.WriteString(" LIMIT ?")
		args = append(args, *q.Limit)
	}
	return b.String(), args, nil
}

// encodeEventKinds renders kinds as their stored TEXT values, deduplicated
// in first-seen order. The order is derived from the caller's slice alone —
// never from Go map iteration (determinism.md), so the same EventQuery
// always produces a byte-identical statement.
func encodeEventKinds(kinds []kernelv1.EventKind) ([]string, error) {
	texts := make([]string, 0, len(kinds))
	seen := make(map[kernelv1.EventKind]struct{}, len(kinds))
	for _, kind := range kinds {
		if _, dup := seen[kind]; dup {
			continue
		}
		seen[kind] = struct{}{}
		text, err := encodeEventKind(kind)
		if err != nil {
			return nil, err
		}
		texts = append(texts, text)
	}
	return texts, nil
}

// placeholders returns n comma-separated `?` bind placeholders. n is always
// >= 1 at every call site.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
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

	category, err := decodeProducer(categoryText, name)
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
		category, decErr := decodeProducer(categoryText, name)
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

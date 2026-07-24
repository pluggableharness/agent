package statebackend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/pluggableharness/agent/internal/telemetry"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// AppendEvent inserts ev into the events table and upserts its producer's
// row into producers, in one transaction
// (docs/specifications/state-backend.md#events,
// docs/specifications/state-backend.md#producers), returning the sequence
// sqlite assigned. ev.Kind must not be EVENT_KIND_UNSPECIFIED
// (ErrInvalidKind); ev.ID must be unique within this session's file
// (ErrDuplicateEventID on a repeat). Returns ErrClosed if Close has already
// been called.
func (s *Session) AppendEvent(ctx context.Context, ev Event) (_ int64, err error) {
	if s.closed.Load() {
		return 0, ErrClosed
	}
	ctx, span := s.telemetry.StartStateBackendEventAppend(ctx, s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: appending event", "session_id", s.id, "event_id", ev.ID)

	seq, err := s.appendEventTx(ctx, ev, nil)
	return seq, err
}

// AppendMessage inserts ev into the events table and cost into cost_ledger
// (docs/specifications/state-backend.md#cost_ledger), plus the same
// producers upsert AppendEvent does, all in one transaction. Same
// validation and error behavior as AppendEvent.
func (s *Session) AppendMessage(ctx context.Context, ev Event, cost CostEntry) (_ int64, err error) {
	if s.closed.Load() {
		return 0, ErrClosed
	}
	ctx, span := s.telemetry.StartStateBackendMessageAppend(ctx, s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: appending message", "session_id", s.id, "event_id", ev.ID)

	seq, err := s.appendEventTx(ctx, ev, func(ctx context.Context, tx *sql.Tx, eventSeq int64) error {
		return insertCostEntry(ctx, tx, eventSeq, cost)
	})
	return seq, err
}

// AppendPlan inserts ev into the events table and items into plan_items
// (docs/specifications/state-backend.md#plan_items), plus the same
// producers upsert AppendEvent does, all in one transaction. Every item's
// Decision must be ALLOW/ASK/DENY (ErrInvalidDecision otherwise). Same
// event validation and error behavior as AppendEvent.
func (s *Session) AppendPlan(ctx context.Context, ev Event, items []PlanItem) (_ int64, err error) {
	if s.closed.Load() {
		return 0, ErrClosed
	}
	ctx, span := s.telemetry.StartStateBackendPlanAppend(ctx, s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: appending plan", "session_id", s.id, "event_id", ev.ID, "item_count", len(items))

	seq, err := s.appendEventTx(ctx, ev, func(ctx context.Context, tx *sql.Tx, eventSeq int64) error {
		for _, item := range items {
			if err := insertPlanItem(ctx, tx, eventSeq, item); err != nil {
				return err
			}
		}
		return nil
	})
	return seq, err
}

// appendEventTx runs the one transaction shared by AppendEvent,
// AppendMessage, and AppendPlan: insert ev into events, upsert its
// producer row, then — if extra is non-nil — call it with the transaction
// and the just-assigned event sequence to insert any accompanying rows
// (cost_ledger, plan_items). extra returning an error rolls the whole
// transaction back, so the event row never exists without its
// accompanying rows.
func (s *Session) appendEventTx(ctx context.Context, ev Event, extra func(ctx context.Context, tx *sql.Tx, eventSeq int64) error) (int64, error) {
	kindText, err := encodeEventKind(ev.Kind)
	if err != nil {
		return 0, fmt.Errorf("statebackend: append event: %w", err)
	}
	if ev.Producer == nil {
		return 0, fmt.Errorf("statebackend: append event: producer is required")
	}
	categoryText, err := encodeProducerCategory(ev.Producer.GetCategory())
	if err != nil {
		return 0, fmt.Errorf("statebackend: append event: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("statebackend: append event: begin: %w", err)
	}

	seq, err := insertEvent(ctx, tx, ev, kindText, categoryText)
	if err != nil {
		_ = tx.Rollback()
		return 0, mapAppendEventError(err)
	}

	if err := upsertProducer(ctx, tx, categoryText, ev.Producer.GetName(), ev.Producer.GetVersion(), seq); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("statebackend: append event: producer: %w", err)
	}

	if extra != nil {
		if err := extra(ctx, tx, seq); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("statebackend: append event: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("statebackend: append event: commit: %w", err)
	}
	return seq, nil
}

// mapAppendEventError translates a sqlite UNIQUE-constraint violation on
// events.id into ErrDuplicateEventID; any other error is wrapped as-is.
func mapAppendEventError(err error) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return fmt.Errorf("statebackend: append event: %w", ErrDuplicateEventID)
	}
	return fmt.Errorf("statebackend: append event: %w", err)
}

// insertEvent inserts ev into the events table within tx, returning the
// sequence sqlite assigned it.
func insertEvent(ctx context.Context, tx *sql.Tx, ev Event, kindText, categoryText string) (int64, error) {
	const q = `INSERT INTO events (id, timestamp, kind, producer_category, producer_name, producer_version, schema_version, payload) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := tx.ExecContext(ctx, q,
		ev.ID, formatTimestamp(ev.Timestamp), kindText, categoryText,
		ev.Producer.GetName(), ev.Producer.GetVersion(), ev.SchemaVersion, ev.Payload,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// upsertProducer inserts a producers row for (category, name, version)
// within tx if this is the first time this session's file has seen that
// triple (docs/specifications/state-backend.md#producers) — INSERT OR
// IGNORE against the table's (category, name, version) primary key makes a
// repeat sighting a no-op rather than a constraint error.
func upsertProducer(ctx context.Context, tx *sql.Tx, category, name, version string, firstSeenSequence int64) error {
	const q = `INSERT OR IGNORE INTO producers (category, name, version, first_seen_sequence) VALUES (?, ?, ?, ?)`
	_, err := tx.ExecContext(ctx, q, category, name, version, firstSeenSequence)
	return err
}

// insertCostEntry inserts one cost_ledger row within tx, referencing the
// event that produced it.
func insertCostEntry(ctx context.Context, tx *sql.Tx, eventSeq int64, c CostEntry) error {
	const q = `INSERT INTO cost_ledger (event_sequence, provider_name, model_id, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens, cost_usd) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := tx.ExecContext(ctx, q, eventSeq, c.ProviderName, c.ModelID, c.InputTokens, c.OutputTokens, c.CacheWriteTokens, c.CacheReadTokens, c.CostUSD)
	return err
}

// insertPlanItem inserts one plan_items row within tx, referencing the
// event that produced it.
func insertPlanItem(ctx context.Context, tx *sql.Tx, eventSeq int64, item PlanItem) error {
	decisionText, err := encodePlanDecision(item.Decision)
	if err != nil {
		return err
	}
	const q = `INSERT INTO plan_items (event_sequence, turn_id, tool_call_id, provider_name, tool_name, decision, decided_by) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err = tx.ExecContext(ctx, q, eventSeq, item.TurnID, item.ToolCallID, item.ProviderName, item.ToolName, decisionText, item.DecidedBy)
	return err
}

// SetStatus updates session_meta's status and ended_at in place — the one
// mutable table (docs/specifications/state-backend.md#session_meta).
// endedAt is nil while the session keeps running. Returns ErrClosed if
// Close has already been called.
func (s *Session) SetStatus(ctx context.Context, status sessionv1.SessionStatus, endedAt *time.Time) (err error) {
	if s.closed.Load() {
		return ErrClosed
	}
	ctx, span := s.telemetry.StartStateBackendStatusSet(ctx, s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: setting session status", "session_id", s.id, "status", status)

	statusText, encErr := encodeSessionStatus(status)
	if encErr != nil {
		err = fmt.Errorf("statebackend: set status: %w", encErr)
		return err
	}

	var endedAtVal any
	if endedAt != nil {
		endedAtVal = formatTimestamp(*endedAt)
	}

	const q = `UPDATE session_meta SET status = ?, ended_at = ? WHERE session_id = ?`
	result, execErr := s.db.ExecContext(ctx, q, statusText, endedAtVal, s.id)
	if execErr != nil {
		err = fmt.Errorf("statebackend: set status: %w", execErr)
		return err
	}
	rows, raErr := result.RowsAffected()
	if raErr != nil {
		err = fmt.Errorf("statebackend: set status: %w", raErr)
		return err
	}
	if rows == 0 {
		err = fmt.Errorf("statebackend: set status: %w", ErrNotFound)
		return err
	}
	return nil
}

// Close checkpoints the WAL (PRAGMA wal_checkpoint(TRUNCATE), folding it
// back into the main file rather than leaving a -wal sidecar behind) and
// closes the underlying *sql.DB. Idempotent — a second Close is a no-op.
// Every write method on Session (AppendEvent, AppendMessage, AppendPlan,
// SetStatus) returns ErrClosed once Close has been called.
func (s *Session) Close() (err error) {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	ctx, span := s.telemetry.StartStateBackendSessionClose(context.Background(), s.id)
	defer func() { telemetry.EndSpan(span, err) }()
	s.logger.DebugContext(ctx, "statebackend: closing session", "session_id", s.id)

	if _, checkpointErr := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); checkpointErr != nil {
		err = fmt.Errorf("statebackend: close %s: checkpoint: %w", s.id, checkpointErr)
	}
	if closeErr := s.db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("statebackend: close %s: %w", s.id, closeErr))
	}
	return err
}

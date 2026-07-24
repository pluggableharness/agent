package statebackend

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// recoveryStats records, per table, how many rows a recoverSession call
// salvaged versus skipped — logged as part of the "recovery MUST NOT
// happen silently" requirement
// (docs/specifications/state-backend.md#corruption-recovery).
type recoveryStats struct {
	salvaged map[string]int
	skipped  map[string]int
}

// recoveryTableSpec describes one table's shape for recoverTable's
// generic table-by-table copy. columns MUST include the table's own
// primary key column(s) explicitly so recovered rows keep their original
// sequence/identity rather than being renumbered — cost_ledger, plan_items,
// and producers all reference events.sequence by foreign key, and sequence
// is the sole ordering authority anywhere in the kernel (determinism.md),
// so a recovered file MUST NOT renumber it.
type recoveryTableSpec struct {
	name    string
	columns []string
	orderBy string
}

// recoveryTableSpecs lists every append-only table in dependency order:
// events first (nothing depends on it having already been inserted, but
// everything else references it), then its three dependents. session_meta
// is handled separately by recoverSessionMeta since it's the one row that
// makes recovery worth attempting at all.
var recoveryTableSpecs = []recoveryTableSpec{
	{
		name:    "events",
		columns: []string{"sequence", "id", "timestamp", "kind", "producer_category", "producer_name", "producer_version", "schema_version", "payload"},
		orderBy: "sequence",
	},
	{
		name:    "producers",
		columns: []string{"category", "name", "version", "first_seen_sequence"},
		orderBy: "category, name, version",
	},
	{
		name:    "cost_ledger",
		columns: []string{"sequence", "event_sequence", "provider_name", "model_id", "input_tokens", "output_tokens", "cache_write_tokens", "cache_read_tokens", "cost_usd"},
		orderBy: "sequence",
	},
	{
		name:    "plan_items",
		columns: []string{"sequence", "event_sequence", "turn_id", "tool_call_id", "provider_name", "tool_name", "decision", "decided_by"},
		orderBy: "sequence",
	},
}

// checkIntegrity implements the Stage 1 seam for real: it opens
// sessionID's file at path, runs PRAGMA integrity_check, and — if the
// file can't even be opened, or integrity_check reports problems —
// attempts salvage recovery per
// docs/specifications/state-backend.md#corruption-recovery. It returns the
// *sql.DB to use going forward: db opened directly on path when healthy,
// or a fresh handle on the recovered file after a successful salvage.
// ErrUnrecoverable if salvage itself fails.
func (st *Store) checkIntegrity(ctx context.Context, path, sessionID string) (_ *sql.DB, err error) {
	ctx, span := st.telemetry.StartStateBackendIntegrityCheck(ctx, sessionID)
	defer func() { telemetry.EndSpan(span, err) }()

	db, openErr := openDB(ctx, path)
	if openErr == nil {
		problems, checkErr := runIntegrityCheck(ctx, db)
		if checkErr == nil && len(problems) == 0 {
			return db, nil
		}
		_ = db.Close()
		if checkErr != nil {
			st.logger.WarnContext(ctx, "statebackend: integrity check could not complete, attempting recovery", "session_id", sessionID, "err", checkErr)
		} else {
			st.logger.WarnContext(ctx, "statebackend: session file failed integrity check, attempting recovery", "session_id", sessionID, "problem_count", len(problems), "problems", strings.Join(problems, "; "))
		}
	} else {
		st.logger.WarnContext(ctx, "statebackend: session file could not be opened, attempting recovery", "session_id", sessionID, "err", openErr)
	}

	// Either the file couldn't even be opened, or it opened but failed
	// integrity_check — both routes attempt the same salvage, per
	// state-backend.md's corruption-recovery section, which doesn't
	// distinguish the two: both mean "this file's contents can't be
	// trusted as-is."
	corruptPath := path + ".corrupt"
	if renameErr := os.Rename(path, corruptPath); renameErr != nil {
		err = fmt.Errorf("statebackend: recover %s: rename damaged file: %w", sessionID, renameErr)
		return nil, err
	}

	recovered, stats, recErr := st.recoverSession(ctx, corruptPath, path, sessionID)
	if recErr != nil {
		st.logger.WarnContext(ctx, "statebackend: recovery failed, session flagged unreadable", "session_id", sessionID, "corrupt_path", corruptPath, "err", recErr)
		err = fmt.Errorf("statebackend: recover %s: %w", sessionID, ErrUnrecoverable)
		return nil, err
	}

	st.logger.WarnContext(ctx, "statebackend: session recovered from corruption",
		"session_id", sessionID, "corrupt_path", corruptPath,
		"events_salvaged", stats.salvaged["events"], "events_skipped", stats.skipped["events"],
		"producers_salvaged", stats.salvaged["producers"], "producers_skipped", stats.skipped["producers"],
		"cost_ledger_salvaged", stats.salvaged["cost_ledger"], "cost_ledger_skipped", stats.skipped["cost_ledger"],
		"plan_items_salvaged", stats.salvaged["plan_items"], "plan_items_skipped", stats.skipped["plan_items"],
	)
	return recovered, nil
}

// runIntegrityCheck runs PRAGMA integrity_check against db
// (docs/specifications/state-backend.md#corruption-recovery names this
// pragma specifically — not quick_check, which trades thoroughness for
// speed) and returns the problem strings sqlite reports. A healthy
// database returns exactly one row containing "ok", which this function
// reports as a nil, empty result. A non-nil error means the check itself
// could not run to completion — the file is damaged badly enough that
// even the diagnostic query fails.
func runIntegrityCheck(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var problems []string
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			return nil, err
		}
		if msg != "ok" {
			problems = append(problems, msg)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return problems, nil
}

// recoverSession attempts state-backend.md's salvage: a fresh,
// schema-correct file built at dstPath+".recovering", populated
// table-by-table from srcPath (the just-renamed damaged original),
// tolerating per-row read/insert failures. Recovery is only considered
// successful once session_meta — the one row every session file must
// carry — is itself salvaged; every other table is best-effort on top of
// that. On success, the recovered file is installed at dstPath (the
// original session filename, per spec — the .corrupt rename already
// happened in checkIntegrity before this is called) and the returned
// *sql.DB is opened on it.
func (st *Store) recoverSession(ctx context.Context, srcPath, dstPath, sessionID string) (*sql.DB, *recoveryStats, error) {
	recoveryPath := dstPath + ".recovering"
	_ = os.Remove(recoveryPath) // best-effort: clear any stale attempt left by a previous crashed recovery

	recDB, err := openDB(ctx, recoveryPath)
	if err != nil {
		return nil, nil, fmt.Errorf("create recovery file: %w", err)
	}
	// cleanupRecoveryFile guards a half-built recovery attempt: true until
	// the file is fully populated and closed, at which point it becomes a
	// complete, valid artifact worth preserving even if the final install
	// rename below fails (never destroy salvaged data).
	cleanupRecoveryFile := true
	defer func() {
		if cleanupRecoveryFile {
			_ = recDB.Close()
			_ = os.Remove(recoveryPath)
		}
	}()

	if err := initSchema(ctx, recDB); err != nil {
		return nil, nil, fmt.Errorf("init recovery schema: %w", err)
	}

	// #nosec G304 -- srcPath is the Store's own just-renamed .corrupt file (path+".corrupt"), derived from an already-ValidateSessionID-checked session ID, not attacker-controlled input.
	srcDB, err := sql.Open("sqlite", srcPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open damaged file for reading: %w", err)
	}
	defer func() { _ = srcDB.Close() }()

	if !recoverSessionMeta(ctx, srcDB, recDB, sessionID) {
		return nil, nil, fmt.Errorf("session_meta row unreadable: %w", ErrUnrecoverable)
	}

	stats := &recoveryStats{salvaged: make(map[string]int, len(recoveryTableSpecs)), skipped: make(map[string]int, len(recoveryTableSpecs))}
	for _, spec := range recoveryTableSpecs {
		salvaged, skipped := recoverTable(ctx, srcDB, recDB, spec)
		stats.salvaged[spec.name] = salvaged
		stats.skipped[spec.name] = skipped
	}

	if err := recDB.Close(); err != nil {
		return nil, nil, fmt.Errorf("close recovery file: %w", err)
	}
	cleanupRecoveryFile = false // the recovery file is complete and valid from here on, regardless of what happens below.

	if err := os.Rename(recoveryPath, dstPath); err != nil {
		return nil, nil, fmt.Errorf("install recovered file (recovered data preserved at %s): %w", recoveryPath, err)
	}

	newDB, err := openDB(ctx, dstPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reopen recovered file: %w", err)
	}
	return newDB, stats, nil
}

// recoverSessionMeta copies session_meta's single row from src into dst,
// reporting whether it succeeded.
func recoverSessionMeta(ctx context.Context, src, dst *sql.DB, sessionID string) bool {
	meta, err := querySessionMeta(ctx, src, sessionID)
	if err != nil {
		return false
	}
	if err := insertSessionMeta(ctx, dst, meta); err != nil {
		return false
	}
	return true
}

// recoverTable performs spec's best-effort, per-row-tolerant table copy
// from src to dst per spec: a row that fails to scan, or fails to insert
// (including a foreign-key violation against a parent row that was itself
// skipped), is counted as skipped and the copy continues with the next
// row rather than aborting the whole table. If the SELECT itself can't
// even start (the table's pages are entirely unreadable), the table is
// reported as 0 salvaged, 0 skipped rather than erroring the whole
// recovery — that's exactly the "tolerate per-row/per-table failure"
// posture this function exists for.
func recoverTable(ctx context.Context, src, dst *sql.DB, spec recoveryTableSpec) (salvaged, skipped int) {
	// #nosec G201 -- spec's name/columns/orderBy all come from the hardcoded, package-level recoveryTableSpecs slice above, never from caller or row data; the actual row values are always bound as placeholders below, not interpolated.
	selectQuery := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", strings.Join(spec.columns, ", "), spec.name, spec.orderBy)
	rows, err := src.QueryContext(ctx, selectQuery)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = rows.Close() }()

	placeholders := make([]string, len(spec.columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	// #nosec G201 -- same reasoning as selectQuery above: spec.name/columns are hardcoded, never caller-controlled.
	insertQuery := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", spec.name, strings.Join(spec.columns, ", "), strings.Join(placeholders, ", "))

	for rows.Next() {
		values := make([]any, len(spec.columns))
		scanDests := make([]any, len(spec.columns))
		for i := range values {
			scanDests[i] = &values[i]
		}
		if err := rows.Scan(scanDests...); err != nil {
			skipped++
			continue
		}
		if _, err := dst.ExecContext(ctx, insertQuery, values...); err != nil {
			skipped++
			continue
		}
		salvaged++
	}
	// rows.Err() being non-nil here means iteration was cut short by a
	// read error partway through the table. Everything already salvaged
	// above is kept; the unread remainder simply isn't separately
	// countable through this API, so it shows up only as
	// salvaged+skipped summing to less than the table's original row
	// count in the recovery log — never as a hard failure of the whole
	// recovery.

	return salvaged, skipped
}

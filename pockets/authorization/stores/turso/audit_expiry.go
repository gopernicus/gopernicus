package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

// The retention migration and the floor table 0008 creates.
const (
	auditRetentionMigration = "0008_iam_audit_retention.sql"
	auditRetentionTable     = "iam_audit_retention"
	auditTriggersChangedKey = "rail_triggers_changed"
)

// The purge transaction's fault-injection stages.
const (
	stageDropped  = "dropped"
	stageRecorded = "recorded"
)

// auditExpiry implements audit.Expirer over libSQL. The 0006 triggers abort
// every DELETE on iam_audit, from every connection, so each call is ONE short
// BEGIN IMMEDIATE transaction that reads the 0008 floor, captures iam_audit's
// triggers from sqlite_schema, drops exactly those, deletes the batch,
// re-creates the captured text verbatim and only then appends the
// iam_audit_purges row. Restoring what was captured — never the embedded 0006 —
// leaves a host that declined 0006 without triggers and a host-altered trigger
// unrepaired, and the restored text is byte-identical to what the boot probe
// compares. After COMMIT the trigger set is read again and a difference logs
// ERROR rail_triggers_changed. A cutoff younger than now - floor_days is
// audit.ErrBelowRetentionFloor.
type auditExpiry struct {
	db *tursodb.DB

	// fault, when set, runs inside the purge transaction at each named stage —
	// stageDropped after the DROP TRIGGERs, stageRecorded after the evidence
	// row, before COMMIT; an error rolls the transaction back. Tests inject
	// faults through it.
	fault func(ctx context.Context, tx *tursodb.Tx, stage string) error
}

// auditTrigger is one trigger on iam_audit as sqlite_schema holds it.
type auditTrigger struct {
	name string
	sql  string
}

var _ audit.Expirer = (*auditExpiry)(nil)

// Expire deletes up to min(limit, audit.MaxExpireLimit) records with
// occurred_at <= before, oldest first, in one capture-drop-delete-restore-
// evidence transaction, and appends ONE iam_audit_purges row, a zero count
// included. It refuses an ambient transaction of e.db.
func (e *auditExpiry) Expire(ctx context.Context, before time.Time, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if _, ok := e.db.TxFrom(ctx); ok {
		return 0, mutations.ErrMutationInsideTransaction
	}
	if limit <= 0 {
		return 0, fmt.Errorf("authorization audit expiry: limit must be positive, got %d: %w", limit, sdk.ErrInvalidInput)
	}
	limit = min(limit, audit.MaxExpireLimit)
	var (
		captured []auditTrigger
		purged   int64
	)
	err := retryBusy(ctx, func() error {
		return e.db.InTx(ctx, func(tx *tursodb.Tx) error {
			floorDays, err := readAuditFloorDays(ctx, tx)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			if before.After(now.Add(-time.Duration(floorDays) * 24 * time.Hour)) {
				return fmt.Errorf("authorization turso store: cutoff %s is below the retention floor of %d days: %w",
					before.UTC().Format(time.RFC3339Nano), floorDays, audit.ErrBelowRetentionFloor)
			}
			// (1) Capture, and close the rows before any DDL: an open statement
			// on a local connection fails DDL with SQLITE_LOCKED.
			if captured, err = auditTriggers(ctx, tx); err != nil {
				return err
			}
			// (2) Drop exactly the captured triggers.
			for _, t := range captured {
				if _, err := tx.Exec(ctx, `DROP TRIGGER `+quoteName(t.name)); err != nil {
					return err
				}
			}
			if err := e.inject(ctx, tx, stageDropped); err != nil {
				return err
			}
			// (3) The bounded delete.
			res, err := tx.Exec(ctx, `DELETE FROM main.iam_audit WHERE id IN (SELECT id FROM main.iam_audit WHERE occurred_at <= ? ORDER BY occurred_at, id LIMIT ?)`, tursodb.FormatTime(before), limit)
			if err != nil {
				return err
			}
			if purged, err = res.RowsAffected(); err != nil {
				return tursodb.MapError(err)
			}
			// (4) Restore the captured text verbatim.
			for _, t := range captured {
				if _, err := tx.Exec(ctx, t.sql); err != nil {
					return err
				}
			}
			// (5) The evidence, after the restore, by plain INSERT.
			if _, err := tx.Exec(ctx, `INSERT INTO main.iam_audit_purges (cutoff, purged, floor_days, max_rows, recorded_at) VALUES (?, ?, ?, ?, ?)`,
				tursodb.FormatTime(before), purged, floorDays, limit, tursodb.FormatTime(now)); err != nil {
				return err
			}
			return e.inject(ctx, tx, stageRecorded)
		})
	})
	if err != nil {
		return 0, err
	}
	// (6) After COMMIT, the trigger set must be what was captured.
	if after, err := auditTriggers(ctx, e.db); err != nil || !slices.Equal(after, captured) {
		slog.Default().ErrorContext(ctx, "authorization turso store: the iam_audit trigger set differs from the one the purge captured",
			"error_kind", auditTriggersChangedKey, "captured", capturedTriggerNames(captured), "after", capturedTriggerNames(after), "read_error", err)
	}
	return int(purged), nil
}

// inject runs the fault hook at stage, if one is set.
func (e *auditExpiry) inject(ctx context.Context, tx *tursodb.Tx, stage string) error {
	if e.fault == nil {
		return nil
	}
	return e.fault(ctx, tx, stage)
}

// readAuditFloorDays reads row 1 of the floor table; a missing row (or table)
// is a refusal naming 0008, never a default.
func readAuditFloorDays(ctx context.Context, q tursodb.Querier) (int, error) {
	var days int
	err := q.QueryRow(ctx, `SELECT floor_days FROM main.`+auditRetentionTable+` WHERE id = 1`).Scan(&days)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("authorization turso store: %s row 1 missing — apply migration %s from the %q migration source: %w",
			auditRetentionTable, auditRetentionMigration, "authorization", sdk.ErrNotFound)
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return 0, fmt.Errorf("authorization turso store: %s table missing — apply migration %s from the %q migration source: %w",
			auditRetentionTable, auditRetentionMigration, "authorization", sdk.ErrNotFound)
	case err != nil:
		return 0, tursodb.MapError(err)
	}
	return days, nil
}

// auditTriggers reads the triggers on iam_audit, in name order, closing the
// rows before it returns.
func auditTriggers(ctx context.Context, q tursodb.Querier) ([]auditTrigger, error) {
	rows, err := q.Query(ctx, `SELECT name, sql FROM main.sqlite_schema WHERE type = 'trigger' AND tbl_name = 'iam_audit' COLLATE NOCASE ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditTrigger
	for rows.Next() {
		var t auditTrigger
		if err := rows.Scan(&t.name, &t.sql); err != nil {
			return nil, tursodb.MapError(err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, rows.Close()
}

// quoteName renders a catalog name as an SQL identifier.
func quoteName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func capturedTriggerNames(ts []auditTrigger) []string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.name
	}
	return names
}

package pgx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5/pgconn"
)

// The retention objects migration 0008 creates. auditPurgeFunction is SECURITY
// DEFINER, owned by iam_audit's owner; its argument list is written
// pg_catalog-qualified, as to_regprocedure and the posture probe bind it.
const (
	auditRetentionMigration    = "0008_iam_audit_retention.sql"
	auditPurgeFunction         = "iam_audit_purge_before"
	auditPurgeFunctionArgs     = "(pg_catalog.timestamptz, pg_catalog.int4)"
	belowFloorSQLState         = "22023"
	insufficientPrivilegeState = "42501"
	undefinedFunctionSQLState  = "42883"
	functionDeniedLead         = "permission denied for function"
)

// auditExpiry implements audit.Expirer. The runtime role holds no DELETE on
// iam_audit (0006), so each call is the 0008 SECURITY DEFINER function, which
// refuses a cutoff younger than the database floor, deletes the batch oldest
// first and appends one iam_audit_purges row in the same statement.
type auditExpiry struct {
	db     *pgxdb.DB
	schema pgxdb.Schema
}

var _ audit.Expirer = (*auditExpiry)(nil)

// Expire calls iam_audit_purge_before once. The arguments are cast to the exact
// signature so no overload can make the call ambiguous, and the name is
// schema-qualified under WithSchema. It refuses an ambient transaction of e.db.
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
	q := `SELECT cutoff, purged FROM ` + e.schema.Table(auditPurgeFunction) + `($1::pg_catalog.timestamptz, $2::pg_catalog.int4)`
	var (
		cutoff time.Time
		purged int64
	)
	if err := e.db.QueryRow(ctx, q, before.UTC(), int32(min(limit, audit.MaxExpireLimit))).Scan(&cutoff, &purged); err != nil {
		return 0, e.purgeError(err)
	}
	return int(purged), nil
}

// purgeError maps the definer call's failures: 22023 is the floor refusal; a
// 42501 naming the function is the missing EXECUTE grant, any other 42501 is
// the 0006 trigger refusing the function's own delete; 42883 is a database
// without 0008.
func (e *auditExpiry) purgeError(err error) error {
	signature := e.schema.Table(auditPurgeFunction) + auditPurgeFunctionArgs
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == belowFloorSQLState:
			return fmt.Errorf("authorization pgx store: %s: %w", pgErr.Message, audit.ErrBelowRetentionFloor)
		case pgErr.Code == insufficientPrivilegeState && strings.Contains(pgErr.Message, functionDeniedLead):
			return fmt.Errorf("authorization pgx store: the runtime role cannot execute %s; fix: GRANT EXECUTE ON FUNCTION %s TO <runtime role>: %w",
				auditPurgeFunction, signature, pgxdb.MapError(err))
		case pgErr.Code == insufficientPrivilegeState:
			return fmt.Errorf("authorization pgx store: iam_audit refused the delete of %s — it must be SECURITY DEFINER and owned by a member of the table's owner (ALTER FUNCTION %s SECURITY DEFINER; ALTER FUNCTION %s OWNER TO <table owner>): %w",
				auditPurgeFunction, signature, signature, pgxdb.MapError(err))
		case pgErr.Code == undefinedFunctionSQLState && strings.Contains(pgErr.Message, auditPurgeFunction):
			return fmt.Errorf("authorization pgx store: %s is missing — apply migration %s from the %q migration source: %w",
				auditPurgeFunction, auditRetentionMigration, migrationSource, sdk.ErrNotFound)
		}
	}
	return pgxdb.MapError(err)
}

package pgx

import (
	"context"
	"errors"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/jackc/pgx/v5"
)

// serviceAccountStatusMigration is the file that creates
// serviceAccountStatusTable; ProbeSchema names it.
const serviceAccountStatusMigration = "0027_service_account_status.sql"

// ServiceAccountStatusStore implements serviceaccount.StatusRepository over a
// PostgreSQL database (SOC 2 S4d). Disable and Enable lock the service account
// row FOR UPDATE first — the lock APIKeyStore.Create takes FOR SHARE and
// DeleteWithKeys FOR UPDATE — so a mint racing a disable either commits first
// and is revoked here, or waits and is inserted after the status row commits,
// where the pocket's post-mint status re-read revokes it. Repositories never
// probes its table: it is read only when the host wires the capability.
type ServiceAccountStatusStore struct {
	db *pgxdb.DB
	qualified
}

var _ serviceaccount.StatusRepository = (*ServiceAccountStatusStore)(nil)

// NewServiceAccountStatusStore returns a ServiceAccountStatusStore backed by
// db. It panics if db is nil; the caller owns the database lifecycle.
func NewServiceAccountStatusStore(db *pgxdb.DB, opts ...Option) *ServiceAccountStatusStore {
	if db == nil {
		panic("authentication pgx: NewServiceAccountStatusStore received a nil database")
	}
	return &ServiceAccountStatusStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// ProbeSchema refuses a database without migration 0027's table with
// sdk.ErrNotFound naming the file.
func (s *ServiceAccountStatusStore) ProbeSchema(ctx context.Context) error {
	return s.probeMigrationTable(ctx, s.db, serviceAccountStatusTable, serviceAccountStatusMigration)
}

// GetStatus returns the account's status; an absent row is StatusActive.
func (s *ServiceAccountStatusStore) GetStatus(ctx context.Context, id string) (serviceaccount.Status, error) {
	var status string
	err := s.db.QueryRow(ctx, `SELECT status FROM `+s.table(serviceAccountStatusTable)+` WHERE service_account_id = @id`,
		pgx.NamedArgs{"id": id}).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return serviceaccount.StatusActive, nil
	case err != nil:
		return "", pgxdb.MapError(err)
	}
	return serviceaccount.Status(status), nil
}

// Statuses returns the statuses of ids in one query; an id without a row is
// StatusActive.
func (s *ServiceAccountStatusStore) Statuses(ctx context.Context, ids []string) (map[string]serviceaccount.Status, error) {
	out := make(map[string]serviceaccount.Status, len(ids))
	for _, id := range ids {
		out[id] = serviceaccount.StatusActive
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT service_account_id, status FROM `+s.table(serviceAccountStatusTable)+` WHERE service_account_id = ANY(@ids)`,
		pgx.NamedArgs{"ids": ids})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, pgxdb.MapError(err)
		}
		out[id] = serviceaccount.Status(status)
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.MapError(err)
	}
	return out, nil
}

// Disable records StatusDisabled and revokes every unrevoked key of the account
// as of now, in one transaction; unknown account → sdk.ErrNotFound.
func (s *ServiceAccountStatusStore) Disable(ctx context.Context, id, changedBy string, now time.Time) (serviceaccount.StatusChange, error) {
	var change serviceaccount.StatusChange
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		current, err := s.lockStatus(ctx, tx, id)
		if err != nil || current == serviceaccount.StatusDisabled {
			return err
		}
		if err := s.writeStatus(ctx, tx, id, serviceaccount.StatusDisabled, changedBy, now); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE `+s.table(apiKeysTable)+` SET revoked_at = @now WHERE service_account_id = @id AND revoked_at IS NULL`,
			pgx.NamedArgs{"id": id, "now": now.UTC()})
		if err != nil {
			return err
		}
		change = serviceaccount.StatusChange{Changed: true, KeysRevoked: int(tag.RowsAffected())}
		return nil
	})
	if err != nil {
		return serviceaccount.StatusChange{}, err
	}
	return change, nil
}

// Enable records StatusActive; revoked keys stay revoked. Unknown account →
// sdk.ErrNotFound.
func (s *ServiceAccountStatusStore) Enable(ctx context.Context, id, changedBy string, now time.Time) (serviceaccount.StatusChange, error) {
	var change serviceaccount.StatusChange
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		current, err := s.lockStatus(ctx, tx, id)
		if err != nil || current == serviceaccount.StatusActive {
			return err
		}
		if err := s.writeStatus(ctx, tx, id, serviceaccount.StatusActive, changedBy, now); err != nil {
			return err
		}
		change.Changed = true
		return nil
	})
	if err != nil {
		return serviceaccount.StatusChange{}, err
	}
	return change, nil
}

// Delete removes the account's status row; an absent row is not an error.
func (s *ServiceAccountStatusStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM `+s.table(serviceAccountStatusTable)+` WHERE service_account_id = @id`, pgx.NamedArgs{"id": id})
	return pgxdb.MapError(err)
}

// lockStatus locks the account row FOR UPDATE (sdk.ErrNotFound when absent)
// and returns its current status, locking the status row too when present.
func (s *ServiceAccountStatusStore) lockStatus(ctx context.Context, tx *pgxdb.Tx, id string) (serviceaccount.Status, error) {
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id FROM `+s.table(serviceAccountsTable)+` WHERE id = @id FOR UPDATE`,
		pgx.NamedArgs{"id": id}).Scan(&locked); err != nil {
		return "", pgxdb.MapError(err)
	}
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM `+s.table(serviceAccountStatusTable)+` WHERE service_account_id = @id FOR UPDATE`,
		pgx.NamedArgs{"id": id}).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return serviceaccount.StatusActive, nil
	case err != nil:
		return "", pgxdb.MapError(err)
	}
	return serviceaccount.Status(status), nil
}

func (s *ServiceAccountStatusStore) writeStatus(ctx context.Context, tx *pgxdb.Tx, id string, status serviceaccount.Status, changedBy string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO `+s.table(serviceAccountStatusTable)+` (service_account_id, status, changed_at, changed_by)
		VALUES (@id, @status, @now, @changed_by)
		ON CONFLICT (service_account_id) DO UPDATE SET status = excluded.status, changed_at = excluded.changed_at, changed_by = excluded.changed_by`,
		pgx.NamedArgs{"id": id, "status": string(status), "now": now.UTC(), "changed_by": changedBy})
	return err
}

package turso

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
)

const (
	// serviceAccountStatusTable is the table migration 0027 creates.
	serviceAccountStatusTable = "service_account_status"
	// serviceAccountStatusMigration is that file; ProbeSchema names it.
	serviceAccountStatusMigration = "0027_service_account_status.sql"
)

// ServiceAccountStatusStore implements serviceaccount.StatusRepository over a
// libSQL database (SOC 2 S4d). Disable and Enable each run in one BEGIN
// IMMEDIATE write transaction, so a mint (a single INSERT) lands wholly before
// a disable — and is revoked by it — or after, where the pocket's post-mint
// status re-read revokes it. Repositories never probes its table: it is read
// only when the host wires the capability.
type ServiceAccountStatusStore struct {
	db *tursodb.DB
}

var _ serviceaccount.StatusRepository = (*ServiceAccountStatusStore)(nil)

// NewServiceAccountStatusStore returns a ServiceAccountStatusStore backed by
// db. It panics if db is nil; the caller owns the database lifecycle.
func NewServiceAccountStatusStore(db *tursodb.DB) *ServiceAccountStatusStore {
	if db == nil {
		panic("authentication turso: NewServiceAccountStatusStore received a nil database")
	}
	return &ServiceAccountStatusStore{db: db}
}

// ProbeSchema refuses a database without migration 0027's table with
// sdk.ErrNotFound naming the file.
func (s *ServiceAccountStatusStore) ProbeSchema(ctx context.Context) error {
	return probeMigrationTable(ctx, s.db, serviceAccountStatusTable, serviceAccountStatusMigration)
}

// GetStatus returns the account's status; an absent row is StatusActive.
func (s *ServiceAccountStatusStore) GetStatus(ctx context.Context, id string) (serviceaccount.Status, error) {
	return readStatus(ctx, s.db, id)
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
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(ctx, `SELECT service_account_id, status FROM service_account_status WHERE service_account_id IN (?`+strings.Repeat(", ?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, tursodb.MapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, tursodb.MapError(err)
		}
		out[id] = serviceaccount.Status(status)
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}

// Disable records StatusDisabled and revokes every unrevoked key of the account
// as of now, in one transaction; unknown account → sdk.ErrNotFound.
func (s *ServiceAccountStatusStore) Disable(ctx context.Context, id, changedBy string, now time.Time) (serviceaccount.StatusChange, error) {
	var change serviceaccount.StatusChange
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		current, err := accountStatus(ctx, tx, id)
		if err != nil || current == serviceaccount.StatusDisabled {
			return err
		}
		if err := writeStatus(ctx, tx, id, serviceaccount.StatusDisabled, changedBy, now); err != nil {
			return err
		}
		res, err := tx.Exec(ctx, `UPDATE api_keys SET revoked_at = ? WHERE service_account_id = ? AND revoked_at IS NULL`, tursodb.FormatTime(now), id)
		if err != nil {
			return err
		}
		revoked, err := affected(res)
		if err != nil {
			return err
		}
		change = serviceaccount.StatusChange{Changed: true, KeysRevoked: revoked}
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
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		current, err := accountStatus(ctx, tx, id)
		if err != nil || current == serviceaccount.StatusActive {
			return err
		}
		if err := writeStatus(ctx, tx, id, serviceaccount.StatusActive, changedBy, now); err != nil {
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
	_, err := s.db.Exec(ctx, `DELETE FROM service_account_status WHERE service_account_id = ?`, id)
	return tursodb.MapError(err)
}

// accountStatus reads the account's status inside tx; an unknown account →
// sdk.ErrNotFound.
func accountStatus(ctx context.Context, tx *tursodb.Tx, id string) (serviceaccount.Status, error) {
	var found string
	if err := tx.QueryRow(ctx, `SELECT id FROM service_accounts WHERE id = ?`, id).Scan(&found); err != nil {
		return "", tursodb.MapError(err)
	}
	return readStatus(ctx, tx, id)
}

func readStatus(ctx context.Context, q tursodb.Querier, id string) (serviceaccount.Status, error) {
	var status string
	err := q.QueryRow(ctx, `SELECT status FROM service_account_status WHERE service_account_id = ?`, id).Scan(&status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return serviceaccount.StatusActive, nil
	case err != nil:
		return "", tursodb.MapError(err)
	}
	return serviceaccount.Status(status), nil
}

func writeStatus(ctx context.Context, tx *tursodb.Tx, id string, status serviceaccount.Status, changedBy string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO service_account_status (service_account_id, status, changed_at, changed_by) VALUES (?, ?, ?, ?)
		ON CONFLICT (service_account_id) DO UPDATE SET status = excluded.status, changed_at = excluded.changed_at, changed_by = excluded.changed_by`,
		id, string(status), tursodb.FormatTime(now), changedBy)
	return err
}

package pgx

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// passkeyMigration is the file that creates passkeyCredentialsTable;
// ProbeSchema names it.
const passkeyMigration = "0025_passkey_credentials.sql"

const passkeyColumns = "id, user_id, name, credential_id, public_key, aaguid, user_handle, sign_count, transports, user_verified, backup_eligible, backup_state, created_at, last_used_at"

// PasskeyStore implements passkey.Repository over a PostgreSQL database
// (SOC 2 S4b). Create runs its guard under the owner's users row lock — the
// lock the credential-mutation rail takes — so the revision fence, the
// bootstrap guard, the cap and the user handle hold under concurrency. RecordUse
// is a single conditional UPDATE on the stored sign count.
type PasskeyStore struct {
	db *pgxdb.DB
	qualified
}

// passkeyProbeTables are the tables migration 0025 creates. Repositories never
// probes them: they are required only when the host wires passkeys, and the
// pocket asks through PasskeyStore.ProbeSchema (passkey.SchemaProber) only then.
var passkeyProbeTables = []string{passkeyCredentialsTable}

var (
	_ passkey.Repository   = (*PasskeyStore)(nil)
	_ passkey.SchemaProber = (*PasskeyStore)(nil)
)

// NewPasskeyStore returns a PasskeyStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewPasskeyStore(db *pgxdb.DB, opts ...Option) *PasskeyStore {
	if db == nil {
		panic("authentication pgx: NewPasskeyStore received a nil database")
	}
	return &PasskeyStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// ProbeSchema refuses a database without migration 0025's table with
// sdk.ErrNotFound naming the file. The pocket calls it only when passkeys are
// wired.
func (s *PasskeyStore) ProbeSchema(ctx context.Context) error {
	for _, table := range passkeyProbeTables {
		if err := s.probeMigrationTable(ctx, s.db, table, passkeyMigration); err != nil {
			return err
		}
	}
	return nil
}

// passkeyRow is the store-local, db-tagged projection of a passkey_credentials
// row.
type passkeyRow struct {
	ID             string     `db:"id"`
	UserID         string     `db:"user_id"`
	Name           string     `db:"name"`
	CredentialID   []byte     `db:"credential_id"`
	PublicKey      []byte     `db:"public_key"`
	AAGUID         []byte     `db:"aaguid"`
	UserHandle     []byte     `db:"user_handle"`
	SignCount      int64      `db:"sign_count"`
	Transports     string     `db:"transports"`
	UserVerified   bool       `db:"user_verified"`
	BackupEligible bool       `db:"backup_eligible"`
	BackupState    bool       `db:"backup_state"`
	CreatedAt      time.Time  `db:"created_at"`
	LastUsedAt     *time.Time `db:"last_used_at"`
}

func (r passkeyRow) toDomain() (passkey.Passkey, error) {
	var transports []string
	if err := json.Unmarshal([]byte(r.Transports), &transports); err != nil {
		return passkey.Passkey{}, fmt.Errorf("authentication pgx store: passkey %s transports: %w", r.ID, err)
	}
	if len(transports) == 0 {
		transports = nil
	}
	p := passkey.Passkey{
		ID:             r.ID,
		UserID:         r.UserID,
		Name:           r.Name,
		CredentialID:   r.CredentialID,
		PublicKey:      r.PublicKey,
		AAGUID:         r.AAGUID,
		UserHandle:     r.UserHandle,
		SignCount:      uint32(r.SignCount),
		Transports:     transports,
		UserVerified:   r.UserVerified,
		BackupEligible: r.BackupEligible,
		BackupState:    r.BackupState,
		CreatedAt:      r.CreatedAt.UTC(),
	}
	if r.LastUsedAt != nil {
		at := r.LastUsedAt.UTC()
		p.LastUsedAt = &at
	}
	return p, nil
}

// nonNilBytes binds a nil slice as an empty value for a NOT NULL BYTEA column.
func nonNilBytes(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

// Create persists in.Passkey under the guard. See passkey.Repository.
func (s *PasskeyStore) Create(ctx context.Context, in passkey.CreateInput) (passkey.Passkey, error) {
	p := in.Passkey
	transports := p.Transports
	if transports == nil {
		transports = []string{}
	}
	encoded, err := json.Marshal(transports)
	if err != nil {
		return passkey.Passkey{}, fmt.Errorf("authentication pgx store: passkey transports: %w", err)
	}
	p.CreatedAt = p.CreatedAt.UTC()
	p.LastUsedAt = nil
	err = s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var err error
		p.ID, err = createPasskey(ctx, tx, s.qualified, p, string(encoded), in)
		return err
	})
	if err != nil {
		return passkey.Passkey{}, err
	}
	return p, nil
}

// GetByCredentialID returns the passkey whose raw credential id is credentialID,
// or sdk.ErrNotFound.
func (s *PasskeyStore) GetByCredentialID(ctx context.Context, credentialID []byte) (passkey.Passkey, error) {
	q := `SELECT ` + passkeyColumns + ` FROM ` + s.table(passkeyCredentialsTable) + ` WHERE credential_id = @credential_id`
	row, err := pgxdb.QueryOne[passkeyRow](ctx, s.db, q, pgx.NamedArgs{"credential_id": nonNilBytes(credentialID)})
	if err != nil {
		return passkey.Passkey{}, err
	}
	return row.toDomain()
}

// ListByUser returns userID's passkeys oldest first (an empty slice, nil error,
// when none).
func (s *PasskeyStore) ListByUser(ctx context.Context, userID string) ([]passkey.Passkey, error) {
	q := `SELECT ` + passkeyColumns + ` FROM ` + s.table(passkeyCredentialsTable) + ` WHERE user_id = @user_id ORDER BY created_at, id`
	rows, err := pgxdb.Collect[passkeyRow](ctx, s.db, q, pgx.NamedArgs{"user_id": userID})
	if err != nil {
		return nil, err
	}
	out := make([]passkey.Passkey, 0, len(rows))
	for _, r := range rows {
		p, err := r.toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Rename sets the name of userID's passkey id; an id userID does not own →
// sdk.ErrNotFound.
func (s *PasskeyStore) Rename(ctx context.Context, id, userID, name string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, `UPDATE `+s.table(passkeyCredentialsTable)+` SET name = @name WHERE id = @id AND user_id = @user_id`,
		pgx.NamedArgs{"name": name, "id": id, "user_id": userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}

// RecordUse advances the sign count, backup state and last use only while the
// stored count still equals expected; a stale count or a missing row →
// sdk.ErrConflict.
func (s *PasskeyStore) RecordUse(ctx context.Context, id string, expected, count uint32, backupState bool, at time.Time) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, `UPDATE `+s.table(passkeyCredentialsTable)+`
		SET sign_count = @count, backup_state = @backup_state, last_used_at = @at
		WHERE id = @id AND sign_count = @expected`,
		pgx.NamedArgs{"count": int64(count), "backup_state": backupState, "at": at.UTC(), "id": id, "expected": int64(expected)})
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrConflict
	}
	return nil
}

package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// OAuthStateStore implements oauthstate.StateRepository over a PostgreSQL
// database. Consume is a DELETE … RETURNING (the jobs queue.go precedent): the row
// is deleted REGARDLESS of expiry and the expiry decision is computed in Go from
// the returned row, so the delete and the decision are one atomic step. payload is
// a BYTEA blob read back verbatim (byte-exact), never JSONB.
type OAuthStateStore struct {
	db *pgxdb.DB
}

var _ oauthstate.StateRepository = (*OAuthStateStore)(nil)

// NewOAuthStateStore returns an OAuthStateStore backed by db.
func NewOAuthStateStore(db *pgxdb.DB) *OAuthStateStore {
	return &OAuthStateStore{db: db}
}

const oauthStateColumns = "token, provider, purpose, payload, expires_at"

// oauthStateRow is the store-local, db-tagged projection of an oauth_states row.
// payload is BYTEA read back byte-exact.
type oauthStateRow struct {
	Token     string    `db:"token"`
	Provider  string    `db:"provider"`
	Purpose   string    `db:"purpose"`
	Payload   []byte    `db:"payload"`
	ExpiresAt time.Time `db:"expires_at"`
}

func (r oauthStateRow) toDomain() oauthstate.State {
	return oauthstate.State{
		Token:     r.Token,
		Provider:  r.Provider,
		Purpose:   r.Purpose,
		Payload:   r.Payload,
		ExpiresAt: r.ExpiresAt.UTC(),
	}
}

// Create persists a new flow state.
func (s *OAuthStateStore) Create(ctx context.Context, st oauthstate.State) (oauthstate.State, error) {
	const q = `INSERT INTO oauth_states (` + oauthStateColumns + `)
		VALUES (@token, @provider, @purpose, @payload, @expires_at)`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"token":      st.Token,
		"provider":   st.Provider,
		"purpose":    st.Purpose,
		"payload":    st.Payload,
		"expires_at": st.ExpiresAt.UTC(),
	})
	if err != nil {
		return oauthstate.State{}, err
	}
	return st, nil
}

// Consume atomically deletes and returns the state for token. The row is deleted
// regardless of expiry, so: unknown or already-consumed → errs.ErrNotFound;
// expired → errs.ErrExpired (row already gone); live → the State. The delete is
// the single atomic step (DELETE … RETURNING); the expiry decision is computed in
// Go from the returned row.
func (s *OAuthStateStore) Consume(ctx context.Context, token string) (oauthstate.State, error) {
	const q = `DELETE FROM oauth_states WHERE token = @token RETURNING ` + oauthStateColumns
	row, err := queryOne[oauthStateRow](ctx, s.db, q, pgx.NamedArgs{"token": token})
	if err != nil {
		return oauthstate.State{}, err
	}
	st := row.toDomain()
	if st.Expired(time.Now()) {
		return oauthstate.State{}, errs.ErrExpired
	}
	return st, nil
}

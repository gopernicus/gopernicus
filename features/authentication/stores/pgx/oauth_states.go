package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/logic/oauthstate"
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

// Create persists a new flow state.
func (s *OAuthStateStore) Create(ctx context.Context, st oauthstate.State) (oauthstate.State, error) {
	const q = `INSERT INTO oauth_states (` + oauthStateColumns + `) VALUES ($1, $2, $3, $4, $5)`
	_, err := s.db.Exec(ctx, q, st.Token, st.Provider, st.Purpose, st.Payload, st.ExpiresAt.UTC())
	if err != nil {
		return oauthstate.State{}, err
	}
	return st, nil
}

// Consume atomically deletes and returns the state for token. The row is deleted
// regardless of expiry, so: unknown or already-consumed → errs.ErrNotFound;
// expired → errs.ErrExpired (row already gone); live → the State.
func (s *OAuthStateStore) Consume(ctx context.Context, token string) (oauthstate.State, error) {
	const q = `DELETE FROM oauth_states WHERE token = $1 RETURNING ` + oauthStateColumns
	st, err := scanOAuthState(s.db.QueryRow(ctx, q, token))
	if err != nil {
		return oauthstate.State{}, err
	}
	if st.Expired(time.Now()) {
		return oauthstate.State{}, errs.ErrExpired
	}
	return st, nil
}

// scanOAuthState scans one oauth_states row, mapping pgx.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanOAuthState(sc scanner) (oauthstate.State, error) {
	var (
		st        oauthstate.State
		payload   []byte
		expiresAt time.Time
	)
	if err := sc.Scan(&st.Token, &st.Provider, &st.Purpose, &payload, &expiresAt); err != nil {
		return oauthstate.State{}, pgxdb.MapError(err)
	}
	st.Payload = payload
	st.ExpiresAt = expiresAt.UTC()
	return st, nil
}

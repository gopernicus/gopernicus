package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/logic/oauthstate"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// OAuthStateStore implements oauthstate.StateRepository over a libSQL database.
// Consume is a DELETE … RETURNING (the jobs queue.go precedent): the row is
// deleted REGARDLESS of expiry and the expiry decision is computed in Go from the
// returned row, so the delete and the decision are one atomic step.
type OAuthStateStore struct {
	db *tursodb.DB
}

var _ oauthstate.StateRepository = (*OAuthStateStore)(nil)

// NewOAuthStateStore returns an OAuthStateStore backed by db.
func NewOAuthStateStore(db *tursodb.DB) *OAuthStateStore {
	return &OAuthStateStore{db: db}
}

const oauthStateColumns = "token, provider, purpose, payload, expires_at"

// Create persists a new flow state.
func (s *OAuthStateStore) Create(ctx context.Context, st oauthstate.State) (oauthstate.State, error) {
	const q = `INSERT INTO oauth_states (` + oauthStateColumns + `) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, st.Token, st.Provider, st.Purpose, string(st.Payload), tursodb.FormatTime(st.ExpiresAt))
	if err != nil {
		return oauthstate.State{}, err
	}
	return st, nil
}

// Consume atomically deletes and returns the state for token. The row is deleted
// regardless of expiry, so: unknown or already-consumed → errs.ErrNotFound;
// expired → errs.ErrExpired (row already gone); live → the State.
func (s *OAuthStateStore) Consume(ctx context.Context, token string) (oauthstate.State, error) {
	const q = `DELETE FROM oauth_states WHERE token = ? RETURNING ` + oauthStateColumns
	st, err := scanOAuthState(s.db.QueryRow(ctx, q, token))
	if err != nil {
		return oauthstate.State{}, err
	}
	if st.Expired(time.Now()) {
		return oauthstate.State{}, errs.ErrExpired
	}
	return st, nil
}

// scanOAuthState scans one oauth_states row, mapping sql.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanOAuthState(sc scanner) (oauthstate.State, error) {
	var (
		st        oauthstate.State
		payload   string
		expiresAt string
	)
	if err := sc.Scan(&st.Token, &st.Provider, &st.Purpose, &payload, &expiresAt); err != nil {
		return oauthstate.State{}, tursodb.MapError(err)
	}
	st.Payload = []byte(payload)
	var err error
	if st.ExpiresAt, err = tursodb.ParseTime(expiresAt); err != nil {
		return oauthstate.State{}, err
	}
	return st, nil
}

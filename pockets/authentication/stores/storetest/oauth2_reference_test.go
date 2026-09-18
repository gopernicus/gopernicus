package storetest

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type oauthRefreshRecord struct {
	SessionID string
	ExpiresAt time.Time
}
type refOAuth2 struct{ *reference }

var _ oauth2.Repository = refOAuth2{}
var _ session.ManagementRepository = refOAuth2{}

func (r refOAuth2) PutClient(ctx context.Context, c oauth2.Client) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.ID == "" || c.ExpiresAt.IsZero() {
		return sdk.ErrInvalidInput
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c.RedirectURIs = slices.Clone(c.RedirectURIs)
	r.oauthClients[c.ID] = c
	return nil
}

func (r refOAuth2) GetClient(ctx context.Context, id string) (oauth2.Client, error) {
	if err := ctx.Err(); err != nil {
		return oauth2.Client{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.oauthClients[id]
	if !ok {
		return oauth2.Client{}, sdk.ErrNotFound
	}
	c.RedirectURIs = slices.Clone(c.RedirectURIs)
	return c, nil
}

func (r refOAuth2) CreateCode(ctx context.Context, c oauth2.Code, approvingSessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[c.UserID]
	w, exists := r.sessions[approvingSessionID]
	if !ok || !u.Active() || u.AuthRevision != c.AuthRevision || !exists || w.UserID != c.UserID || !w.FirstParty() || w.Expired(time.Now()) || c.Hash == "" || !c.ExpiresAt.After(time.Now()) || c.Delegation.AuthRevision != c.AuthRevision {
		return oauth2.ErrInvalidGrant
	}
	if _, exists := r.oauthCodes[c.Hash]; exists {
		return sdk.ErrAlreadyExists
	}
	r.oauthCodes[c.Hash] = c
	return nil
}

func (r refOAuth2) GetCode(ctx context.Context, hash string) (oauth2.Code, error) {
	if err := ctx.Err(); err != nil {
		return oauth2.Code{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.oauthCodes[hash]
	if !ok || hash == "" {
		return oauth2.Code{}, oauth2.ErrInvalidGrant
	}
	return c, nil
}

func (r refOAuth2) RedeemCode(ctx context.Context, expected oauth2.Code, proposed session.Session, now time.Time) (session.Session, error) {
	if err := ctx.Err(); err != nil {
		return session.Session{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.oauthCodes[expected.Hash]
	u, userOK := r.users[c.UserID]
	if !ok || !userOK || !u.Active() || u.AuthRevision != c.AuthRevision || !sameCode(c, expected) || !now.Before(c.ExpiresAt) || proposed.ID == "" || proposed.UserID != c.UserID || proposed.Profile != session.ProfileDelegated || proposed.Delegation != c.Delegation || proposed.Delegation.AuthRevision != c.AuthRevision || proposed.RefreshTokenHash == "" || proposed.Expired(now) || proposed.PreviousRefreshTokenHash != "" || proposed.RotationCount != 0 {
		return session.Session{}, oauth2.ErrInvalidGrant
	}
	if _, exists := r.sessions[proposed.ID]; exists {
		return session.Session{}, sdk.ErrAlreadyExists
	}
	if r.oauthHashUsed(proposed.RefreshTokenHash) {
		return session.Session{}, sdk.ErrAlreadyExists
	}
	delete(r.oauthCodes, c.Hash)
	r.sessions[proposed.ID] = cloneOAuthSession(proposed)
	return cloneOAuthSession(proposed), nil
}

func sameCode(a, b oauth2.Code) bool {
	return a.Hash == b.Hash && a.UserID == b.UserID && a.AuthRevision == b.AuthRevision && a.Delegation == b.Delegation && a.RedirectURI == b.RedirectURI && a.CodeChallenge == b.CodeChallenge && a.CreatedAt.Equal(b.CreatedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
}

// Callers hold the shared datastore lock, including while selecting a spent hash.
func (r refOAuth2) oauthSession(hash string) (session.Session, bool, bool) {
	if hash == "" {
		return session.Session{}, false, false
	}
	for _, s := range r.sessions {
		if s.RefreshTokenHash == hash {
			return s, false, true
		}
	}
	if record, ok := r.oauthRefresh[hash]; ok {
		s, live := r.sessions[record.SessionID]
		return s, true, live
	}
	return session.Session{}, false, false
}

func (r refOAuth2) oauthHashUsed(hash string) bool {
	if _, ok := r.oauthRefresh[hash]; ok {
		return true
	}
	for _, s := range r.sessions {
		if s.RefreshTokenHash == hash {
			return true
		}
	}
	return false
}

func (r refOAuth2) RotateRefresh(ctx context.Context, in oauth2.Refresh) (session.Session, error) {
	if err := ctx.Err(); err != nil {
		return session.Session{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, spent, ok := r.oauthSession(in.Hash)
	u, userOK := r.users[s.UserID]
	if !ok || !userOK || !u.Active() || s.Profile != session.ProfileDelegated || s.Expired(in.Now) || s.Delegation.ClientID != in.ClientID || s.Delegation.Resource != in.Resource {
		return session.Session{}, oauth2.ErrInvalidGrant
	}
	if spent {
		r.deleteOAuthSession(s.ID)
		return session.Session{}, oauth2.ErrRefreshReuse
	}
	if in.NewHash == "" || in.Hash == in.NewHash {
		return session.Session{}, oauth2.ErrInvalidGrant
	}
	if r.oauthHashUsed(in.NewHash) {
		return session.Session{}, sdk.ErrAlreadyExists
	}
	r.oauthRefresh[in.Hash] = oauthRefreshRecord{s.ID, s.ExpiresAt}
	s.RefreshTokenHash = in.NewHash
	s.PreviousRefreshTokenHash = ""
	s.PreviousUsed = false
	s.RotationCount++
	r.sessions[s.ID] = cloneOAuthSession(s)
	return cloneOAuthSession(s), nil
}

func (r refOAuth2) deleteOAuthSession(id string) {
	delete(r.sessions, id)
	for key, grant := range r.grants {
		if grant.SessionID == id {
			delete(r.grants, key)
		}
	}
}

func (r refOAuth2) RevokeRefresh(ctx context.Context, hash, clientID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, _, ok := r.oauthSession(hash)
	if ok && s.Profile == session.ProfileDelegated && s.Delegation.ClientID == clientID {
		r.deleteOAuthSession(s.ID)
	}
	return nil
}

func (r refOAuth2) RevokeGrant(ctx context.Context, id, userID, clientID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if ok && s.Profile == session.ProfileDelegated && s.UserID == userID && s.Delegation.ClientID == clientID {
		r.deleteOAuthSession(id)
	}
	return nil
}

func (r refOAuth2) Prune(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, c := range r.oauthCodes {
		if !now.Before(c.ExpiresAt) {
			delete(r.oauthCodes, id)
		}
	}
	for id, c := range r.oauthClients {
		if !now.Before(c.ExpiresAt) {
			delete(r.oauthClients, id)
		}
	}
	for hash, record := range r.oauthRefresh {
		if !now.Before(record.ExpiresAt) {
			delete(r.oauthRefresh, hash)
		}
	}
	return nil
}

func (r refOAuth2) ListByUser(ctx context.Context, userID string, req list.Request) (list.Page[session.Session], error) {
	if err := ctx.Err(); err != nil {
		return list.Page[session.Session]{}, err
	}
	if strings.TrimSpace(req.Search) != "" {
		return list.Page[session.Session]{}, sdk.ErrInvalidInput
	}
	r.mu.RLock()
	items := []session.Session{}
	for _, s := range r.sessions {
		if s.UserID == userID && !s.Expired(time.Now()) {
			items = append(items, cloneOAuthSession(s))
		}
	}
	r.mu.RUnlock()
	return pageMem(items, req, func(s session.Session) (time.Time, string) { return s.CreatedAt, s.ID })
}

func (r refOAuth2) DeleteForUser(ctx context.Context, id, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok || s.UserID != userID {
		return sdk.ErrNotFound
	}
	r.deleteOAuthSession(id)
	return nil
}

func (r refOAuth2) RevokeAllForUser(ctx context.Context, userID string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[userID]
	if !ok {
		return sdk.ErrNotFound
	}
	u.AuthRevision++
	u.UpdatedAt = now
	r.users[userID] = u
	r.authRevisions[userID] = u.AuthRevision
	for id, s := range r.sessions {
		if s.UserID == userID {
			r.deleteOAuthSession(id)
		}
	}
	for id, grant := range r.grants {
		if grant.UserID == userID {
			delete(r.grants, id)
		}
	}
	return nil
}

func cloneOAuthSession(s session.Session) session.Session {
	s.Authentication.Methods = slices.Clone(s.Authentication.Methods)
	return s
}

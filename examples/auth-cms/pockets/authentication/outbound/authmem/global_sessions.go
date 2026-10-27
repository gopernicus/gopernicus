package authmem

import (
	"context"
	"fmt"
	"sort"
	"time"

	challenge "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ session.GlobalRevoker = globalSessionsRepo{}

// --- session.GlobalRevoker ---

// globalSessionsRepo ends every session in the store. Each batch runs under the
// one mutex, the per-user order a SQL store's transaction takes: the revision
// bump, then the sessions, grants and password_reset challenges.
type globalSessionsRepo struct{ *data }

func (r globalSessionsRepo) RevokeAllBatch(ctx context.Context, afterUserID string, limit int, now time.Time) (session.GlobalRevocationBatch, error) {
	if err := ctx.Err(); err != nil {
		return session.GlobalRevocationBatch{}, err
	}
	if limit <= 0 {
		return session.GlobalRevocationBatch{}, fmt.Errorf("authmem: limit must be positive: %w", sdk.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var page []string
	for id, u := range r.users {
		if id > afterUserID && r.projectLocked(u).Active() {
			page = append(page, id)
		}
	}
	sort.Strings(page)
	if len(page) > limit {
		page = page[:limit]
	}
	batch := session.GlobalRevocationBatch{Users: len(page), Done: len(page) < limit, LastUserID: afterUserID}
	in := make(map[string]bool, len(page))
	for _, id := range page {
		r.advanceCredentialRevisionLocked(id, now)
		in[id] = true
		batch.LastUserID = id
	}
	for id, g := range r.authGrants {
		if in[g.UserID] {
			delete(r.authGrants, id)
			batch.Grants++
		}
	}
	for id, s := range r.sessions {
		if in[s.UserID] {
			r.deleteSession(id)
			batch.Sessions++
		}
	}
	for id, c := range r.challenges {
		if in[c.UserID] && c.Purpose == challenge.PurposePasswordReset {
			delete(r.challenges, id)
		}
	}
	return batch, nil
}

func (r globalSessionsRepo) SweepOrphans(ctx context.Context, _ time.Time) (int, int, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	inactive := func(userID string) bool {
		u, ok := r.users[userID]
		return !ok || !r.projectLocked(u).Active()
	}
	sessions, grants := 0, 0
	for id, g := range r.authGrants {
		if inactive(g.UserID) {
			delete(r.authGrants, id)
			grants++
		}
	}
	for id, s := range r.sessions {
		if inactive(s.UserID) {
			r.deleteSession(id)
			sessions++
		}
	}
	return sessions, grants, nil
}

package authmem

import (
	"context"
	"time"

	apikey "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	serviceaccount "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var (
	_ serviceaccount.StatusRepository = accountStatusRepo{}
	_ apikey.FilteredLister           = apiKeyRepo{}
)

// --- serviceaccount.StatusRepository ---

// accountStatusRepo is the service-account status table; an absent row is
// active. A disable writes the row and revokes the account's keys under the
// one mutex, so no half-applied disable is observable.
type accountStatusRepo struct{ *data }

func (r accountStatusRepo) GetStatus(_ context.Context, id string) (serviceaccount.Status, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.accountStatusLocked(id), nil
}

func (d *data) accountStatusLocked(id string) serviceaccount.Status {
	if s, ok := d.accountStatus[id]; ok {
		return s
	}
	return serviceaccount.StatusActive
}

func (r accountStatusRepo) Statuses(_ context.Context, ids []string) (map[string]serviceaccount.Status, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]serviceaccount.Status, len(ids))
	for _, id := range ids {
		out[id] = r.accountStatusLocked(id)
	}
	return out, nil
}

func (r accountStatusRepo) Disable(_ context.Context, id, _ string, now time.Time) (serviceaccount.StatusChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return serviceaccount.StatusChange{}, sdk.ErrNotFound
	}
	if r.accountStatusLocked(id) == serviceaccount.StatusDisabled {
		return serviceaccount.StatusChange{}, nil
	}
	r.accountStatus[id] = serviceaccount.StatusDisabled
	change := serviceaccount.StatusChange{Changed: true}
	for kid, k := range r.apiKeys {
		if k.ServiceAccountID == id && !k.Revoked() {
			k.RevokedAt = now.UTC()
			r.apiKeys[kid] = k
			change.KeysRevoked++
		}
	}
	return change, nil
}

func (r accountStatusRepo) Enable(_ context.Context, id, _ string, _ time.Time) (serviceaccount.StatusChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return serviceaccount.StatusChange{}, sdk.ErrNotFound
	}
	if r.accountStatusLocked(id) == serviceaccount.StatusActive {
		return serviceaccount.StatusChange{}, nil
	}
	r.accountStatus[id] = serviceaccount.StatusActive
	return serviceaccount.StatusChange{Changed: true}, nil
}

func (r accountStatusRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.accountStatus, id)
	return nil
}

// ProbeSchema always finds the table: memory has no migration to miss.
func (r accountStatusRepo) ProbeSchema(context.Context) error { return nil }

// --- apikey.FilteredLister ---

// ListByServiceAccountFiltered narrows the listing by filter before paging,
// through the same Filter.Matches predicate the SQL stores are held to.
func (r apiKeyRepo) ListByServiceAccountFiltered(_ context.Context, serviceAccountID string, filter apikey.Filter, req list.Request) (list.Page[apikey.APIKey], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]apikey.APIKey, 0)
	for _, k := range r.apiKeys {
		if k.ServiceAccountID == serviceAccountID && list.MatchesSearch(k.Name, req.Search) && filter.Matches(k) {
			all = append(all, k)
		}
	}
	return page(all, req, func(k apikey.APIKey) (time.Time, string) { return k.CreatedAt, k.ID })
}

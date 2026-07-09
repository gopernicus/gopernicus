package authmem

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// orderField is the keyset order column every paginated auth port pages by; it
// must match the cursor's order field so a stale cursor from a different sort is
// ignored (the store precedent — features/authentication/stores/turso uses "created_at").
const orderField = "created_at"

// Compile-time proof that each thin view fills its exact port.
var (
	_ oauthaccount.OAuthAccountRepository     = oauthAccountRepo{}
	_ oauthstate.StateRepository              = oauthStateRepo{}
	_ serviceaccount.ServiceAccountRepository = serviceAccountRepo{}
	_ apikey.APIKeyRepository                 = apiKeyRepo{}
	_ securityevent.SecurityEventRepository   = securityEventRepo{}
	_ invitation.InvitationRepository         = invitationRepo{}
)

// --- oauthaccount.OAuthAccountRepository ---

type oauthAccountRepo struct{ *data }

func (r oauthAccountRepo) Create(_ context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.oauthAccounts {
		if ex.Provider == a.Provider && ex.ProviderUserID == a.ProviderUserID {
			return oauthaccount.OAuthAccount{}, errs.ErrAlreadyExists
		}
	}
	r.oauthAccounts = append(r.oauthAccounts, a)
	return a, nil
}

func (r oauthAccountRepo) GetByProvider(_ context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.oauthAccounts {
		if a.Provider == provider && a.ProviderUserID == providerUserID {
			return a, nil
		}
	}
	return oauthaccount.OAuthAccount{}, errs.ErrNotFound
}

func (r oauthAccountRepo) ListByUser(_ context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]oauthaccount.OAuthAccount, 0)
	for _, a := range r.oauthAccounts {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r oauthAccountRepo) Delete(_ context.Context, userID, provider string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, a := range r.oauthAccounts {
		if a.UserID == userID && a.Provider == provider {
			r.oauthAccounts = append(r.oauthAccounts[:i], r.oauthAccounts[i+1:]...)
			return nil
		}
	}
	return errs.ErrNotFound
}

// --- oauthstate.StateRepository ---

type oauthStateRepo struct{ *data }

func (r oauthStateRepo) Create(_ context.Context, s oauthstate.State) (oauthstate.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.oauthStates[s.Token] = s
	return s, nil
}

// Consume is a single-use get-and-delete: the row is deleted REGARDLESS of
// expiry (the DELETE … RETURNING contract), so an expired token deletes and
// returns errs.ErrExpired and any second Consume → errs.ErrNotFound.
func (r oauthStateRepo) Consume(_ context.Context, token string) (oauthstate.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.oauthStates[token]
	if !ok {
		return oauthstate.State{}, errs.ErrNotFound
	}
	delete(r.oauthStates, token)
	if s.Expired(time.Now()) {
		return oauthstate.State{}, errs.ErrExpired
	}
	return s, nil
}

// --- serviceaccount.ServiceAccountRepository ---

type serviceAccountRepo struct{ *data }

func (r serviceAccountRepo) Create(_ context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.serviceAccounts[sa.ID] = sa
	return sa, nil
}

func (r serviceAccountRepo) Get(_ context.Context, id string) (serviceaccount.ServiceAccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sa, ok := r.serviceAccounts[id]
	if !ok {
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
	}
	return sa, nil
}

func (r serviceAccountRepo) List(_ context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]serviceaccount.ServiceAccount, 0, len(r.serviceAccounts))
	for _, sa := range r.serviceAccounts {
		all = append(all, sa)
	}
	return page(all, req, func(sa serviceaccount.ServiceAccount) (time.Time, string) { return sa.CreatedAt, sa.ID })
}

func (r serviceAccountRepo) Update(_ context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
	}
	r.serviceAccounts[id] = sa
	return sa, nil
}

func (r serviceAccountRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return errs.ErrNotFound
	}
	delete(r.serviceAccounts, id)
	return nil
}

// --- apikey.APIKeyRepository ---

type apiKeyRepo struct{ *data }

func (r apiKeyRepo) Create(_ context.Context, k apikey.APIKey) (apikey.APIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.apiKeys {
		if ex.KeyHash == k.KeyHash {
			return apikey.APIKey{}, errs.ErrAlreadyExists
		}
	}
	r.apiKeys[k.ID] = k
	return k, nil
}

// GetByHash returns the record for ANY present row — revoked and expired rows
// included; unknown hash → errs.ErrNotFound (the pinned contract: revocation and
// expiry are service-layer branches, never a store filter).
func (r apiKeyRepo) GetByHash(_ context.Context, keyHash string) (apikey.APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, k := range r.apiKeys {
		if k.KeyHash == keyHash {
			return k, nil
		}
	}
	return apikey.APIKey{}, errs.ErrNotFound
}

func (r apiKeyRepo) ListByServiceAccount(_ context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]apikey.APIKey, 0)
	for _, k := range r.apiKeys {
		if k.ServiceAccountID == serviceAccountID {
			all = append(all, k)
		}
	}
	return page(all, req, func(k apikey.APIKey) (time.Time, string) { return k.CreatedAt, k.ID })
}

func (r apiKeyRepo) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.apiKeys[id]
	if !ok {
		return errs.ErrNotFound
	}
	k.RevokedAt = revokedAt
	r.apiKeys[id] = k
	return nil
}

func (r apiKeyRepo) TouchLastUsed(_ context.Context, id string, usedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.apiKeys[id]
	if !ok {
		return errs.ErrNotFound
	}
	k.LastUsedAt = usedAt
	r.apiKeys[id] = k
	return nil
}

// --- securityevent.SecurityEventRepository ---

type securityEventRepo struct{ *data }

// Create appends an audit row. Details is normalized to a non-nil map so the
// read-back contract (a nil/empty map reads back non-nil empty) holds uniformly.
func (r securityEventRepo) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	evt.Details = normalizeDetails(evt.Details)
	r.securityEvents = append(r.securityEvents, evt)
	return evt, nil
}

func (r securityEventRepo) List(_ context.Context, filter securityevent.ListFilter, req crud.ListRequest) (crud.Page[securityevent.SecurityEvent], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]securityevent.SecurityEvent, 0)
	for _, evt := range r.securityEvents {
		if filter.Match(evt) {
			all = append(all, evt)
		}
	}
	return page(all, req, func(evt securityevent.SecurityEvent) (time.Time, string) { return evt.CreatedAt, evt.ID })
}

// normalizeDetails returns a non-nil copy of d: a nil or empty map yields a
// non-nil empty map (the uniform read-back contract the storetest asserts).
func normalizeDetails(d map[string]any) map[string]any {
	out := make(map[string]any, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

// --- invitation.InvitationRepository ---

type invitationRepo struct{ *data }

// Create enforces PARTIAL pending-tuple uniqueness: at most one PENDING
// invitation per (resource_type, resource_id, identifier, relation). Once a row
// moves off pending, a new pending invite for the same tuple succeeds.
func (r invitationRepo) Create(_ context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.invitations {
		if ex.Status == invitation.StatusPending &&
			ex.ResourceType == inv.ResourceType && ex.ResourceID == inv.ResourceID &&
			ex.Identifier == inv.Identifier && ex.Relation == inv.Relation {
			return invitation.Invitation{}, errs.ErrAlreadyExists
		}
	}
	r.invitations[inv.ID] = inv
	return inv, nil
}

func (r invitationRepo) Get(_ context.Context, id string) (invitation.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	inv, ok := r.invitations[id]
	if !ok {
		return invitation.Invitation{}, errs.ErrNotFound
	}
	return inv, nil
}

// GetByTokenHash returns the invitation for tokenHash; a present row past its
// ExpiresAt surfaces the read-time errs.ErrExpired, unknown → errs.ErrNotFound.
func (r invitationRepo) GetByTokenHash(_ context.Context, tokenHash string) (invitation.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, inv := range r.invitations {
		if inv.TokenHash == tokenHash {
			if inv.Expired(time.Now()) {
				return invitation.Invitation{}, errs.ErrExpired
			}
			return inv, nil
		}
	}
	return invitation.Invitation{}, errs.ErrNotFound
}

func (r invitationRepo) ListByResource(_ context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]invitation.Invitation, 0)
	for _, inv := range r.invitations {
		if inv.ResourceType == resourceType && inv.ResourceID == resourceID {
			all = append(all, inv)
		}
	}
	return page(all, req, func(inv invitation.Invitation) (time.Time, string) { return inv.CreatedAt, inv.ID })
}

func (r invitationRepo) ListBySubject(_ context.Context, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]invitation.Invitation, 0)
	for _, inv := range r.invitations {
		if inv.Identifier == identifier {
			all = append(all, inv)
		}
	}
	return page(all, req, func(inv invitation.Invitation) (time.Time, string) { return inv.CreatedAt, inv.ID })
}

// UpdateStatus applies the lifecycle transition's mutable subset, leaving the
// immutable fields (id, resource, identifier, invited-by, created-at) intact.
func (r invitationRepo) UpdateStatus(_ context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.invitations[id]
	if !ok {
		return invitation.Invitation{}, errs.ErrNotFound
	}
	inv.Status = upd.Status
	inv.TokenHash = upd.TokenHash
	inv.ExpiresAt = upd.ExpiresAt
	inv.AcceptedAt = upd.AcceptedAt
	inv.ResolvedSubjectID = upd.ResolvedSubjectID
	inv.UpdatedAt = upd.UpdatedAt
	r.invitations[id] = inv
	return inv, nil
}

// --- shared pagination ---

// page sorts items by (created_at, id) in the resolved direction, then applies
// the sdk/crud list matrix — cursor or offset mode, the reverse-probe prev page,
// and the optional count — the keyset shape a dialect store implements in SQL,
// hand-rolled here so this memstore paginates identically (the jobs memstore
// precedent). created_at is the only sortable field.
func page[T any](items []T, req crud.ListRequest, key func(T) (time.Time, string)) (crud.Page[T], error) {
	if err := req.Validate(); err != nil {
		return crud.Page[T]{}, err
	}
	if req.Order.Field != "" && req.Order.Field != orderField {
		return crud.Page[T]{}, fmt.Errorf("unknown order field %q: %w", req.Order.Field, errs.ErrInvalidInput)
	}
	asc := req.Order.Direction == crud.ASC

	sort.Slice(items, func(i, j int) bool {
		ti, ii := key(items[i])
		tj, ij := key(items[j])
		if !ti.Equal(tj) {
			if asc {
				return ti.Before(tj)
			}
			return ti.After(tj)
		}
		if asc {
			return ii < ij
		}
		return ii > ij
	})

	total := int64(len(items))
	limit := req.NormalizedLimit(crud.Limits{})
	encode := func(it T) (string, error) {
		t, id := key(it)
		return crud.EncodeCursor(orderField, t, id)
	}

	if req.ResolvedStrategy() == crud.StrategyOffset {
		window := items
		if req.Offset < len(window) {
			window = window[req.Offset:]
		} else {
			window = window[:0]
		}
		if len(window) > limit+1 {
			window = window[:limit+1]
		}
		pg, err := crud.TrimPage(window, limit, encode)
		if err != nil {
			return crud.Page[T]{}, err
		}
		pg.NextCursor = ""
		pg.HasPrev = req.Offset > 0
		if req.WithCount {
			pg.Total = &total
		}
		return pg, nil
	}

	cur, err := crud.DecodeCursor(req.Cursor, orderField)
	if err != nil {
		return crud.Page[T]{}, err
	}

	forward := items
	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		forward = forward[:0:0]
		for _, it := range items {
			t, id := key(it)
			if afterCursor(t, id, cv, cur.PK, asc) {
				forward = append(forward, it)
			}
		}
	}
	window := forward
	if len(window) > limit+1 {
		window = window[:limit+1]
	}
	pg, err := crud.TrimPage(window, limit, encode)
	if err != nil {
		return crud.Page[T]{}, err
	}

	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		var before []T
		for _, it := range items {
			t, id := key(it)
			if beforeCursor(t, id, cv, cur.PK, asc) {
				before = append(before, it)
			}
		}
		// The previous page is the `limit` rows immediately before the cursor.
		if len(before) > limit {
			before = before[len(before)-limit:]
		}
		if err := crud.MarkPrevPage(&pg, before, limit, encode); err != nil {
			return crud.Page[T]{}, err
		}
	}

	if req.WithCount {
		pg.Total = &total
	}
	return pg, nil
}

// afterCursor reports whether (t, id) sorts strictly after the cursor under the
// resolved direction — the next-page predicate.
func afterCursor(t time.Time, id string, cv time.Time, cpk string, asc bool) bool {
	if !t.Equal(cv) {
		if asc {
			return t.After(cv)
		}
		return t.Before(cv)
	}
	if asc {
		return id > cpk
	}
	return id < cpk
}

// beforeCursor reports whether (t, id) sorts strictly before the cursor under the
// resolved direction — the reverse-probe predicate.
func beforeCursor(t time.Time, id string, cv time.Time, cpk string, asc bool) bool {
	if !t.Equal(cv) {
		if asc {
			return t.Before(cv)
		}
		return t.After(cv)
	}
	if asc {
		return id < cpk
	}
	return id > cpk
}

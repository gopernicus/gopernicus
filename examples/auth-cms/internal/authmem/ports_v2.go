package authmem

import (
	"context"
	"fmt"
	"sort"
	"time"

	apikey "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	oauthaccount "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	oauthstate "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	securityevent "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	serviceaccount "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	invitation "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// orderField is the keyset order column every paginated auth port pages by; it
// must match the cursor's order field so a stale cursor from a different sort is
// ignored (the store precedent — pockets/authentication/stores/turso uses "created_at").
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
	u, ok := r.users[a.UserID]
	if !ok {
		return oauthaccount.OAuthAccount{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return oauthaccount.OAuthAccount{}, session.ErrUserNotActive
	}
	for _, ex := range r.oauthAccounts {
		if ex.Provider == a.Provider && ex.ProviderUserID == a.ProviderUserID {
			return oauthaccount.OAuthAccount{}, sdk.ErrAlreadyExists
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
	return oauthaccount.OAuthAccount{}, sdk.ErrNotFound
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
	u, ok := r.users[userID]
	if !ok {
		return sdk.ErrNotFound
	}
	if !u.Active() {
		return session.ErrUserNotActive
	}

	for i, a := range r.oauthAccounts {
		if a.UserID == userID && a.Provider == provider {
			r.oauthAccounts = append(r.oauthAccounts[:i], r.oauthAccounts[i+1:]...)
			r.advanceCredentialRevisionLocked(userID, time.Now())
			r.revokeCredentialStateLocked(userID)
			return nil
		}
	}
	return sdk.ErrNotFound
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
// returns sdk.ErrExpired and any second Consume → sdk.ErrNotFound.
func (r oauthStateRepo) Consume(_ context.Context, token string) (oauthstate.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.oauthStates[token]
	if !ok {
		return oauthstate.State{}, sdk.ErrNotFound
	}
	delete(r.oauthStates, token)
	if s.Expired(time.Now()) {
		return oauthstate.State{}, sdk.ErrExpired
	}
	return s, nil
}

// --- serviceaccount.ServiceAccountRepository ---

type serviceAccountRepo struct{ *data }

func (r serviceAccountRepo) Create(_ context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if sa.ID == "" {
		sa.ID = ids.MustGenerate()
	}
	r.serviceAccounts[sa.ID] = sa
	return sa, nil
}

func (r serviceAccountRepo) Get(_ context.Context, id string) (serviceaccount.ServiceAccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sa, ok := r.serviceAccounts[id]
	if !ok {
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
	}
	return sa, nil
}

func (r serviceAccountRepo) List(_ context.Context, req list.Request) (list.Page[serviceaccount.ServiceAccount], error) {
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
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
	}
	r.serviceAccounts[id] = sa
	return sa, nil
}

func (r serviceAccountRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return sdk.ErrNotFound
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
			return apikey.APIKey{}, sdk.ErrAlreadyExists
		}
	}
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if k.ID == "" {
		k.ID = ids.MustGenerate()
	}
	r.apiKeys[k.ID] = k
	return k, nil
}

// GetByHash returns the record for ANY present row — revoked and expired rows
// included; unknown hash → sdk.ErrNotFound (the pinned contract: revocation and
// expiry are service-layer branches, never a store filter).
func (r apiKeyRepo) GetByHash(_ context.Context, keyHash string) (apikey.APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, k := range r.apiKeys {
		if k.KeyHash == keyHash {
			return k, nil
		}
	}
	return apikey.APIKey{}, sdk.ErrNotFound
}

func (r apiKeyRepo) ListByServiceAccount(_ context.Context, serviceAccountID string, req list.Request) (list.Page[apikey.APIKey], error) {
	// The search term is applied through list.MatchesSearch — the SHARED oracle the
	// SQL dialects are pinned against (crud-search-upstream T4) — so this host
	// store cannot disagree with Postgres or libSQL about what a term matches.
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]apikey.APIKey, 0)
	for _, k := range r.apiKeys {
		if k.ServiceAccountID != serviceAccountID {
			continue
		}
		if !list.MatchesSearch(k.Name, req.Search) {
			continue
		}
		all = append(all, k)
	}
	return page(all, req, func(k apikey.APIKey) (time.Time, string) { return k.CreatedAt, k.ID })
}

func (r apiKeyRepo) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.apiKeys[id]
	if !ok {
		return sdk.ErrNotFound
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
		return sdk.ErrNotFound
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
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if evt.ID == "" {
		evt.ID = ids.MustGenerate()
	}
	r.securityEvents = append(r.securityEvents, evt)
	return evt, nil
}

func (r securityEventRepo) List(_ context.Context, filter securityevent.ListFilter, req list.Request) (list.Page[securityevent.SecurityEvent], error) {
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
// invitation per (resource_type, resource_id, identifier_kind, identifier,
// relation) — kind-aware (migration 0013), so the same value coexists across
// kinds. Once a row moves off pending, a new pending invite for the same tuple
// succeeds.
func (r invitationRepo) Create(ctx context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return invitation.Invitation{}, err
	}
	if inv.Status != invitation.StatusPending || inv.ResolvedSubjectType != "" {
		return invitation.Invitation{}, sdk.ErrInvalidInput
	}
	for _, ex := range r.invitations {
		if (inv.ID != "" && ex.ID == inv.ID) || ex.TokenHash == inv.TokenHash {
			return invitation.Invitation{}, sdk.ErrAlreadyExists
		}
		if ex.Active() &&
			ex.ResourceType == inv.ResourceType && ex.ResourceID == inv.ResourceID &&
			ex.IdentifierKind == inv.IdentifierKind &&
			ex.Identifier == inv.Identifier && ex.Relation == inv.Relation {
			return invitation.Invitation{}, sdk.ErrAlreadyExists
		}
	}
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if inv.ID == "" {
		inv.ID = ids.MustGenerate()
	}
	// Mirror the SQL stores' nil/empty → '{}' round-trip and defensive copy: a
	// stored row always carries a non-nil metadata map the caller cannot mutate.
	inv.Metadata = invitation.CloneMetadata(inv.Metadata)
	r.invitations[inv.ID] = inv.Clone()
	return inv.Clone(), nil
}

func (r invitationRepo) Get(_ context.Context, id string) (invitation.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	inv, ok := r.invitations[id]
	if !ok {
		return invitation.Invitation{}, sdk.ErrNotFound
	}
	return inv.Clone(), nil
}

// GetByTokenHash returns the invitation for tokenHash; a present row past its
// ExpiresAt surfaces the read-time sdk.ErrExpired, unknown → sdk.ErrNotFound.
func (r invitationRepo) GetByTokenHash(_ context.Context, tokenHash string) (invitation.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, inv := range r.invitations {
		if inv.TokenHash == tokenHash {
			if inv.Status != invitation.StatusAccepting && inv.Status != invitation.StatusAccepted && inv.Expired(time.Now()) {
				return invitation.Invitation{}, sdk.ErrExpired
			}
			return inv.Clone(), nil
		}
	}
	return invitation.Invitation{}, sdk.ErrNotFound
}

func (r invitationRepo) ListByResource(_ context.Context, resourceType, resourceID string, req list.Request) (list.Page[invitation.Invitation], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]invitation.Invitation, 0)
	for _, inv := range r.invitations {
		if inv.ResourceType == resourceType && inv.ResourceID == resourceID {
			all = append(all, inv.Clone())
		}
	}
	return page(all, req, func(inv invitation.Invitation) (time.Time, string) { return inv.CreatedAt, inv.ID })
}

func (r invitationRepo) ListBySubject(_ context.Context, kind, identifier string, req list.Request) (list.Page[invitation.Invitation], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]invitation.Invitation, 0)
	for _, inv := range r.invitations {
		if inv.IdentifierKind == kind && inv.Identifier == identifier {
			all = append(all, inv.Clone())
		}
	}
	return page(all, req, func(inv invitation.Invitation) (time.Time, string) { return inv.CreatedAt, inv.ID })
}

// UpdateStatus applies the lifecycle transition's mutable subset, leaving the
// immutable fields (id, resource, identifier, invited-by, created-at) intact.
func (r invitationRepo) UpdateStatus(ctx context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return invitation.Invitation{}, err
	}
	inv, ok := r.invitations[id]
	if !ok {
		return invitation.Invitation{}, sdk.ErrNotFound
	}
	updated, err := inv.UpdateStatus(upd)
	if err != nil {
		return invitation.Invitation{}, err
	}
	for existingID, existing := range r.invitations {
		if existingID == id {
			continue
		}
		if existing.TokenHash == updated.TokenHash {
			return invitation.Invitation{}, sdk.ErrAlreadyExists
		}
		if existing.Active() && updated.Active() && existing.ResourceType == updated.ResourceType && existing.ResourceID == updated.ResourceID && existing.IdentifierKind == updated.IdentifierKind && existing.Identifier == updated.Identifier && existing.Relation == updated.Relation {
			return invitation.Invitation{}, sdk.ErrAlreadyExists
		}
	}
	r.invitations[id] = updated.Clone()
	return updated.Clone(), nil
}

func (r invitationRepo) ClaimAcceptance(ctx context.Context, id string, claim invitation.Acceptance) (invitation.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return invitation.Invitation{}, err
	}
	inv, ok := r.invitations[id]
	if !ok {
		return invitation.Invitation{}, sdk.ErrNotFound
	}
	updated, err := inv.ClaimAcceptance(claim)
	if err != nil {
		return invitation.Invitation{}, err
	}
	r.invitations[id] = updated.Clone()
	return updated.Clone(), nil
}

func (r invitationRepo) CompleteAcceptance(ctx context.Context, id string, claim invitation.Acceptance) (invitation.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return invitation.Invitation{}, err
	}
	inv, ok := r.invitations[id]
	if !ok {
		return invitation.Invitation{}, sdk.ErrNotFound
	}
	updated, err := inv.CompleteAcceptance(claim)
	if err != nil {
		return invitation.Invitation{}, err
	}
	r.invitations[id] = updated.Clone()
	return updated.Clone(), nil
}

// --- shared pagination ---

// page sorts items by (created_at, id) in the resolved direction, then applies
// the sdk/pkg/list list matrix — cursor or offset mode, the reverse-probe prev page,
// and the optional count — the keyset shape a dialect store implements in SQL,
// hand-rolled here so this memstore paginates identically (the jobs memstore
// precedent). created_at is the only sortable field.
func page[T any](items []T, req list.Request, key func(T) (time.Time, string)) (list.Page[T], error) {
	if err := req.Validate(); err != nil {
		return list.Page[T]{}, err
	}
	if req.Order.Field != "" && req.Order.Field != orderField {
		return list.Page[T]{}, fmt.Errorf("unknown order field %q: %w", req.Order.Field, sdk.ErrInvalidInput)
	}
	asc := req.Order.Direction == list.ASC

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
	limit := req.NormalizedLimit(list.Limits{})
	encode := func(it T) (string, error) {
		t, id := key(it)
		return list.EncodeCursor(orderField, t, id)
	}

	if req.ResolvedStrategy() == list.StrategyOffset {
		window := items
		if req.Offset < len(window) {
			window = window[req.Offset:]
		} else {
			window = window[:0]
		}
		if len(window) > limit+1 {
			window = window[:limit+1]
		}
		pg, err := list.TrimPage(window, limit, encode)
		if err != nil {
			return list.Page[T]{}, err
		}
		pg.NextCursor = ""
		pg.HasPrev = req.Offset > 0
		if req.WithCount {
			pg.Total = &total
		}
		return pg, nil
	}

	cur, err := list.DecodeCursor(req.Cursor, orderField)
	if err != nil {
		return list.Page[T]{}, err
	}

	var cv time.Time
	forward := items
	if cur != nil {
		var ok bool
		cv, ok = cur.OrderValue.(time.Time)
		if !ok {
			return list.Page[T]{}, fmt.Errorf("cursor order value must be a timestamp: %w", sdk.ErrInvalidInput)
		}
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
	pg, err := list.TrimPage(window, limit, encode)
	if err != nil {
		return list.Page[T]{}, err
	}

	if cur != nil {
		var before []T
		for _, it := range items {
			t, id := key(it)
			if !afterCursor(t, id, cv, cur.PK, asc) {
				before = append(before, it)
			}
		}
		// Include the boundary and one extra predecessor for the previous cursor.
		if len(before) > limit+1 {
			before = before[len(before)-limit-1:]
		}
		if err := list.MarkPrevPage(&pg, before, limit, encode); err != nil {
			return list.Page[T]{}, err
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

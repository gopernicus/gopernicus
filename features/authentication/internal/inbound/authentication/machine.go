package authentication

import (
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

type createServiceAccountRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ActAsUser   bool   `json:"act_as_user"`
	// ActAsUserID names the human the account acts as. Empty with ActAsUser → the
	// caller. Non-empty → delegation, allowed only because the route sits behind
	// MachineRoutesGate; the service validates and audits it. Requires ActAsUser.
	ActAsUserID string `json:"act_as_user_id"`
	// OwnerUserID is REFUSED (400 by name) — kept one release so a client sending
	// the v0.5.x field learns the rename instead of a generic decode error. Dropped
	// at the next minor, after which strict decode answers 400 for it.
	OwnerUserID string `json:"owner_user_id"`
}

type mintKeyRequest struct {
	Name string `json:"name"`
	// ExpiresAt is an optional RFC3339 timestamp; empty → the key never expires.
	ExpiresAt string `json:"expires_at"`
}

type serviceAccountResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedBy   string `json:"created_by"`
	ActAsUser   bool   `json:"act_as_user"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func newServiceAccountResponse(sa serviceaccount.ServiceAccount) serviceAccountResponse {
	return serviceAccountResponse{
		ID:          sa.ID,
		Name:        sa.Name,
		Description: sa.Description,
		CreatedBy:   sa.CreatedBy,
		ActAsUser:   sa.ActAsUser,
		OwnerUserID: sa.OwnerUserID,
		CreatedAt:   sa.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   sa.UpdatedAt.Format(time.RFC3339),
	}
}

// apiKeyResponse is a key WITHOUT its secret — the listing shape. The plaintext
// key is only ever in mintedKeyResponse, returned once at mint.
type apiKeyResponse struct {
	ID               string `json:"id"`
	ServiceAccountID string `json:"service_account_id"`
	Name             string `json:"name"`
	KeyPrefix        string `json:"key_prefix"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	RevokedAt        string `json:"revoked_at,omitempty"`
	LastUsedAt       string `json:"last_used_at,omitempty"`
	CreatedAt        string `json:"created_at"`
}

func newAPIKeyResponse(k apikey.APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID:               k.ID,
		ServiceAccountID: k.ServiceAccountID,
		Name:             k.Name,
		KeyPrefix:        k.KeyPrefix,
		ExpiresAt:        formatOptionalTime(k.ExpiresAt),
		RevokedAt:        formatOptionalTime(k.RevokedAt),
		LastUsedAt:       formatOptionalTime(k.LastUsedAt),
		CreatedAt:        k.CreatedAt.Format(time.RFC3339),
	}
}

// mintedKeyResponse carries the plaintext key exactly once, at mint.
type mintedKeyResponse struct {
	apiKeyResponse
	Key string `json:"key"`
}

// pageResponse is the JSON envelope for a paginated list. Items are the mapped
// response DTOs; the remaining fields mirror crud.Page (both cursors, HasMore,
// HasPrev, and the optional Total count).
type pageResponse[T any] struct {
	Items          []T    `json:"items"`
	NextCursor     string `json:"next_cursor,omitempty"`
	HasMore        bool   `json:"has_more,omitempty"`
	HasPrev        bool   `json:"has_prev,omitempty"`
	PreviousCursor string `json:"previous_cursor,omitempty"`
	Total          *int64 `json:"total,omitempty"`
}

func newPageResponse[E any, T any](p crud.Page[E], mapFn func(E) T) pageResponse[T] {
	items := make([]T, 0, len(p.Items))
	for _, e := range p.Items {
		items = append(items, mapFn(e))
	}
	return pageResponse[T]{
		Items:          items,
		NextCursor:     p.NextCursor,
		HasMore:        p.HasMore,
		HasPrev:        p.HasPrev,
		PreviousCursor: p.PreviousCursor,
		Total:          p.Total,
	}
}

// mountMachine registers the machine-identity lifecycle routes (design §4.1).
// Called from Mount only when both machine repositories are wired AND the host
// named a gate.
//
// Every route carries the same identity stack, outermost first (web.Handle
// applies the list left-to-right as wrappers): requireUser admits the human
// credential class ONLY — an API key, act-as-user or not, is 401 here, so a key
// can never mint another key; requireLiveSession revokes within one round-trip
// instead of RequireUser's ≤AccessTokenTTL stale window (the invitation
// precedent — credential issuance is not less sensitive); then the host's gate
// decides authorization and writes its own denial (the bundled handlers write no
// 403 of their own).
//
// The three MUTATIONS carry browserSafe between the live-session check and the
// gate, exactly like mountUserAdmin: a cookie-authenticated mint or revoke is a
// browser-driven state change, so it must clear the allowlisted-Origin +
// double-submit CSRF gate before the host's policy is consulted — a forged
// cross-site request is refused as CSRF, never merely as unauthorized. Bearer-only
// callers skip it (requireBrowserSafeMutation short-circuits). The two GETs are
// body-less reads and stay off the mutation gate.
func mountMachine(r feature.RouteRegistrar, h *handlers, requireUser, requireLiveSession, browserSafe, gate web.Middleware) {
	r.Handle("POST", "/auth/service-accounts", h.createServiceAccount, requireUser, requireLiveSession, browserSafe, gate)
	r.Handle("GET", "/auth/service-accounts", h.listServiceAccounts, requireUser, requireLiveSession, gate)
	r.Handle("POST", "/auth/service-accounts/{id}/keys", h.mintAPIKey, requireUser, requireLiveSession, browserSafe, gate)
	r.Handle("GET", "/auth/service-accounts/{id}/keys", h.listAPIKeys, requireUser, requireLiveSession, gate)
	r.Handle("POST", "/auth/api-keys/{id}/revoke", h.revokeAPIKey, requireUser, requireLiveSession, browserSafe, gate)
}

// createServiceAccount creates a machine identity created by the calling human.
// The caller is the creator and, for an act-as-user account, the default owner;
// naming act_as_user_id delegates ownership to another EXISTING user, which the
// service validates and audits. owner_user_id is refused by name.
func (h *handlers) createServiceAccount(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req createServiceAccountRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	if req.OwnerUserID != "" {
		web.RespondJSONError(w, web.ErrBadRequest("owner_user_id is no longer accepted; the caller is the owner, or name act_as_user_id"))
		return
	}
	if !req.ActAsUser && req.ActAsUserID != "" {
		web.RespondJSONError(w, web.ErrBadRequest("act_as_user_id requires act_as_user"))
		return
	}
	createdBy, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	ownerUserID := ""
	if req.ActAsUser {
		ownerUserID = req.ActAsUserID
		if ownerUserID == "" {
			ownerUserID = createdBy
		}
	}
	sa, err := h.svc.CreateServiceAccount(r.Context(), createdBy, req.Name, req.Description, req.ActAsUser, ownerUserID)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONCreated(w, newServiceAccountResponse(sa))
}

// listServiceAccounts returns a cursor-paginated page of service accounts.
func (h *handlers) listServiceAccounts(w http.ResponseWriter, r *http.Request) {
	req, ok := h.parseListRequest(w, r, serviceaccount.OrderFields, serviceaccount.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.svc.ListServiceAccounts(r.Context(), req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newServiceAccountResponse))
}

// mintAPIKey mints a key for the service account and returns the plaintext ONCE.
// The response carries the only copy of a live credential, so it is no-store like
// every other secret-bearing handler.
func (h *handlers) mintAPIKey(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req mintKeyRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	expiresAt, ok := parseOptionalTime(w, req.ExpiresAt)
	if !ok {
		return
	}
	k, raw, err := h.svc.MintAPIKey(r.Context(), web.Param(r, "id"), req.Name, expiresAt)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONCreated(w, mintedKeyResponse{apiKeyResponse: newAPIKeyResponse(k), Key: raw})
}

// listAPIKeys returns a cursor-paginated page of a service account's keys.
func (h *handlers) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	req, ok := h.parseListRequest(w, r, apikey.OrderFields, apikey.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.svc.ListAPIKeys(r.Context(), web.Param(r, "id"), req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newAPIKeyResponse))
}

// revokeAPIKey revokes a key; an unknown key → 404.
func (h *handlers) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.RevokeAPIKey(r.Context(), web.Param(r, "id")); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "revoked"})
}

// parseListRequest parses the strict transport-edge page params
// (limit/cursor/offset/count/q plus a per-aggregate order) into a
// crud.ListRequest.
// orderFields is the aggregate's allow-list and defaultOrder its default sort;
// the order field is validated against the allow-list. The host-configured
// DefaultStrategy (h.listStrategy) applies when a request names neither a cursor
// nor an offset param. On any bad param it writes a 400 (the existing
// web.ErrBadRequest pattern) and returns ok=false.
func (h *handlers) parseListRequest(w http.ResponseWriter, r *http.Request, orderFields map[string]crud.OrderField, defaultOrder crud.Order) (crud.ListRequest, bool) {
	q := r.URL.Query()
	req, err := crud.ParseListRequest(crud.ListParams{
		Limit:  q.Get("limit"),
		Cursor: q.Get("cursor"),
		Offset: q.Get("offset"),
		Count:  q.Get("count"),
		// `q` is the canonical v3 search key (crud-search-upstream D3). A legacy
		// edge migrating v1 clients may fall back to `s` at ITS OWN transport, with
		// a documented removal milestone; this feature accepts `q` only.
		Search:          q.Get("q"),
		DefaultStrategy: h.listStrategy,
	})
	if err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("invalid page parameters"))
		return crud.ListRequest{}, false
	}
	order, err := crud.ParseOrder(orderFields, q.Get("order"), defaultOrder)
	if err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("invalid order parameter"))
		return crud.ListRequest{}, false
	}
	req.Order = order
	return req, true
}

// parseOptionalTime parses an optional RFC3339 timestamp. An empty value yields
// the zero time (never-expires); a malformed value writes a 400 and returns
// ok=false.
func parseOptionalTime(w http.ResponseWriter, value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("invalid expires_at (want RFC3339)"))
		return time.Time{}, false
	}
	return t, true
}

// formatOptionalTime renders a possibly-zero time as RFC3339, or "" when zero.
func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

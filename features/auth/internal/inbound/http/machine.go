package http

import (
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/apikey"
	"github.com/gopernicus/gopernicus/features/auth/logic/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

type createServiceAccountRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ActAsUser   bool   `json:"act_as_user"`
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
// response DTOs; NextCursor/HasMore mirror crud.Page.
type pageResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more,omitempty"`
}

func newPageResponse[E any, T any](p crud.Page[E], mapFn func(E) T) pageResponse[T] {
	items := make([]T, 0, len(p.Items))
	for _, e := range p.Items {
		items = append(items, mapFn(e))
	}
	return pageResponse[T]{Items: items, NextCursor: p.NextCursor, HasMore: p.HasMore}
}

// mountMachine registers the machine-identity lifecycle routes (design §4.1),
// all session-gated. Called from Mount only when both machine repositories are
// wired.
func mountMachine(r feature.RouteRegistrar, h *handlers, requireUser web.Middleware) {
	r.Handle("POST", "/auth/service-accounts", h.createServiceAccount, requireUser)
	r.Handle("GET", "/auth/service-accounts", h.listServiceAccounts, requireUser)
	r.Handle("POST", "/auth/service-accounts/{id}/keys", h.mintAPIKey, requireUser)
	r.Handle("GET", "/auth/service-accounts/{id}/keys", h.listAPIKeys, requireUser)
	r.Handle("POST", "/auth/api-keys/{id}/revoke", h.revokeAPIKey, requireUser)
}

// createServiceAccount creates a machine identity owned by the calling user.
func (h *handlers) createServiceAccount(w http.ResponseWriter, r *http.Request) {
	var req createServiceAccountRequest
	if !decode(w, r, &req) {
		return
	}
	createdBy, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		writeError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	sa, err := h.svc.CreateServiceAccount(r.Context(), createdBy, req.Name, req.Description, req.ActAsUser, req.OwnerUserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, newServiceAccountResponse(sa))
}

// listServiceAccounts returns a cursor-paginated page of service accounts.
func (h *handlers) listServiceAccounts(w http.ResponseWriter, r *http.Request) {
	req, ok := parseListRequest(w, r)
	if !ok {
		return
	}
	page, err := h.svc.ListServiceAccounts(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPageResponse(page, newServiceAccountResponse))
}

// mintAPIKey mints a key for the service account and returns the plaintext ONCE.
func (h *handlers) mintAPIKey(w http.ResponseWriter, r *http.Request) {
	var req mintKeyRequest
	if !decode(w, r, &req) {
		return
	}
	expiresAt, ok := parseOptionalTime(w, req.ExpiresAt)
	if !ok {
		return
	}
	k, raw, err := h.svc.MintAPIKey(r.Context(), web.Param(r, "id"), req.Name, expiresAt)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, mintedKeyResponse{apiKeyResponse: newAPIKeyResponse(k), Key: raw})
}

// listAPIKeys returns a cursor-paginated page of a service account's keys.
func (h *handlers) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	req, ok := parseListRequest(w, r)
	if !ok {
		return
	}
	page, err := h.svc.ListAPIKeys(r.Context(), web.Param(r, "id"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPageResponse(page, newAPIKeyResponse))
}

// revokeAPIKey revokes a key; an unknown key → 404.
func (h *handlers) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.RevokeAPIKey(r.Context(), web.Param(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// parseListRequest parses the strict transport-edge page params (limit/cursor
// query). On a bad param it writes a 400 and returns ok=false.
func parseListRequest(w http.ResponseWriter, r *http.Request) (crud.ListRequest, bool) {
	req, err := crud.ParseListRequest(r.URL.Query().Get("limit"), r.URL.Query().Get("cursor"), crud.MaxLimit)
	if err != nil {
		writeError(w, web.ErrBadRequest("invalid page parameters"))
		return crud.ListRequest{}, false
	}
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
		writeError(w, web.ErrBadRequest("invalid expires_at (want RFC3339)"))
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

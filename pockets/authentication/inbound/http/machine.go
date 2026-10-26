package authenticationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The API-key listing's filter keys and the machine routes' named codes.
const (
	queryKeyStatus       = "status"
	queryKeyNeverExpires = "never_expires"

	codeFilterInvalid          = "filter_invalid"
	codeFilterContradiction    = "filter_contradiction"
	codeFilterUnsupported      = "filter_unsupported"
	codeAPIKeyTTLExceeded      = "api_key_ttl_exceeded"
	codeAPIKeyExpiryPast       = "api_key_expiry_past"
	codeServiceAccountDisabled = "service_account_disabled"
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
	// ExpiresAt is an optional RFC3339 timestamp. Empty → the key never
	// expires, or, under AdministrationConfig.APIKeyMaxTTL (production's
	// default), expires at now plus it — the 201 echoes the expiry. One at or
	// before now is 400 api_key_expiry_past; one beyond the bound is 400
	// api_key_ttl_exceeded, naming max_ttl_seconds and max_expires_at.
	ExpiresAt string `json:"expires_at"`
}

// serviceAccountResponse is a machine identity. Status is always present:
// active, or disabled under AdministrationConfig.ServiceAccountStatus.
type serviceAccountResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedBy   string `json:"created_by"`
	ActAsUser   bool   `json:"act_as_user"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func newServiceAccountResponse(sa serviceaccount.ServiceAccount, status serviceaccount.Status) serviceAccountResponse {
	if status == "" {
		status = serviceaccount.StatusActive
	}
	return serviceAccountResponse{
		ID:          sa.ID,
		Name:        sa.Name,
		Description: sa.Description,
		CreatedBy:   sa.CreatedBy,
		ActAsUser:   sa.ActAsUser,
		OwnerUserID: sa.OwnerUserID,
		Status:      string(status),
		CreatedAt:   sa.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   sa.UpdatedAt.Format(time.RFC3339),
	}
}

// serviceAccountStatusResponse is the body of a disable or enable: the
// resulting account, whether THIS call changed its status (false on a
// replay), and how many keys a disable revoked.
type serviceAccountStatusResponse struct {
	ServiceAccount serviceAccountResponse `json:"service_account"`
	Changed        bool                   `json:"changed"`
	KeysRevoked    int                    `json:"keys_revoked"`
}

// apiKeyResponse is a key WITHOUT its secret — the listing shape. The plaintext
// key is only ever in mintedKeyResponse, returned once at mint. Status is the
// key's derived status at the response's instant: revoked, expired or active.
type apiKeyResponse struct {
	ID               string `json:"id"`
	ServiceAccountID string `json:"service_account_id"`
	Name             string `json:"name"`
	KeyPrefix        string `json:"key_prefix"`
	Status           string `json:"status"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	RevokedAt        string `json:"revoked_at,omitempty"`
	LastUsedAt       string `json:"last_used_at,omitempty"`
	CreatedAt        string `json:"created_at"`
}

func newAPIKeyResponse(k apikey.APIKey) apiKeyResponse {
	return newAPIKeyResponseAt(k, time.Now())
}

// newAPIKeyResponseAt derives the status at now, the instant a filtered
// listing judged expiry at, so a row never contradicts its filter.
func newAPIKeyResponseAt(k apikey.APIKey, now time.Time) apiKeyResponse {
	return apiKeyResponse{
		ID:               k.ID,
		ServiceAccountID: k.ServiceAccountID,
		Name:             k.Name,
		KeyPrefix:        k.KeyPrefix,
		Status:           string(k.Status(now)),
		ExpiresAt:        formatOptionalTime(k.ExpiresAt),
		RevokedAt:        formatOptionalTime(k.RevokedAt),
		LastUsedAt:       formatOptionalTime(k.LastUsedAt),
		CreatedAt:        k.CreatedAt.Format(time.RFC3339),
	}
}

// apiKeyTTLExceededResponse is the 400 api_key_ttl_exceeded body: the error
// envelope plus the bound the request crossed.
type apiKeyTTLExceededResponse struct {
	Message       string `json:"message"`
	Code          string `json:"code"`
	MaxTTLSeconds int64  `json:"max_ttl_seconds"`
	MaxExpiresAt  string `json:"max_expires_at"`
}

// mintedKeyResponse carries the plaintext key exactly once, at mint.
type mintedKeyResponse struct {
	apiKeyResponse
	Key string `json:"key"`
}

// pageResponse is the JSON envelope for a paginated list. Items are the mapped
// response DTOs; the remaining fields mirror list.Page (both cursors, HasMore,
// HasPrev, and the optional Total count).
type pageResponse[T any] struct {
	Items          []T    `json:"items"`
	NextCursor     string `json:"next_cursor,omitempty"`
	HasMore        bool   `json:"has_more,omitempty"`
	HasPrev        bool   `json:"has_prev,omitempty"`
	PreviousCursor string `json:"previous_cursor,omitempty"`
	Total          *int64 `json:"total,omitempty"`
}

func newPageResponse[E any, T any](p list.Page[E], mapFn func(E) T) pageResponse[T] {
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
// applies the list left-to-right as wrappers): the MachineLifecycle
// authenticator admits the human credential class ONLY and at the
// immediate-revocation tier — an API key, act-as-user or not, is 401 here, so a
// key can never mint another key, and a revoked session is denied within one
// round-trip instead of lingering for its access token's remaining life (the
// invitation precedent — credential issuance is not less sensitive); then the
// host's gate decides authorization and writes its own denial (the bundled
// handlers write no 403 of their own).
//
// The three MUTATIONS carry browserSafe between the authenticator and the
// gate, exactly like mountUserAdmin: a cookie-authenticated mint or revoke is a
// browser-driven state change, so it must clear the allowlisted-Origin +
// double-submit CSRF gate before the host's policy is consulted — a forged
// cross-site request is refused as CSRF, never merely as unauthorized. Bearer-only
// callers skip it (requireBrowserSafeMutation short-circuits). The two GETs are
// body-less reads and stay off the mutation gate.
//
// With MFAConfig admins beside passkeys or TOTP, every route then requires AAL2 of the
// caller the gate admitted (requireMachineMFA), so an AAL1 session cannot mint
// a key that would then pass the admin gates.
func mountMachine(r pockets.RouteRegistrar, h *handlers, machineLifecycle, browserSafe, gate web.Middleware) {
	mfa := h.requireMachineMFA
	r.Handle("POST", "/auth/service-accounts", h.createServiceAccount, machineLifecycle, browserSafe, gate, mfa)
	r.Handle("GET", "/auth/service-accounts", h.listServiceAccounts, machineLifecycle, gate, mfa)
	r.Handle("POST", "/auth/service-accounts/{id}/keys", h.mintAPIKey, machineLifecycle, browserSafe, gate, mfa)
	r.Handle("GET", "/auth/service-accounts/{id}/keys", h.listAPIKeys, machineLifecycle, gate, mfa)
	r.Handle("POST", "/auth/api-keys/{id}/revoke", h.revokeAPIKey, machineLifecycle, browserSafe, gate, mfa)
	// The disable/enable pair mounts only when the host wired the status
	// (AdministrationConfig.ServiceAccountStatus), under the same stack.
	if h.accountStatus != nil {
		r.Handle("POST", "/auth/service-accounts/{id}/disable", h.disableServiceAccount, machineLifecycle, browserSafe, gate, mfa)
		r.Handle("POST", "/auth/service-accounts/{id}/enable", h.enableServiceAccount, machineLifecycle, browserSafe, gate, mfa)
	}
}

// requireMachineMFA is the machine gate of MFAConfig admins: a caller below AAL2
// gets the RFC 9470 401 and one mfa_required_denied row (gate machine). An API
// key a host's MachineLifecycle override admits gets the 403
// mfa_credential_not_eligible, as at the admin gate, unless
// MFAConfig.APIKeysExempt. Without the requirement it passes everything.
func (h *handlers) requireMachineMFA(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.machineMFA {
			cred, _ := h.svc.CurrentCredential(r.Context())
			if reason := h.svc.admitMFA(r.Context(), authlogic.MFAGateMachine, cred); reason != "" {
				writeMFARequired(w, reason)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
	web.RespondJSONCreated(w, newServiceAccountResponse(sa, serviceaccount.StatusActive))
}

// listServiceAccounts returns a cursor-paginated page of service accounts,
// each with its status, read for the page in one call.
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
	statuses := map[string]serviceaccount.Status{}
	if h.accountStatus != nil && len(page.Items) > 0 {
		ids := make([]string, 0, len(page.Items))
		for _, sa := range page.Items {
			ids = append(ids, sa.ID)
		}
		if statuses, err = h.accountStatus.ServiceAccountStatuses(r.Context(), ids); err != nil {
			web.RespondJSONDomainError(w, err)
			return
		}
	}
	web.RespondJSONOK(w, newPageResponse(page, func(sa serviceaccount.ServiceAccount) serviceAccountResponse {
		return newServiceAccountResponse(sa, statuses[sa.ID])
	}))
}

// disableServiceAccount pauses the account and revokes its keys; a replay is
// a 200 with changed=false.
func (h *handlers) disableServiceAccount(w http.ResponseWriter, r *http.Request) {
	h.setServiceAccountStatus(w, r, serviceaccount.StatusDisabled, h.accountStatus.DisableServiceAccount)
}

// enableServiceAccount restores minting; the revoked keys stay revoked.
func (h *handlers) enableServiceAccount(w http.ResponseWriter, r *http.Request) {
	h.setServiceAccountStatus(w, r, serviceaccount.StatusActive, h.accountStatus.EnableServiceAccount)
}

// setServiceAccountStatus is the shared body of disable and enable: an empty
// strict JSON body, the route-chosen transition, and the resulting account,
// whose status is the transition's — never re-read, so a committed change
// cannot answer 503.
func (h *handlers) setServiceAccountStatus(w http.ResponseWriter, r *http.Request, status serviceaccount.Status, change func(context.Context, authlogic.Principal, string) (serviceaccount.ServiceAccount, serviceaccount.StatusChange, error)) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var body struct{}
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	actor, ok := h.svc.CurrentPrincipal(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	sa, result, err := change(r.Context(), actor, web.Param(r, "id"))
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, serviceAccountStatusResponse{
		ServiceAccount: newServiceAccountResponse(sa, status),
		Changed:        result.Changed,
		KeysRevoked:    result.KeysRevoked,
	})
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
	expiresAt, ok := parseOptionalTime(w, "expires_at", req.ExpiresAt)
	if !ok {
		return
	}
	k, raw, err := h.svc.MintAPIKey(r.Context(), web.Param(r, "id"), req.Name, expiresAt)
	if err != nil {
		writeMintError(w, err)
		return
	}
	web.RespondJSONCreated(w, mintedKeyResponse{apiKeyResponse: newAPIKeyResponse(k), Key: raw})
}

// writeMintError answers a mint refusal with its named code; anything else
// takes the generic domain mapping.
func writeMintError(w http.ResponseWriter, err error) {
	var exceeded *authlogic.APIKeyTTLExceededError
	switch {
	case errors.As(err, &exceeded):
		web.RespondJSON(w, http.StatusBadRequest, apiKeyTTLExceededResponse{
			Message:       "expires_at exceeds the maximum API key lifetime",
			Code:          codeAPIKeyTTLExceeded,
			MaxTTLSeconds: int64(exceeded.MaxTTL / time.Second),
			MaxExpiresAt:  exceeded.MaxExpiresAt.UTC().Format(time.RFC3339),
		})
	case errors.Is(err, authlogic.ErrAPIKeyExpiryPast):
		web.RespondJSONError(w, web.ErrBadRequest("expires_at must be in the future").WithCode(codeAPIKeyExpiryPast))
	case errors.Is(err, authlogic.ErrServiceAccountDisabled):
		web.RespondJSONError(w, web.ErrStateConflict("the service account is disabled").WithCode(codeServiceAccountDisabled))
	default:
		web.RespondJSONDomainError(w, err)
	}
}

// listAPIKeys returns a cursor-paginated page of a service account's keys.
// ?status=active|expired|revoked and ?never_expires=true|false narrow it in the
// store: each may appear once, an unknown value is 400 filter_invalid,
// status=expired beside never_expires=true is 400 filter_contradiction, and a
// store that cannot filter is 400 filter_unsupported. Every other key is
// tolerated as before. A key that expires between pages moves status: cursors
// do not bind the clock.
func (h *handlers) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !singleValued(w, q, queryKeyStatus, queryKeyNeverExpires) {
		return
	}
	filter, filtered, ok := parseAPIKeyFilter(w, q)
	if !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, apikey.OrderFields, apikey.DefaultOrder)
	if !ok {
		return
	}
	var page list.Page[apikey.APIKey]
	var err error
	filter.Now = time.Now()
	switch {
	case !filtered:
		page, err = h.svc.ListAPIKeys(r.Context(), web.Param(r, "id"), req)
	case h.keyFilter == nil:
		err = authlogic.ErrAPIKeyFilterUnsupported
	default:
		page, err = h.keyFilter.ListAPIKeysFiltered(r.Context(), web.Param(r, "id"), filter, req)
	}
	if errors.Is(err, authlogic.ErrAPIKeyFilterUnsupported) {
		web.RespondJSONError(w, web.ErrBadRequest("this API-key store does not support filtered listings").WithCode(codeFilterUnsupported))
		return
	}
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, func(k apikey.APIKey) apiKeyResponse { return newAPIKeyResponseAt(k, filter.Now) }))
}

// parseAPIKeyFilter reads the listing's closed filter vocabulary; filtered is
// false when neither narrows the listing (never_expires=false is no filter).
func parseAPIKeyFilter(w http.ResponseWriter, q url.Values) (apikey.Filter, bool, bool) {
	var f apikey.Filter
	filtered := false
	if q.Has(queryKeyStatus) {
		filtered = true
		switch status := apikey.Status(q.Get(queryKeyStatus)); status {
		case apikey.StatusActive, apikey.StatusExpired, apikey.StatusRevoked:
			f.Status = status
		default:
			web.RespondJSONError(w, web.ErrBadRequest("status must be active, expired or revoked").WithCode(codeFilterInvalid))
			return apikey.Filter{}, false, false
		}
	}
	if q.Has(queryKeyNeverExpires) {
		switch q.Get(queryKeyNeverExpires) {
		case "true":
			f.NeverExpires, filtered = true, true
		case "false":
		default:
			web.RespondJSONError(w, web.ErrBadRequest("never_expires must be true or false").WithCode(codeFilterInvalid))
			return apikey.Filter{}, false, false
		}
	}
	if f.Status == apikey.StatusExpired && f.NeverExpires {
		web.RespondJSONError(w, web.ErrBadRequest("an expired key cannot be one that never expires").WithCode(codeFilterContradiction))
		return apikey.Filter{}, false, false
	}
	return f, filtered, true
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
// list.Request.
// orderFields is the aggregate's allow-list and defaultOrder its default sort;
// the order field is validated against the allow-list. The host-configured
// DefaultStrategy (h.listStrategy) applies when a request names neither a cursor
// nor an offset param. On any bad param it writes a 400 carrying the parser's
// own sentence (web.ErrValidation) and returns ok=false.
func (h *handlers) parseListRequest(w http.ResponseWriter, r *http.Request, orderFields map[string]list.OrderField, defaultOrder list.Order) (list.Request, bool) {
	q := r.URL.Query()
	// `q` is the canonical v3 search key (crud-search-upstream D3). A legacy edge
	// migrating v1 clients may fall back to `s` at ITS OWN transport, with a
	// documented removal milestone; this pocket accepts `q` only.
	req, err := list.ParseQuery(q, list.QueryOptions{DefaultStrategy: h.listStrategy})
	if err == nil {
		req.Order, err = list.ParseOrder(orderFields, q.Get(list.QueryKeyOrder), defaultOrder)
	}
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return list.Request{}, false
	}
	return req, true
}

// parseOptionalTime parses the optional RFC3339 timestamp of field. An empty
// value yields the zero time; a malformed value writes a 400 naming field and
// returns ok=false.
func parseOptionalTime(w http.ResponseWriter, field, value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("invalid "+field+" (want RFC3339)"))
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

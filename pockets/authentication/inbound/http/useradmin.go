package authenticationhttp

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The security-event listing's filter keys.
const (
	querySecurityEventUserID = "user_id"
	querySecurityEventType   = "event_type"
	querySecurityEventStatus = "event_status"
	querySecurityEventSince  = "since"
	querySecurityEventUntil  = "until"
)

// codeOffsetNotSupported names the refusal of an offset on the cursor-only
// security-event listing.
const codeOffsetNotSupported = "offset_not_supported"

// globalRevokeBudgetPrefix keys the revoke-all route's per-actor budget, one
// call a minute.
const globalRevokeBudgetPrefix = "global_revoke"

// globalRevokeTimeout bounds a revoke-all run. The run is detached from the
// request's cancellation — a client disconnect or a proxy idle timeout must
// not abort it mid-fleet while the budget blocks the retry — so this is what
// stops a hung store from holding it.
const globalRevokeTimeout = 10 * time.Minute

// securityEventQueryKeys are the keys the security-event listing reads. Each
// may appear once, so the host check and this handler can never read
// different values of one key.
var (
	// eventTypeShape bounds an event_type filter: the rail stores free TEXT
	// (a host-bridged type such as authorization_denied is valid there without
	// being in securityevent.Types()), so the filter admits any snake_case type.
	eventTypeShape = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	// securityEventStatuses is the closed status vocabulary.
	securityEventStatuses = []string{securityevent.StatusSuccess, securityevent.StatusFailure, securityevent.StatusBlocked, securityevent.StatusStarted}
)

var securityEventQueryKeys = []string{
	querySecurityEventUserID, querySecurityEventType, querySecurityEventStatus, querySecurityEventSince, querySecurityEventUntil,
	list.QueryKeyLimit, list.QueryKeyCursor, list.QueryKeyOffset, list.QueryKeyCount, list.QueryKeySearch, list.QueryKeyOrder,
}

// The user-administration surface. Registered by Mount ONLY when the
// host supplied WithUserAdminCheck AND the administration/fenced-mint
// repositories are wired — deny-by-absence, the Providers precedent. A host that
// wires nothing here has no admin routes at all, and every path below 404s.
//
// Every route is gated in the same order:
//
//  1. The UserAdministration authenticator — sensitive reads and mutations must observe revocation
//     within one round-trip, not within AccessTokenTTL. A deactivated
//     administrator loses this surface immediately, because the transition
//     deleted their sessions.
//  2. the browser-safe mutation gate (allowlisted Origin + double-submit CSRF) —
//     on the POST mutations only; the GET reads are bearer-safe and carry no
//     body, matching /auth/methods.
//  3. WithUserAdminCheck — the HOST's decision, run before any target
//     resolution or mutation, so an unauthorized caller cannot use timing or
//     error shape to probe which user ids exist.
//
// The handlers write Cache-Control: no-store: the directory carries unmasked
// contact data and must never be retained by a shared cache.

// userSummaryResponse is one directory row. It mirrors user.Summary exactly —
// no credential material, no auth revision. status_changed_at is omitted while
// the account has never transitioned, and primary_email is omitted when the
// subject holds no email identifier (absent means "none on file", never
// "hidden": this surface is already explicitly authorized, so nothing is masked).
type userSummaryResponse struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	Status          string `json:"status"`
	StatusChangedAt string `json:"status_changed_at,omitempty"`
	PrimaryEmail    string `json:"primary_email,omitempty"`
	EmailVerified   bool   `json:"email_verified"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// userStatusResponse is the body of a lifecycle mutation: the resulting summary
// plus whether THIS call performed the transition. changed=false means the
// account was already in the requested status — a safe, idempotent replay, not
// an error.
type userStatusResponse struct {
	User    userSummaryResponse `json:"user"`
	Changed bool                `json:"changed"`
}

// userErasureResponse is the body of an erase: the resulting summary, whether
// THIS call erased the user (false on a repeat), and what it removed.
type userErasureResponse struct {
	User    userSummaryResponse `json:"user"`
	Changed bool                `json:"changed"`
	Counts  eraseCountsResponse `json:"counts"`
}

// eraseCountsResponse mirrors user.EraseCounts.
type eraseCountsResponse struct {
	Sessions             int `json:"sessions"`
	Grants               int `json:"grants"`
	Passwords            int `json:"passwords"`
	Passkeys             int `json:"passkeys"`
	TOTP                 int `json:"totp"`
	RecoveryCodes        int `json:"recovery_codes"`
	OAuthAccounts        int `json:"oauth_accounts"`
	ExternalSubjects     int `json:"external_subjects"`
	ContactChanges       int `json:"contact_changes"`
	Codes                int `json:"codes"`
	Challenges           int `json:"challenges"`
	IdentifiersBlanked   int `json:"identifiers_blanked"`
	InvitationsDeleted   int `json:"invitations_deleted"`
	InvitationsBlanked   int `json:"invitations_blanked"`
	InvitationsCancelled int `json:"invitations_cancelled"`
	ServiceAccounts      int `json:"service_accounts"`
	APIKeys              int `json:"api_keys"`
}

// globalRevocationResponse is the body of POST /auth/admin/sessions/revoke-all:
// what the run revoked, and when it started (UTC RFC 3339 nanoseconds).
type globalRevocationResponse struct {
	Users     int    `json:"users"`
	Sessions  int    `json:"sessions"`
	Grants    int    `json:"grants"`
	Batches   int    `json:"batches"`
	RevokedAt string `json:"revoked_at"`
}

// globalRevocationFailure is a failed revoke-all: the error envelope plus the
// partial counts of the batches that committed before the failure.
type globalRevocationFailure struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	globalRevocationResponse
}

// securityEventResponse is one security-event row: everything the rail stores,
// with created_at in UTC RFC 3339 nanoseconds. Details carry identifiers and
// digests only, never a secret or an address (the rail's content contract).
type securityEventResponse struct {
	ID          string         `json:"id"`
	UserID      string         `json:"user_id,omitempty"`
	ActorType   string         `json:"actor_type,omitempty"`
	ActorID     string         `json:"actor_id,omitempty"`
	EventType   string         `json:"event_type"`
	EventStatus string         `json:"event_status"`
	IPAddress   string         `json:"ip_address,omitempty"`
	UserAgent   string         `json:"user_agent,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	TraceID     string         `json:"trace_id,omitempty"`
	Details     map[string]any `json:"details"`
	CreatedAt   string         `json:"created_at"`
}

func newSecurityEventResponse(e securityevent.SecurityEvent) securityEventResponse {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	return securityEventResponse{
		ID: e.ID, UserID: e.UserID, ActorType: e.Actor.Type, ActorID: e.Actor.ID,
		EventType: e.EventType, EventStatus: e.EventStatus, IPAddress: e.IPAddress, UserAgent: e.UserAgent,
		RequestID: e.RequestID, TraceID: e.TraceID, Details: details,
		CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func newUserSummaryResponse(s user.Summary) userSummaryResponse {
	out := userSummaryResponse{
		ID:            s.ID,
		DisplayName:   s.DisplayName,
		Status:        string(user.NormalizeStatus(s.Status)),
		PrimaryEmail:  s.PrimaryEmail,
		EmailVerified: s.EmailVerified,
		CreatedAt:     s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     s.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if !s.StatusChangedAt.IsZero() {
		out.StatusChangedAt = s.StatusChangedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// mountUserAdmin registers the optional administrative user surface. Called from
// mount only when the administration capability and inbound policy are present.
func mountUserAdmin(r pockets.RouteRegistrar, h *handlers, userAdministration, browserSafe web.Middleware) {
	r.Handle("GET", "/auth/admin/users", h.adminListUsers, userAdministration)
	r.Handle("GET", "/auth/admin/users/{id}", h.adminGetUser, userAdministration)
	r.Handle("POST", "/auth/admin/users/{id}/deactivate", h.adminDeactivateUser, userAdministration, browserSafe)
	r.Handle("POST", "/auth/admin/users/{id}/reactivate", h.adminReactivateUser, userAdministration, browserSafe)
	// The AUTHORIZED counterpart of the public, enumeration-safe resend.
	// It rides the same gate chain as the lifecycle mutations and may report real
	// target state.
	r.Handle("POST", "/auth/admin/users/{id}/verification/resend", h.adminResendVerification, userAdministration, browserSafe)
}

// adminPrincipal resolves the caller's effective principal and runs the host
// authorization check for action/target. It writes the response and returns
// ok=false on any refusal, so a handler's remaining body runs only for an
// authorized caller.
//
// A machine principal reaches the policy as a real principal: the pocket does
// not pre-decide whether a service account may administer users.
//
// With WithAdministrationMFA the caller the check admits must also hold an AAL2
// credential from a source MFAConfig.Methods admits, or it gets the RFC 9470 401 and one mfa_required_denied row
// (gate admin). The check runs first, so a non-admin keeps the host's 403 and
// the challenge never reveals who is an administrator. An API key — which can
// never satisfy the requirement — gets the 403 mfa_credential_not_eligible with
// no challenge, unless MFAConfig.APIKeysExempt passes it.
func (h *handlers) adminPrincipal(w http.ResponseWriter, r *http.Request, action UserAdminAction, targetUserID string) (authlogic.Principal, bool) {
	principal, ok := h.svc.CurrentPrincipal(r.Context())
	if !ok {
		// The UserAdministration authenticator already ran, so this is a wiring
		// failure rather than a missing credential. Fail closed either way.
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return authlogic.Principal{}, false
	}
	if err := h.checkUserAdmin(r.Context(), UserAdminCheckRequest{Principal: principal, Action: action, TargetUserID: targetUserID}); err != nil {
		// A denial and an infrastructure error both stop here; the shared mapper
		// turns sdk.ErrForbidden into 403 and anything else into 500, so a policy
		// outage never reads as permission.
		web.RespondJSONDomainError(w, err)
		return authlogic.Principal{}, false
	}
	if h.adminMFA {
		cred, _ := h.svc.CurrentCredential(r.Context())
		if reason := h.svc.admitMFA(r.Context(), authlogic.MFAGateAdmin, cred); reason != "" {
			writeMFARequired(w, reason)
			return authlogic.Principal{}, false
		}
	}
	return principal, true
}

// adminListUsers returns a page of the operator directory.
func (h *handlers) adminListUsers(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if _, ok := h.adminPrincipal(w, r, UserAdminList, ""); !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, user.OrderFields, user.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.svc.ListUsers(r.Context(), req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newUserSummaryResponse))
}

// adminGetUser returns one user's directory projection.
func (h *handlers) adminGetUser(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	id := web.Param(r, "id")
	if _, ok := h.adminPrincipal(w, r, UserAdminRead, id); !ok {
		return
	}
	summary, err := h.svc.GetUserSummary(r.Context(), id)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newUserSummaryResponse(summary))
}

// adminDeactivateUser denies the target every new session and revokes what it
// already holds. Replaying it on an already-deactivated user is a 200 with
// changed=false.
func (h *handlers) adminDeactivateUser(w http.ResponseWriter, r *http.Request) {
	h.adminSetStatus(w, r, UserAdminDeactivate, user.StatusDeactivated)
}

// adminReactivateUser returns the target to the active posture. It fabricates no
// session — the user must authenticate again.
func (h *handlers) adminReactivateUser(w http.ResponseWriter, r *http.Request) {
	h.adminSetStatus(w, r, UserAdminReactivate, user.StatusActive)
}

// adminSetStatus is the shared body of the two lifecycle mutations. The target
// status is chosen by the ROUTE, never by the request body: a body-driven status
// would let a client name a value the host's policy was not asked about.
//
// Self-transition is not generically forbidden — a host policy may allow or
// refuse an administrator acting on their own account, and a last-admin
// invariant lives in the host's policy, not here.
func (h *handlers) adminSetStatus(w http.ResponseWriter, r *http.Request, action UserAdminAction, status user.Status) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	// These mutations carry no fields; the strict decode still rejects an unknown
	// key or an oversized body, matching every other mutation route.
	var body struct{}
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	id := web.Param(r, "id")
	principal, ok := h.adminPrincipal(w, r, action, id)
	if !ok {
		return
	}
	summary, change, err := h.svc.SetUserStatus(r.Context(), principal, id, status)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, userStatusResponse{
		User:    newUserSummaryResponse(summary),
		Changed: change.Changed,
	})
}

// adminEraseUser erases the target subject. A repeat is a 200 with
// changed=false; an accepting invitation claim held for the target is 409 (the
// operator settles it first); naming one's own account is 400.
func (h *handlers) adminEraseUser(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var body struct{}
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	id := web.Param(r, "id")
	principal, ok := h.adminPrincipal(w, r, UserAdminErase, id)
	if !ok {
		return
	}
	summary, erasure, err := h.eraser.EraseUser(r.Context(), principal, id)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	c := erasure.Counts
	web.RespondJSONOK(w, userErasureResponse{
		User:    newUserSummaryResponse(summary),
		Changed: erasure.Changed,
		Counts: eraseCountsResponse{
			Sessions: c.Sessions, Grants: c.Grants, Passwords: c.Passwords, Passkeys: c.Passkeys, TOTP: c.TOTP, RecoveryCodes: c.RecoveryCodes, OAuthAccounts: c.OAuthAccounts,
			ExternalSubjects: c.ExternalSubjects, ContactChanges: c.ContactChanges, Codes: c.Codes, Challenges: c.Challenges,
			IdentifiersBlanked: c.IdentifiersBlanked, InvitationsDeleted: c.InvitationsDeleted, InvitationsBlanked: c.InvitationsBlanked,
			InvitationsCancelled: c.InvitationsCancelled, ServiceAccounts: c.ServiceAccounts, APIKeys: c.APIKeys,
		},
	})
}

// adminListSecurityEvents pages the security-event rail, newest first, for an
// administrator the host check admits for UserAdminReadAudit; the user_id filter
// reaches the check as the target. Every filter is optional, so an unfiltered
// call lists the whole rail. The listing is cursor only: paging until has_more
// is false is the export, and a changed filter needs a fresh cursor. The
// service records one audit_read row per page read.
func (h *handlers) adminListSecurityEvents(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	q := r.URL.Query()
	// Only the key the host check reads is validated before it; everything
	// else is the admitted caller's to get wrong.
	if !singleValued(w, q, querySecurityEventUserID) {
		return
	}
	userID := q.Get(querySecurityEventUserID)
	if _, ok := h.adminPrincipal(w, r, UserAdminReadAudit, userID); !ok {
		return
	}
	if !singleValued(w, q, securityEventQueryKeys...) {
		return
	}
	eventType, eventStatus := q.Get(querySecurityEventType), q.Get(querySecurityEventStatus)
	if eventType != "" && !eventTypeShape.MatchString(eventType) {
		web.RespondJSONError(w, web.ErrBadRequest("event_type must be a snake_case event type"))
		return
	}
	if eventStatus != "" && !slices.Contains(securityEventStatuses, eventStatus) {
		web.RespondJSONError(w, web.ErrBadRequest("event_status must be success, failure, blocked or started"))
		return
	}
	since, ok := parseOptionalTime(w, querySecurityEventSince, q.Get(querySecurityEventSince))
	if !ok {
		return
	}
	until, ok := parseOptionalTime(w, querySecurityEventUntil, q.Get(querySecurityEventUntil))
	if !ok {
		return
	}
	if !since.IsZero() && !until.IsZero() && !since.Before(until) {
		web.RespondJSONError(w, web.ErrBadRequest("since must be before until"))
		return
	}
	req, ok := parseSecurityEventListRequest(w, r)
	if !ok {
		return
	}
	filter := securityevent.ListFilter{UserID: userID, EventType: eventType, EventStatus: eventStatus, Since: since, Until: until}
	page, err := h.events.ListSecurityEvents(r.Context(), filter, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newSecurityEventResponse))
}

// adminRevokeAllSessions ends every session in the fleet for an administrator
// the host check admits for UserAdminRevokeAllSessions, at most once a minute
// per actor. The caller's own session goes with the rest, so the session
// cookies are cleared and the next bearer request is a 401. The run is detached
// from the request (globalRevokeTimeout bounds it), so a dropped connection
// does not stop it. A failed run answers its error with the partial counts —
// clearing the cookies once a batch committed, since the caller's session may
// be gone — and is safe to repeat.
func (h *handlers) adminRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var body struct{}
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	principal, ok := h.adminPrincipal(w, r, UserAdminRevokeAllSessions, "")
	if !ok {
		return
	}
	revoke := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), globalRevokeTimeout)
		defer cancel()
		res, err := h.globalRevoker.RevokeAllSessions(ctx, principal)
		counts := globalRevocationResponse{
			Users: res.Users, Sessions: res.Sessions, Grants: res.Grants, Batches: res.Batches,
			RevokedAt: res.RevokedAt.UTC().Format(time.RFC3339Nano),
		}
		if err != nil {
			if res.Batches == 0 {
				web.RespondJSONDomainError(w, err)
				return
			}
			h.svc.ClearSessionCookies(w)
			mapped := web.ErrFromDomain(err)
			web.RespondJSON(w, mapped.Status, globalRevocationFailure{Message: mapped.Message, Code: mapped.Code, globalRevocationResponse: counts})
			return
		}
		h.svc.ClearSessionCookies(w)
		web.RespondJSONOK(w, counts)
	})
	h.svc.rateLimitByKey(globalRevokeBudgetPrefix, principal.Type+":"+principal.ID, ratelimiter.PerMinute(1))(revoke).ServeHTTP(w, r)
}

// singleValued answers a named 400 when any of keys is repeated.
func singleValued(w http.ResponseWriter, q url.Values, keys ...string) bool {
	for _, key := range keys {
		if len(q[key]) > 1 {
			web.RespondJSONError(w, web.ErrBadRequest(key+" may appear at most once"))
			return false
		}
	}
	return true
}

// parseSecurityEventListRequest admits limit, cursor and count only: the rail
// orders by created_at DESC by contract and declares no search fields. The
// cursor strategy is fixed whatever the adapter's ListStrategy says.
func parseSecurityEventListRequest(w http.ResponseWriter, r *http.Request) (list.Request, bool) {
	q := r.URL.Query()
	switch {
	case q.Has(list.QueryKeyOffset):
		web.RespondJSONError(w, web.ErrBadRequest("the security-event listing pages by cursor only; the offset parameter is not supported").WithCode(codeOffsetNotSupported))
		return list.Request{}, false
	case q.Has(list.QueryKeySearch):
		web.RespondJSONError(w, web.ErrBadRequest("the security-event listing declares no search fields; the q parameter is not supported"))
		return list.Request{}, false
	case q.Has(list.QueryKeyOrder):
		web.RespondJSONError(w, web.ErrBadRequest("the security-event listing is ordered by created_at, newest first; the order parameter is not supported"))
		return list.Request{}, false
	}
	req, err := list.ParseQuery(q, list.QueryOptions{DefaultStrategy: list.StrategyCursor})
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return list.Request{}, false
	}
	req.Order = securityevent.DefaultOrder
	return req, true
}

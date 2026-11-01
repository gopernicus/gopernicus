package authenticationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// allowInviteCheck authorizes every invitation operation. Wired where a test
// exercises the routes past the host policy (the create/list allowed contract and
// the revoked-session middleware gate, which denies before the handler body).
func allowInviteCheck(context.Context, InviteCheckRequest) error { return nil }

// stubInvitationService satisfies InvitationService with inert methods — enough
// to prove the routes register when a Granter is wired.
type stubInvitationService struct{}

// Preparation is real domain behavior even when execution is a transport spy.
type unusedInvitationRepo struct {
	invitations.InvitationRepository
}
type unusedInvitationGranter struct{ invitations.Granter }

func (stubInvitationService) PrepareCreate(ctx context.Context, in invitations.CreateInput) (invitations.PreparedCreate, error) {
	svc, err := invitations.New(unusedInvitationRepo{}, unusedInvitationGranter{}, invitations.WithDelivery(invitations.DeliveryConfig{Mailer: nopMailer{}}))
	if err != nil {
		return invitations.PreparedCreate{}, err
	}
	return svc.PrepareCreate(ctx, in)
}

type managementInvitationRepo struct {
	invitations.InvitationRepository
	invitation invitations.Invitation
}

func (r managementInvitationRepo) Get(context.Context, string) (invitations.Invitation, error) {
	return r.invitation, nil
}
func preparedHTTPManagement(ctx context.Context, id, issuer string) (invitations.PreparedManagement, error) {
	svc, err := invitations.New(managementInvitationRepo{invitation: invitations.Invitation{ID: id, InvitedBy: issuer}}, unusedInvitationGranter{})
	if err != nil {
		return invitations.PreparedManagement{}, err
	}
	return svc.PrepareManagement(ctx, id)
}
func (stubInvitationService) PrepareManagement(ctx context.Context, id string) (invitations.PreparedManagement, error) {
	return preparedHTTPManagement(ctx, id, "u1")
}
func (stubInvitationService) CreatePrepared(context.Context, invitations.PreparedCreate) (invitations.CreateResult, error) {
	return invitations.CreateResult{}, nil
}
func (stubInvitationService) ListByResource(context.Context, string, string, list.Request) (list.Page[invitations.Invitation], error) {
	return list.Page[invitations.Invitation]{}, nil
}
func (stubInvitationService) Mine(context.Context, string, string, list.Request) (list.Page[invitations.Invitation], error) {
	return list.Page[invitations.Invitation]{}, nil
}
func (stubInvitationService) Accept(context.Context, invitations.AcceptInput) (invitations.AcceptResult, error) {
	return invitations.AcceptResult{}, nil
}
func (stubInvitationService) Decline(context.Context, string, string) error { return nil }
func (stubInvitationService) Cancel(context.Context, invitations.PreparedManagement) error {
	return nil
}
func (stubInvitationService) Resend(context.Context, invitations.PreparedManagement, string) (invitations.Invitation, error) {
	return invitations.Invitation{}, nil
}

// spyInvitationService records execution after the adapter's policy succeeds.
// Preparation uses the real domain normalization path; results remain configurable.
type spyInvitationService struct {
	// Embed the domain preparation fixture.
	stubInvitationService
	createCalled bool
	listCalled   bool
	mineCalled   bool
	acceptCalled bool
	cancelCalled bool
	resendCalled bool
	createResult invitations.CreateResult
	// lastCreate is the most recent CreateInput the handler passed, so a test can
	// assert request fields (e.g. Metadata) reach the service verbatim.
	lastCreate invitations.CreateInput
	// lastPrincipal is the most recent principal an authorized operation received,
	// so a test can assert the handler resolved and forwarded the caller.
	lastPrincipal sdk.Principal
	// listPage / resendResult are the configurable list and resend outcomes the
	// response-shape tests assert the DTO mapping over.
	listPage     list.Page[invitations.Invitation]
	resendResult invitations.Invitation
}

func (s *spyInvitationService) CreatePrepared(_ context.Context, p invitations.PreparedCreate) (invitations.CreateResult, error) {
	s.createCalled = true
	s.lastCreate = p.Input()
	return s.createResult, nil
}
func (s *spyInvitationService) ListByResource(context.Context, string, string, list.Request) (list.Page[invitations.Invitation], error) {
	s.listCalled = true
	return s.listPage, nil
}
func (s *spyInvitationService) Mine(context.Context, string, string, list.Request) (list.Page[invitations.Invitation], error) {
	s.mineCalled = true
	return list.Page[invitations.Invitation]{}, nil
}
func (s *spyInvitationService) Accept(context.Context, invitations.AcceptInput) (invitations.AcceptResult, error) {
	s.acceptCalled = true
	return invitations.AcceptResult{}, nil
}
func (s *spyInvitationService) Decline(context.Context, string, string) error { return nil }
func (s *spyInvitationService) PrepareManagement(ctx context.Context, id string) (invitations.PreparedManagement, error) {
	issuer := s.resendResult.InvitedBy
	if issuer == "" {
		issuer = "u1"
	}
	return preparedHTTPManagement(ctx, id, issuer)
}
func (s *spyInvitationService) Cancel(context.Context, invitations.PreparedManagement) error {
	s.cancelCalled = true
	return nil
}
func (s *spyInvitationService) Resend(context.Context, invitations.PreparedManagement, string) (invitations.Invitation, error) {
	s.resendCalled = true
	return s.resendResult, nil
}

// newInvitationTestHandler mounts the routes with a wired InvitationService, so
// the invitation surface IS registered. The stub authorizes everything — the
// caller-reachability tests here never exercise the policy.
func newInvitationTestHandler(t *testing.T, inv InvitationService) http.Handler {
	t.Helper()
	users := newMemUsers()
	svc := newServiceWithFakes(authenticationFixture{
		Users:       users,
		Identifiers: newMemIdentifiers(users),
		Passwords:   &memPasswords{m: map[string]string{}},
		Sessions:    &memSessions{m: map[string]session.Session{}},
		Hasher:      fakeHasher{},
		Limiter:     ratelimiter.NewMemory(),
		TokenSigner: newFakeSigner(),
	})
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc, Invitations: inv, InviteCheck: allowInviteCheck, ListStrategy: list.StrategyCursor})
	return h
}

// invitationFixture wires a real authlogic.Service over the mem stores with a spy
// invitation service, a configurable InviteCheck, and a configurable limiter, so a
// test can log in, revoke the session, and assert the policy/live-session gates.
// The policy is carried by the handlers, independently of the service.
type invitationFixture struct {
	h         http.Handler
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
	sessions  *memSessions
	inv       *spyInvitationService
}

func newInvitationFixture(t *testing.T, check InviteCheck, limiter ratelimiter.Limiter) invitationFixture {
	t.Helper()
	return newInvitationFixtureWith(t, nil, check, limiter)
}

// newInvitationFixtureWith is newInvitationFixture with an explicit
// InvitationService: nil mounts the recording spy (fixture.inv), and a supplied
// service replaces it for tests that need service-side behavior (ownership).
func newInvitationFixtureWith(t *testing.T, inv InvitationService, check InviteCheck, limiter ratelimiter.Limiter) invitationFixture {
	t.Helper()
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	spy := &spyInvitationService{}
	policy := func(ctx context.Context, req InviteCheckRequest) error {
		spy.lastPrincipal = req.Principal
		if check == nil {
			return sdk.ErrForbidden
		}
		return check(ctx, req)
	}
	if inv == nil {
		inv = spy
	}
	svc := newServiceWithFakes(authenticationFixture{
		Users:       users,
		Identifiers: idents,
		Passwords:   passwords,
		Sessions:    sessions,
		Hasher:      fakeHasher{},
		Limiter:     limiter,
		TokenSigner: newFakeSigner(),
	})
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: svc, Invitations: inv, InviteCheck: policy, ListStrategy: list.StrategyCursor})
	return invitationFixture{h: h, users: users, idents: idents, passwords: passwords, sessions: sessions, inv: spy}
}

// seedLoginUser inserts a user with a password and a verified login+recovery
// email so a cookie login resolves it (reusing methods_test's verifiedEmail).
func (f invitationFixture) seedLoginUser(userID, emailAddr string) {
	f.users.mu.Lock()
	f.users.byID[userID] = user.User{ID: userID, DisplayName: "Seed"}
	f.users.mu.Unlock()
	f.passwords.mu.Lock()
	f.passwords.m[userID] = "hash:password123456789"
	f.passwords.mu.Unlock()
	f.idents.insert(verifiedEmail("id-primary", userID, emailAddr))
}

// login authenticates the seeded email over the cookie lane and returns the live
// session cookie carrying an otherwise-unexpired access JWT.
func (f invitationFixture) login(t *testing.T, emailAddr string) *http.Cookie {
	t.Helper()
	rec := do(t, f.h, "POST", "/auth/login", `{"email":"`+emailAddr+`","password":"password123456789"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", rec.Code, rec.Body)
	}
	c := sessionCookie(rec)
	if c == nil {
		t.Fatal("login set no session cookie")
	}
	return c
}

// doCSRF is do for a cookie-lane invitation mutation: it adds the
// __Host-auth_csrf double-submit pair (cookie + matching X-CSRF-Token header) the
// browser-safe gate compares, as a browser client must send it.
func doCSRF(t *testing.T, h http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token"})
	r.Header.Set(csrfHeaderName, "csrf-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestInvitationRoutesDenyByAbsence proves the whole invitation surface is NOT
// registered when no Granter (→ nil InvitationService) is wired: every route
// returns 404, never a 401 from a gated-but-registered route (design §6
// deny-by-absence — the recorded acceptance proof).
func TestInvitationRoutesDenyByAbsence(t *testing.T) {
	h := newTestHandler(t, nil) // mount(..., nil) → invitations off
	routes := []struct{ method, path string }{
		{"POST", "/auth/invitations/project/p1"},
		{"GET", "/auth/invitations/project/p1"},
		{"GET", "/auth/invitations/mine"},
		{"POST", "/auth/invitations/accept"},
		{"POST", "/auth/invitations/inv-1/cancel"},
		{"POST", "/auth/invitations/inv-1/resend"},
		{"POST", "/auth/invitations/inv-1/decline"},
		{"POST", "/auth/invitations/decline"},
	}
	for _, rt := range routes {
		rec := do(t, h, rt.method, rt.path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s deny-by-absence = %d, want 404", rt.method, rt.path, rec.Code)
		}
	}
}

// TestInvitationRoutesRegisteredWhenWired proves the surface IS registered when a
// Granter is wired: a session-gated route without a session is 401 (registered,
// gated — not 404), and the public decline route exists (bad body → 400).
func TestInvitationRoutesRegisteredWhenWired(t *testing.T) {
	h := newInvitationTestHandler(t, stubInvitationService{})

	rec := do(t, h, "GET", "/auth/invitations/mine", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /auth/invitations/mine without session = %d, want 401 (registered + gated)", rec.Code)
	}
	rec = do(t, h, "POST", "/auth/invitations/inv-1/decline", "not-json")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST decline with bad body = %d, want 400 (registered, public)", rec.Code)
	}
}

// TestInvitationCreateRelationDeniedNeverReachesService proves the create handler
// drives the AUTHORIZED create operation with the EXACT requested relation and
// maps its refusal: a resource-allowed but relation-denied create (editor
// forbidden from inviting an owner) is 403 and no invitation is created (design
// §6/D3; the check-before-side-effect ordering itself is pinned in invitation service).
func TestInvitationCreateRelationDeniedNeverReachesService(t *testing.T) {
	// The policy authorizes creation but forbids the "owner" relation specifically —
	// proving it sees the validated payload, not just the route.
	relationAware := func(_ context.Context, req InviteCheckRequest) error {
		if req.Action == InviteCreate && req.Relation == "owner" {
			return fmt.Errorf("cannot invite an owner: %w", sdk.ErrForbidden)
		}
		return nil
	}
	f := newInvitationFixture(t, relationAware, nil)
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1",
		`{"identifier":"bob@example.com","relation":"owner"}`, cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("relation-denied create = %d, want 403; body=%s", rec.Code, rec.Body)
	}
	if f.inv.createCalled {
		t.Fatal("relation-denied create reached the invitation service")
	}
}

// TestInvitationCreateRelationAllowedReachesService proves the same policy admits
// a permitted relation: the exact requested relation the host allows reaches the
// authorized create operation — with the SESSION caller as the principal — and
// returns the pending-invitation contract (201 + the DTO).
func TestInvitationCreateRelationAllowedReachesService(t *testing.T) {
	relationAware := func(_ context.Context, req InviteCheckRequest) error {
		if req.Action == InviteCreate && req.Relation == "owner" {
			return fmt.Errorf("cannot invite an owner: %w", sdk.ErrForbidden)
		}
		return nil
	}
	f := newInvitationFixture(t, relationAware, nil)
	f.inv.createResult = invitations.CreateResult{Invitation: invitations.Invitation{
		ID: "inv-1", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "bob@example.com", Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}}
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1",
		`{"identifier":"bob@example.com","relation":"member"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("allowed create = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	if !f.inv.createCalled {
		t.Fatal("allowed create never reached the invitation service")
	}
	if want := (sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "u1"}); f.inv.lastPrincipal != want {
		t.Errorf("authorized create principal = %+v, want the session caller %+v", f.inv.lastPrincipal, want)
	}
	var resp invitationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if resp.ID != "inv-1" || resp.Relation != "member" || resp.Status != "pending" {
		t.Fatalf("create DTO = %+v, want id=inv-1 relation=member status=pending", resp)
	}
}

// TestInvitationListDeniedNeverReachesService proves the resource-list handler
// drives the AUTHORIZED list operation and maps its refusal: a denied list is 403
// and no page is read (design §6/D3).
func TestInvitationListDeniedNeverReachesService(t *testing.T) {
	denyList := func(_ context.Context, req InviteCheckRequest) error {
		if req.Action == InviteList {
			if req.Relation != "" {
				return errors.New("list check must carry an empty relation")
			}
			return fmt.Errorf("cannot list invitations: %w", sdk.ErrForbidden)
		}
		return nil
	}
	f := newInvitationFixture(t, denyList, nil)
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "GET", "/auth/invitations/project/p1", "", cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied list = %d, want 403; body=%s", rec.Code, rec.Body)
	}
	if f.inv.listCalled {
		t.Fatal("denied list reached the invitation service")
	}
}

// TestInvitationListAllowedReachesService proves an authorized list reaches the
// service and returns 200 (the existing page contract), and that the authorized
// list operation receives the SESSION caller as its principal — the value the host
// policy is posed for.
func TestInvitationListAllowedReachesService(t *testing.T) {
	f := newInvitationFixture(t, allowInviteCheck, nil)
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "GET", "/auth/invitations/project/p1", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed list = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if !f.inv.listCalled {
		t.Fatal("allowed list never reached the invitation service")
	}
	if want := (sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "u1"}); f.inv.lastPrincipal != want {
		t.Errorf("authorized list principal = %+v, want the session caller %+v", f.inv.lastPrincipal, want)
	}
}

// TestInvitationCreateFailsClosed proves the create route fails CLOSED on every
// pre-create failure and never creates an invitation: a missing principal (no
// session) is 401 and never reaches the service at all, a policy denial is 403,
// and a policy infrastructure error is 500 (design §6/D3 — an unexpected policy
// error never falls through to allow).
func TestInvitationCreateFailsClosed(t *testing.T) {
	body := `{"identifier":"bob@example.com","relation":"member"}`

	t.Run("missing principal", func(t *testing.T) {
		f := newInvitationFixture(t, allowInviteCheck, nil)
		rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1", body)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("no-session create = %d, want 401; body=%s", rec.Code, rec.Body)
		}
		if f.inv.createCalled {
			t.Fatal("no-session create reached the invitation service")
		}
	})

	t.Run("policy denial", func(t *testing.T) {
		deny := func(context.Context, InviteCheckRequest) error {
			return fmt.Errorf("denied: %w", sdk.ErrForbidden)
		}
		f := newInvitationFixture(t, deny, nil)
		f.seedLoginUser("u1", "alice@example.com")
		cookie := f.login(t, "alice@example.com")
		rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1", body, cookie)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("denied create = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if f.inv.createCalled {
			t.Fatal("denied create reached the invitation service")
		}
	})

	t.Run("policy infrastructure error", func(t *testing.T) {
		boom := func(context.Context, InviteCheckRequest) error {
			return errors.New("policy backend unreachable")
		}
		f := newInvitationFixture(t, boom, nil)
		f.seedLoginUser("u1", "alice@example.com")
		cookie := f.login(t, "alice@example.com")
		rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1", body, cookie)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("policy-error create = %d, want 500; body=%s", rec.Code, rec.Body)
		}
		if f.inv.createCalled {
			t.Fatal("policy-error create reached the invitation service")
		}
	})
}

// TestInvitationRoutesRejectRevokedSession proves every authenticated invitation
// route rides the Invitations authenticator: a revoked (deleted) session presenting an
// otherwise-unexpired access JWT is denied within one round-trip, and the
// invitation service is never reached (design §6/D3, §1.4).
func TestInvitationRoutesRejectRevokedSession(t *testing.T) {
	routes := []struct {
		name, method, path, body string
	}{
		{"create", "POST", "/auth/invitations/project/p1", `{"identifier":"bob@example.com","relation":"member"}`},
		{"resource-list", "GET", "/auth/invitations/project/p1", ""},
		{"mine", "GET", "/auth/invitations/mine", ""},
		{"accept", "POST", "/auth/invitations/accept", `{"token":"t"}`},
		{"cancel", "POST", "/auth/invitations/inv-1/cancel", ""},
		{"resend", "POST", "/auth/invitations/inv-1/resend", ""},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			f := newInvitationFixture(t, allowInviteCheck, nil)
			f.seedLoginUser("u1", "alice@example.com")
			cookie := f.login(t, "alice@example.com")
			// Revoke the session while the access JWT in the cookie is still unexpired.
			if err := f.sessions.DeleteByUser(context.Background(), "u1"); err != nil {
				t.Fatalf("DeleteByUser: %v", err)
			}
			rec := do(t, f.h, rt.method, rt.path, rt.body, cookie)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("revoked-session %s = %d, want 401; body=%s", rt.name, rec.Code, rec.Body)
			}
			if f.inv.createCalled || f.inv.listCalled || f.inv.mineCalled ||
				f.inv.acceptCalled || f.inv.cancelCalled || f.inv.resendCalled {
				t.Fatalf("revoked-session %s reached the invitation service", rt.name)
			}
		})
	}
}

// TestInvitationDeclineReachableWithoutSession proves the public decline route stays
// reachable without a session: a valid token body succeeds (the service enforces the
// token), so decline is NOT swept under the live-session gate (design §6).
func TestInvitationDeclineReachableWithoutSession(t *testing.T) {
	f := newInvitationFixture(t, allowInviteCheck, nil)
	rec := do(t, f.h, "POST", "/auth/invitations/inv-1/decline", `{"token":"secret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("public decline = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

// TestInvitationDeclineRateLimited proves the public decline route keeps its IP
// rate-limit control: a refusing limiter returns 429 before the handler runs.
func TestInvitationDeclineRateLimited(t *testing.T) {
	f := newInvitationFixture(t, allowInviteCheck, denyLimiter{})
	rec := do(t, f.h, "POST", "/auth/invitations/inv-1/decline", `{"token":"secret"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited decline = %d, want 429; body=%s", rec.Code, rec.Body)
	}
}

// TestInvitationDeclineOriginGated proves decline carries the ORIGIN-ONLY gate:
// a cross-site browser page is refused before the handler, while the session-less
// token decline (no Origin, no CSRF pair) stays reachable (see
// TestInvitationDeclineReachableWithoutSession).
func TestInvitationDeclineOriginGated(t *testing.T) {
	f := newInvitationFixture(t, allowInviteCheck, nil)
	r := httptest.NewRequest("POST", "/auth/invitations/inv-1/decline", strings.NewReader(`{"token":"secret"}`))
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), originRejectedCode) {
		t.Fatalf("cross-site decline = %d, want 403 %s; body=%s", rec.Code, originRejectedCode, rec.Body)
	}
}

// TestInvitationMutationsRequireCSRFOnCookieTransport pins issue #105: every
// authenticated invitation mutation carries the double-submit gate on a cookie
// transport — the native session cookie and a provider cookie
// (WithExternalCookies) alike — while bearer-only callers (native access token,
// provider token) need no token. An act-as-user API key bearer is refused by
// the Invitations authenticator before the CSRF gate is consulted.
func TestInvitationMutationsRequireCSRFOnCookieTransport(t *testing.T) {
	h := newExternalHarness(t, externalSetup{cookies: []string{extCookie}})
	mux, _ := newExternalRoutes(t, h, RouteAuthentication{})
	apiKey, _, _ := h.mintKey(t, "inviter-bot", true, h.userID, time.Time{})
	routes := []struct{ name, path, body string }{
		{"create", "/auth/invitations/project/p1", `{"identifier":"bob@example.com","relation":"member"}`},
		{"accept", "/auth/invitations/accept", `{"token":"t"}`},
		{"cancel", "/auth/invitations/inv-1/cancel", ""},
		{"resend", "/auth/invitations/inv-1/resend", ""},
	}
	csrfRefused := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "CSRF")
	}
	cookies := map[string]*http.Cookie{
		"native cookie":   {Name: "session", Value: h.accessJWT},
		"provider cookie": {Name: extCookie, Value: h.token},
	}
	bearers := map[string]string{"native bearer": h.accessJWT, "provider bearer": h.token}
	for _, rt := range routes {
		for name, c := range cookies {
			t.Run(rt.name+"/"+name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, routeRequest("POST", rt.path, rt.body, func(r *http.Request) { r.AddCookie(c) }))
				if !csrfRefused(rec) {
					t.Fatalf("no CSRF token = %d, want 403 CSRF; body=%s", rec.Code, rec.Body)
				}
				rec = httptest.NewRecorder()
				mux.ServeHTTP(rec, routeRequest("POST", rt.path, rt.body, func(r *http.Request) {
					r.AddCookie(c)
					r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token"})
					r.Header.Set(csrfHeaderName, "csrf-token")
				}))
				if csrfRefused(rec) || rec.Code == http.StatusUnauthorized {
					t.Fatalf("matching CSRF pair = %d, want past the gates; body=%s", rec.Code, rec.Body)
				}
			})
		}
		for name, token := range bearers {
			t.Run(rt.name+"/"+name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, routeRequest("POST", rt.path, rt.body, func(r *http.Request) {
					r.Header.Set("Authorization", "Bearer "+token)
				}))
				if csrfRefused(rec) || rec.Code == http.StatusUnauthorized {
					t.Fatalf("bearer without CSRF token = %d, want past the gates; body=%s", rec.Code, rec.Body)
				}
			})
		}
		t.Run(rt.name+"/act-as-user api key", func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, routeRequest("POST", rt.path, rt.body, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+apiKey)
			}))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("act-as-user api key bearer = %d, want 401; body=%s", rec.Code, rec.Body)
			}
		})
	}
}

// TestInvitationResponseCarriesInvitedBy proves the owner field is on EVERY
// response built from the invitation DTO mapper — create (201), resource list
// (200), and resend (200) — so a resource list can tell the rows the current admin
// owns from another admin's. It is an identifier, never the token: the response
// still carries no secret. The server enforces ownership on cancel/resend
// regardless of what a client renders (see TestInvitationCancelResend*).
func TestInvitationResponseCarriesInvitedBy(t *testing.T) {
	const owner = "u-owner"
	other := invitations.Invitation{
		ID: "inv-2", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "carol@example.com", InvitedBy: "u-other", Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	mine := invitations.Invitation{
		ID: "inv-1", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "bob@example.com", InvitedBy: owner, Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}

	assertNoToken := func(t *testing.T, body string) {
		t.Helper()
		if strings.Contains(body, "token") {
			t.Fatalf("response leaked a token field: %s", body)
		}
	}

	t.Run("create", func(t *testing.T) {
		f := newInvitationFixture(t, allowInviteCheck, nil)
		f.inv.createResult = invitations.CreateResult{Invitation: mine}
		f.seedLoginUser(owner, "alice@example.com")
		cookie := f.login(t, "alice@example.com")

		rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1",
			`{"identifier":"bob@example.com","relation":"member","auto_accept":true}`, cookie)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		var resp invitationResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body)
		}
		if resp.InvitedBy != owner {
			t.Errorf("create invited_by = %q, want %q", resp.InvitedBy, owner)
		}
		assertNoToken(t, rec.Body.String())
	})

	t.Run("resource list", func(t *testing.T) {
		f := newInvitationFixture(t, allowInviteCheck, nil)
		f.inv.listPage = list.Page[invitations.Invitation]{Items: []invitations.Invitation{mine, other}}
		f.seedLoginUser(owner, "alice@example.com")
		cookie := f.login(t, "alice@example.com")

		rec := do(t, f.h, "GET", "/auth/invitations/project/p1", "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var page pageResponse[invitationResponse]
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body)
		}
		if len(page.Items) != 2 {
			t.Fatalf("list items = %d, want 2", len(page.Items))
		}
		if page.Items[0].InvitedBy != owner || page.Items[1].InvitedBy != "u-other" {
			t.Errorf("list invited_by = [%q %q], want [%q u-other]",
				page.Items[0].InvitedBy, page.Items[1].InvitedBy, owner)
		}
		assertNoToken(t, rec.Body.String())
	})

	t.Run("resend", func(t *testing.T) {
		f := newInvitationFixture(t, allowInviteCheck, nil)
		f.inv.resendResult = mine
		f.seedLoginUser(owner, "alice@example.com")
		cookie := f.login(t, "alice@example.com")

		rec := doCSRF(t, f.h, "POST", "/auth/invitations/inv-1/resend", "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("resend = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var resp invitationResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body)
		}
		if resp.InvitedBy != owner {
			t.Errorf("resend invited_by = %q, want %q", resp.InvitedBy, owner)
		}
		assertNoToken(t, rec.Body.String())
	})
}

// TestInvitationCancelResendOwnershipStaysServerEnforced checks issuer admission
// in HTTP, independently of the policy-free lifecycle service.
func TestInvitationCancelResendOwnershipStaysServerEnforced(t *testing.T) {
	routes := []struct{ name, path string }{
		{"cancel", "/auth/invitations/inv-1/cancel"},
		{"resend", "/auth/invitations/inv-1/resend"},
	}
	for _, rt := range routes {
		t.Run(rt.name+"/owner", func(t *testing.T) {
			f := newInvitationFixtureWith(t, &ownerAwareInvitationService{owner: "u-owner"}, allowInviteCheck, nil)
			f.seedLoginUser("u-owner", "owner@example.com")
			rec := doCSRF(t, f.h, "POST", rt.path, "", f.login(t, "owner@example.com"))
			if rec.Code != http.StatusOK {
				t.Fatalf("owner %s = %d, want 200; body=%s", rt.name, rec.Code, rec.Body)
			}
		})
		t.Run(rt.name+"/non-owner", func(t *testing.T) {
			f := newInvitationFixtureWith(t, &ownerAwareInvitationService{owner: "u-owner"}, allowInviteCheck, nil)
			f.seedLoginUser("u-other", "other@example.com")
			rec := doCSRF(t, f.h, "POST", rt.path, "", f.login(t, "other@example.com"))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("non-owner %s = %d, want 403; body=%s", rt.name, rec.Code, rec.Body)
			}
		})
	}
}

// ownerAwareInvitationService supplies the stored issuer without choosing access.
type ownerAwareInvitationService struct {
	stubInvitationService
	owner string
}

func (s *ownerAwareInvitationService) PrepareManagement(ctx context.Context, id string) (invitations.PreparedManagement, error) {
	return preparedHTTPManagement(ctx, id, s.owner)
}
func (s *ownerAwareInvitationService) Resend(context.Context, invitations.PreparedManagement, string) (invitations.Invitation, error) {
	return invitations.Invitation{ID: "inv-1", InvitedBy: s.owner, Status: "pending"}, nil
}

// TestInvitationCreateForwardsMetadata proves metadata survives preparation and
// the inbound policy check, while a response carrying none omits the key.
func TestInvitationCreateForwardsMetadata(t *testing.T) {
	f := newInvitationFixture(t, allowInviteCheck, nil)
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1",
		`{"identifier":"bob@example.com","relation":"member","metadata":{"routing_key":"org-1"}}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	want := map[string]string{"routing_key": "org-1"}
	if !maps.Equal(f.inv.lastCreate.Metadata, want) {
		t.Errorf("CreateInput metadata = %+v, want %+v", f.inv.lastCreate.Metadata, want)
	}
	if strings.Contains(rec.Body.String(), "metadata") {
		t.Errorf("an invitation with no metadata still emitted the key: %s", rec.Body)
	}
}

// TestInvitationCreateMetadataAbsentIsEmpty proves an omitted metadata object is
// the no-metadata case: CreateInput.Metadata is empty, never a decode failure.
func TestInvitationCreateMetadataAbsentIsEmpty(t *testing.T) {
	f := newInvitationFixture(t, allowInviteCheck, nil)
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1",
		`{"identifier":"bob@example.com","relation":"member"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	if len(f.inv.lastCreate.Metadata) != 0 {
		t.Errorf("absent metadata = %+v, want empty", f.inv.lastCreate.Metadata)
	}
}

// TestInvitationCreateOversizedBodyRejected proves the create route bounds the
// body before decoding the unbounded metadata object: past the live-session gate,
// a body over the JSON limit is 413 and never reaches the service.
func TestInvitationCreateOversizedBodyRejected(t *testing.T) {
	f := newInvitationFixture(t, allowInviteCheck, nil)
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")
	big := strings.Repeat("a", (1<<20)+1) // just over maxJSONBodyBytes
	body := `{"identifier":"bob@example.com","relation":"member","metadata":{"k":"` + big + `"}}`

	rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1", body, cookie)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d, want 413; body=%s", rec.Code, rec.Body)
	}
	if f.inv.createCalled {
		t.Fatal("oversized create reached the invitation service")
	}
}

// TestInvitationOwnerResponsesCarryMetadata proves the RESOURCE-OWNER projection
// round-trips the persisted host metadata on every endpoint an inviting owner
// drives — pending create, resource list, and resend — so an owner can verify and
// audit the routing choice they supplied.
func TestInvitationOwnerResponsesCarryMetadata(t *testing.T) {
	const owner = "u-owner"
	want := map[string]string{"routing_key": "route-1"}
	withMeta := invitations.Invitation{
		ID: "inv-1", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "bob@example.com", InvitedBy: owner, Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		Metadata: want,
	}

	t.Run("create", func(t *testing.T) {
		f := newInvitationFixture(t, allowInviteCheck, nil)
		f.inv.createResult = invitations.CreateResult{Invitation: withMeta}
		f.seedLoginUser(owner, "alice@example.com")
		cookie := f.login(t, "alice@example.com")

		rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1",
			`{"identifier":"bob@example.com","relation":"member","metadata":{"routing_key":"route-1"}}`, cookie)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		var resp invitationResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body)
		}
		if !maps.Equal(resp.Metadata, want) {
			t.Errorf("create metadata = %+v, want %+v", resp.Metadata, want)
		}
	})

	t.Run("resource list", func(t *testing.T) {
		f := newInvitationFixture(t, allowInviteCheck, nil)
		f.inv.listPage = list.Page[invitations.Invitation]{Items: []invitations.Invitation{withMeta}}
		f.seedLoginUser(owner, "alice@example.com")
		cookie := f.login(t, "alice@example.com")

		rec := do(t, f.h, "GET", "/auth/invitations/project/p1", "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var page pageResponse[invitationResponse]
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body)
		}
		if len(page.Items) != 1 || !maps.Equal(page.Items[0].Metadata, want) {
			t.Errorf("list metadata = %+v, want %+v", page.Items, want)
		}
	})

	t.Run("resend", func(t *testing.T) {
		f := newInvitationFixture(t, allowInviteCheck, nil)
		f.inv.resendResult = withMeta
		f.seedLoginUser(owner, "alice@example.com")
		cookie := f.login(t, "alice@example.com")

		rec := doCSRF(t, f.h, "POST", "/auth/invitations/inv-1/resend", "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("resend = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var resp invitationResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body)
		}
		if !maps.Equal(resp.Metadata, want) {
			t.Errorf("resend metadata = %+v, want %+v", resp.Metadata, want)
		}
	})
}

// TestInvitationMineNeverCarriesMetadata proves the recipient-facing projection is
// a genuinely separate DTO: /auth/invitations/mine never emits the metadata key,
// even for a row that carries host metadata — it is issuer→host routing, not
// something the invitee reads.
func TestInvitationMineNeverCarriesMetadata(t *testing.T) {
	f := newInvitationFixtureWith(t, &metadataMineInvitationService{}, allowInviteCheck, nil)
	f.seedLoginUser("u1", "bob@example.com")
	cookie := f.login(t, "bob@example.com")

	rec := do(t, f.h, "GET", "/auth/invitations/mine", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("mine = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "metadata") || strings.Contains(rec.Body.String(), "routing_key") {
		t.Fatalf("/mine leaked host metadata: %s", rec.Body)
	}
	var page pageResponse[myInvitationResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "inv-1" {
		t.Fatalf("mine items = %+v, want the seeded invitation", page.Items)
	}
}

// TestInvitationResponsesOmitEmptyMetadata proves an invitation that never carried
// metadata keeps a byte-identical response on the owner projection: the key omits
// rather than serializing null or {}.
func TestInvitationResponsesOmitEmptyMetadata(t *testing.T) {
	f := newInvitationFixture(t, allowInviteCheck, nil)
	f.inv.listPage = list.Page[invitations.Invitation]{Items: []invitations.Invitation{{
		ID: "inv-1", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "bob@example.com", InvitedBy: "u-owner", Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		Metadata: map[string]string{},
	}}}
	f.seedLoginUser("u-owner", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "GET", "/auth/invitations/project/p1", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "metadata") {
		t.Errorf("empty metadata emitted a key: %s", rec.Body)
	}
}

// metadataMineInvitationService returns one "my invitation" carrying host
// metadata, so the /mine projection is exercised against a row that HAS something
// to leak.
type metadataMineInvitationService struct {
	stubInvitationService
}

func (metadataMineInvitationService) Mine(context.Context, string, string, list.Request) (list.Page[invitations.Invitation], error) {
	return list.Page[invitations.Invitation]{Items: []invitations.Invitation{{
		ID: "inv-1", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "bob@example.com", InvitedBy: "u-owner", Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		Metadata: map[string]string{"routing_key": "route-1"},
	}}}, nil
}

// compile-time proof the spy satisfies the consumed InvitationService port.
var _ InvitationService = (*spyInvitationService)(nil)

var _ invitationTokenDecliner = (*invitations.Service)(nil)

// tokenDeclineRepo is the slice of InvitationRepository a token decline reads
// and writes, keyed by token hash with the store contract's expiry rule.
type tokenDeclineRepo struct {
	invitations.InvitationRepository
	mu     sync.Mutex
	byHash map[string]invitations.Invitation
}

func (r *tokenDeclineRepo) GetByTokenHash(_ context.Context, hash string) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.byHash[hash]
	if !ok {
		return invitations.Invitation{}, sdk.ErrNotFound
	}
	if inv.Status != invitations.StatusAccepting && inv.Status != invitations.StatusAccepted && inv.Expired(time.Now()) {
		return invitations.Invitation{}, sdk.ErrExpired
	}
	return inv, nil
}

func (r *tokenDeclineRepo) Get(_ context.Context, id string) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, inv := range r.byHash {
		if inv.ID == id {
			return inv, nil
		}
	}
	return invitations.Invitation{}, sdk.ErrNotFound
}

func (r *tokenDeclineRepo) UpdateStatus(_ context.Context, id string, upd invitations.StatusUpdate) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hash, inv := range r.byHash {
		if inv.ID == id {
			updated, err := inv.UpdateStatus(upd)
			if err != nil {
				return invitations.Invitation{}, err
			}
			r.byHash[hash] = updated
			return updated, nil
		}
	}
	return invitations.Invitation{}, sdk.ErrNotFound
}

func (r *tokenDeclineRepo) status(t *testing.T, token string) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byHash[mustHash(t, token)].Status
}

func mustHash(t *testing.T, token string) string {
	t.Helper()
	hash, err := cryptids.SHA256(token)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// newTokenDeclineService is the real invitation service over tokenDeclineRepo,
// seeded with one invitation per token in the given state.
func newTokenDeclineService(t *testing.T, seed map[string]string) (*invitations.Service, *tokenDeclineRepo) {
	t.Helper()
	repo := &tokenDeclineRepo{byHash: map[string]invitations.Invitation{}}
	for token, status := range seed {
		inv, err := invitations.NewInvitation(sdk.IDGenerator{}, "project", "p1", "member", token+"@x.com", sdk.AddressKindEmail, "u1", mustHash(t, token), false, time.Hour, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		switch status {
		case "expired":
			inv.ExpiresAt = time.Now().Add(-time.Minute)
		case "declined-expired":
			inv.Status, inv.ExpiresAt = invitations.StatusDeclined, time.Now().Add(-time.Minute)
		case "accepted-expired":
			inv.Status, inv.ExpiresAt = invitations.StatusAccepted, time.Now().Add(-time.Minute)
		case "":
		default:
			inv.Status = status
		}
		repo.byHash[inv.TokenHash] = inv
	}
	svc, err := invitations.New(repo, unusedInvitationGranter{})
	if err != nil {
		t.Fatal(err)
	}
	return svc, repo
}

// TestInvitationDeclineByTokenResponses pins the POST /auth/invitations/decline
// response matrix over the real service: success, an unknown or wrong token as
// one not-found body, expiry, a second click as the not-pending conflict, and
// a missing token as the id route's bad-body 400.
func TestInvitationDeclineByTokenResponses(t *testing.T) {
	svc, repo := newTokenDeclineService(t, map[string]string{"tok-pending": "", "tok-expired": "expired", "tok-accepted": invitations.StatusAccepted, "tok-accepting": invitations.StatusAccepting, "tok-declined-expired": "declined-expired", "tok-accepted-expired": "accepted-expired"})
	f := newInvitationFixtureWith(t, svc, allowInviteCheck, nil)

	rec := do(t, f.h, "POST", "/auth/invitations/decline", `{"token":"tok-pending"}`)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"declined"}` {
		t.Fatalf("decline = %d %s", rec.Code, rec.Body)
	}
	if got := repo.status(t, "tok-pending"); got != invitations.StatusDeclined {
		t.Fatalf("status = %q, want declined", got)
	}

	unknown := do(t, f.h, "POST", "/auth/invitations/decline", `{"token":"tok-unknown"}`)
	wrong := do(t, f.h, "POST", "/auth/invitations/decline", `{"token":"tok-pending-but-wrong"}`)
	idRoute := do(t, f.h, "POST", "/auth/invitations/inv-1/decline", `{"token":"x"}`)
	if unknown.Code != http.StatusNotFound || wrong.Code != http.StatusNotFound || unknown.Body.String() != wrong.Body.String() || idRoute.Body.String() != unknown.Body.String() {
		t.Fatalf("unknown = %d %s; wrong = %d %s; id route = %d %s", unknown.Code, unknown.Body, wrong.Code, wrong.Body, idRoute.Code, idRoute.Body)
	}
	if strings.Contains(unknown.Body.String(), "tok-unknown") {
		t.Fatalf("not-found body echoes the token: %s", unknown.Body)
	}

	// Past ExpiresAt the stores expire every unclaimed row, declined included;
	// claimed rows (accepted/accepting) stay readable and conflict.
	for _, tok := range []string{"tok-expired", "tok-declined-expired"} {
		if rec := do(t, f.h, "POST", "/auth/invitations/decline", `{"token":"`+tok+`"}`); rec.Code != http.StatusGone {
			t.Fatalf("%s = %d %s, want 410", tok, rec.Code, rec.Body)
		}
	}
	f = newInvitationFixtureWith(t, svc, allowInviteCheck, nil)
	for _, tok := range []string{"tok-pending", "tok-accepted", "tok-accepting", "tok-accepted-expired"} {
		rec := do(t, f.h, "POST", "/auth/invitations/decline", `{"token":"`+tok+`"}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s = %d %s, want 409", tok, rec.Code, rec.Body)
		}
	}
	if got := repo.status(t, "tok-accepting"); got != invitations.StatusAccepting {
		t.Fatalf("accepting claim changed to %q", got)
	}

	f = newInvitationFixtureWith(t, svc, allowInviteCheck, nil)
	for _, body := range []string{`{}`, `{"token":""}`, "not-json"} {
		tokenRoute := do(t, f.h, "POST", "/auth/invitations/decline", body)
		badBody := do(t, f.h, "POST", "/auth/invitations/inv-1/decline", "not-json")
		if tokenRoute.Code != http.StatusBadRequest || tokenRoute.Body.String() != badBody.Body.String() {
			t.Fatalf("body %q = %d %s, want the id route's %d %s", body, tokenRoute.Code, tokenRoute.Body, badBody.Code, badBody.Body)
		}
	}
}

// TestInvitationDeclineByTokenGates pins the token route's transport gates:
// cross-origin refused as the id route refuses, POST only, and no collision
// with the resource create route or accept.
func TestInvitationDeclineByTokenGates(t *testing.T) {
	svc, _ := newTokenDeclineService(t, map[string]string{"tok": ""})
	f := newInvitationFixtureWith(t, svc, allowInviteCheck, nil)

	for _, path := range []string{"/auth/invitations/decline", "/auth/invitations/inv-1/decline"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"token":"tok"}`))
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), originRejectedCode) {
			t.Fatalf("cross-site %s = %d %s, want 403 %s", path, rec.Code, rec.Body, originRejectedCode)
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		if rec := do(t, f.h, method, "/auth/invitations/decline", ""); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s decline = %d, want 405", method, rec.Code)
		}
	}
	// A resource type named "decline" stays the create route and accept stays
	// accept (both session-gated): neither reaches the public decline.
	for _, path := range []string{"/auth/invitations/decline/p1", "/auth/invitations/project/p1", "/auth/invitations/accept"} {
		if rec := do(t, f.h, "POST", path, `{"token":"tok"}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("POST %s = %d %s, want 401", path, rec.Code, rec.Body)
		}
	}
}

// TestInvitationDeclineByTokenSharesBudget proves both decline routes draw on
// the one invitation_decline budget: spending it on the id route refuses the
// token route, and the reverse.
func TestInvitationDeclineByTokenSharesBudget(t *testing.T) {
	for _, order := range [][2]string{{"/auth/invitations/inv-1/decline", "/auth/invitations/decline"}, {"/auth/invitations/decline", "/auth/invitations/inv-1/decline"}} {
		svc, _ := newTokenDeclineService(t, nil)
		h := web.NewWebHandler()
		mount(h, mountDeps{
			Auth:        newServiceWithFakes(authenticationFixture{Users: newMemUsers(), Limiter: ratelimiter.NewMemory(), TokenSigner: newFakeSigner()}),
			Invitations: svc, InviteCheck: allowInviteCheck, ListStrategy: list.StrategyCursor,
			RouteLimits: PublicRouteLimits{InvitationDecline: ratelimiter.Limit{Requests: 1, Window: time.Minute}},
		})
		first := do(t, h, "POST", order[0], `{"token":"tok"}`)
		if first.Code == http.StatusTooManyRequests {
			t.Fatalf("first %s refused: %s", order[0], first.Body)
		}
		if second := do(t, h, "POST", order[1], `{"token":"tok"}`); second.Code != http.StatusTooManyRequests {
			t.Fatalf("%s after %s = %d, want 429 (shared budget)", order[1], order[0], second.Code)
		}
	}
}

// TestInvitationDeclineByTokenUnmountedWithoutDecliner proves a host's
// InvitationService fake without DeclineByToken still mounts every other route
// and simply goes without the token decline.
func TestInvitationDeclineByTokenUnmountedWithoutDecliner(t *testing.T) {
	h := newInvitationTestHandler(t, stubInvitationService{})
	if rec := do(t, h, "POST", "/auth/invitations/decline", `{"token":"tok"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("token decline without a decliner = %d, want 404", rec.Code)
	}
	if rec := do(t, h, "POST", "/auth/invitations/inv-1/decline", `{"token":"tok"}`); rec.Code != http.StatusOK {
		t.Fatalf("id decline = %d, want 200", rec.Code)
	}
}

func (r *tokenDeclineRepo) Create(_ context.Context, inv invitations.Invitation) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byHash[inv.TokenHash] = inv
	return inv, nil
}

// TestInvitationCreateAttributesCallerActor proves the create handler attributes
// the invitation_created row to the calling user as Actor, as resend does.
func TestInvitationCreateAttributesCallerActor(t *testing.T) {
	events := newSpySecurityEvents()
	router, err := delivery.NewRouter(nopMailer{})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := invitations.New(&tokenDeclineRepo{byHash: map[string]invitations.Invitation{}}, unusedInvitationGranter{},
		invitations.WithDelivery(invitations.DeliveryConfig{Mailer: nopMailer{}, Deliver: router, Queue: stubQueue{}}),
		invitations.WithSecurityEvents(events))
	if err != nil {
		t.Fatal(err)
	}
	f := newInvitationFixtureWith(t, svc, allowInviteCheck, nil)
	f.seedLoginUser("u1", "alice@example.com")
	rec := doCSRF(t, f.h, "POST", "/auth/invitations/project/p1", `{"identifier":"bob@example.com","relation":"member"}`, f.login(t, "alice@example.com"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var created []securityevent.SecurityEvent
	for _, evt := range events.recorded() {
		if evt.EventType == securityevent.TypeInvitationCreated {
			created = append(created, evt)
		}
	}
	if len(created) != 1 {
		t.Fatalf("created rows = %+v", created)
	}
	evt := created[0]
	if evt.Actor != (securityevent.Principal{Type: sdk.PrincipalTypeUser, ID: "u1"}) || evt.UserID != "u1" || evt.Details["invited_by"] != "u1" {
		t.Fatalf("created row = %+v", evt)
	}
}

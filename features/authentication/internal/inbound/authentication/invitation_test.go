package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/invitationsvc"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// allowInviteCheck authorizes every invitation operation. Wired where a test
// exercises the routes past the host policy (the create/list allowed contract and
// the revoked-session middleware gate, which denies before the handler body).
func allowInviteCheck(context.Context, invitationsvc.InviteCheckRequest) error { return nil }

// stubInvitationService satisfies InvitationService with inert methods — enough
// to prove the routes register when a Granter is wired.
type stubInvitationService struct{}

func (stubInvitationService) CreateAuthorized(context.Context, identity.Principal, invitationsvc.CreateInput) (invitationsvc.CreateResult, error) {
	return invitationsvc.CreateResult{}, nil
}
func (stubInvitationService) ListByResourceAuthorized(context.Context, identity.Principal, string, string, crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	return crud.Page[invitation.Invitation]{}, nil
}
func (stubInvitationService) Mine(context.Context, string, crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	return crud.Page[invitation.Invitation]{}, nil
}
func (stubInvitationService) Accept(context.Context, invitationsvc.AcceptInput) (invitationsvc.AcceptResult, error) {
	return invitationsvc.AcceptResult{}, nil
}
func (stubInvitationService) Decline(context.Context, string, string) error { return nil }
func (stubInvitationService) Cancel(context.Context, string, string) error  { return nil }
func (stubInvitationService) Resend(context.Context, string, string, string) (invitation.Invitation, error) {
	return invitation.Invitation{}, nil
}

// spyInvitationService records whether each use-case was reached and mimics the
// real service's authorized operations: the host InviteCheck runs FIRST, and the
// *Called flags mark the side-effect path, so a test asserting "a denied create
// never touched the service" keeps its meaning now that the check lives in the
// service (the ordering itself is pinned by the invitationsvc tests).
// createResult is the configurable Create outcome the allowed-contract test
// asserts on.
type spyInvitationService struct {
	// inviteCheck is the host policy the authorized operations pose, mirroring the
	// real service. Nil authorizes.
	inviteCheck  invitationsvc.InviteCheck
	createCalled bool
	listCalled   bool
	mineCalled   bool
	acceptCalled bool
	cancelCalled bool
	resendCalled bool
	createResult invitationsvc.CreateResult
	// lastCreate is the most recent CreateInput the handler passed, so a test can
	// assert request fields (e.g. Metadata) reach the service verbatim.
	lastCreate invitationsvc.CreateInput
	// lastPrincipal is the most recent principal an authorized operation received,
	// so a test can assert the handler resolved and forwarded the caller.
	lastPrincipal identity.Principal
	// listPage / resendResult are the configurable list and resend outcomes the
	// response-shape tests assert the DTO mapping over.
	listPage     crud.Page[invitation.Invitation]
	resendResult invitation.Invitation
}

func (s *spyInvitationService) CreateAuthorized(ctx context.Context, principal identity.Principal, in invitationsvc.CreateInput) (invitationsvc.CreateResult, error) {
	s.lastPrincipal = principal
	if s.inviteCheck != nil {
		if err := s.inviteCheck(ctx, invitationsvc.InviteCheckRequest{
			Principal:      principal,
			Action:         invitationsvc.InviteCreate,
			ResourceType:   in.ResourceType,
			ResourceID:     in.ResourceID,
			Relation:       in.Relation,
			Metadata:       invitation.CloneMetadata(in.Metadata),
			Identifier:     in.Identifier,
			IdentifierKind: in.IdentifierKind,
		}); err != nil {
			return invitationsvc.CreateResult{}, err
		}
	}
	s.createCalled = true
	s.lastCreate = in
	return s.createResult, nil
}
func (s *spyInvitationService) ListByResourceAuthorized(ctx context.Context, principal identity.Principal, resourceType, resourceID string, _ crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	s.lastPrincipal = principal
	if s.inviteCheck != nil {
		if err := s.inviteCheck(ctx, invitationsvc.InviteCheckRequest{
			Principal:    principal,
			Action:       invitationsvc.InviteList,
			ResourceType: resourceType,
			ResourceID:   resourceID,
		}); err != nil {
			return crud.Page[invitation.Invitation]{}, err
		}
	}
	s.listCalled = true
	return s.listPage, nil
}
func (s *spyInvitationService) Mine(context.Context, string, crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	s.mineCalled = true
	return crud.Page[invitation.Invitation]{}, nil
}
func (s *spyInvitationService) Accept(context.Context, invitationsvc.AcceptInput) (invitationsvc.AcceptResult, error) {
	s.acceptCalled = true
	return invitationsvc.AcceptResult{}, nil
}
func (s *spyInvitationService) Decline(context.Context, string, string) error { return nil }
func (s *spyInvitationService) Cancel(context.Context, string, string) error {
	s.cancelCalled = true
	return nil
}
func (s *spyInvitationService) Resend(context.Context, string, string, string) (invitation.Invitation, error) {
	s.resendCalled = true
	return s.resendResult, nil
}

// newInvitationTestHandler mounts the routes with a wired InvitationService, so
// the invitation surface IS registered. The stub authorizes everything — the
// caller-reachability tests here never exercise the policy.
func newInvitationTestHandler(t *testing.T, inv InvitationService) http.Handler {
	t.Helper()
	users := newMemUsers()
	svc := authsvc.NewService(authsvc.Deps{
		Users:       users,
		Identifiers: newMemIdentifiers(users),
		Passwords:   &memPasswords{m: map[string]string{}},
		Sessions:    &memSessions{m: map[string]session.Session{}},
		Hasher:      fakeHasher{},
		Limiter:     ratelimiter.NewMemory(),
		Cookie:      authsvc.CookieConfig{},
		TokenSigner: newFakeSigner(),
	})
	h := web.NewWebHandler()
	Mount(h, svc, inv, crud.StrategyCursor, MutationSecurity{}, nil, nil)
	return h
}

// invitationFixture wires a real authsvc.Service over the mem stores with a spy
// invitation service, a configurable InviteCheck, and a configurable limiter, so a
// test can log in, revoke the session, and assert the policy/live-session gates.
// The check is carried by the spy service (as the real service carries it), never
// by the handlers.
type invitationFixture struct {
	h         http.Handler
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
	sessions  *memSessions
	inv       *spyInvitationService
}

func newInvitationFixture(t *testing.T, check invitationsvc.InviteCheck, limiter ratelimiter.Limiter) invitationFixture {
	t.Helper()
	return newInvitationFixtureWith(t, nil, check, limiter)
}

// newInvitationFixtureWith is newInvitationFixture with an explicit
// InvitationService: nil mounts the recording spy (fixture.inv), and a supplied
// service replaces it for tests that need service-side behavior (ownership).
func newInvitationFixtureWith(t *testing.T, inv InvitationService, check invitationsvc.InviteCheck, limiter ratelimiter.Limiter) invitationFixture {
	t.Helper()
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	spy := &spyInvitationService{inviteCheck: check}
	if inv == nil {
		inv = spy
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:       users,
		Identifiers: idents,
		Passwords:   passwords,
		Sessions:    sessions,
		Hasher:      fakeHasher{},
		Limiter:     limiter,
		Cookie:      authsvc.CookieConfig{},
		TokenSigner: newFakeSigner(),
	})
	h := web.NewWebHandler()
	Mount(h, svc, inv, crud.StrategyCursor, MutationSecurity{}, nil, nil)
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

// TestInvitationRoutesDenyByAbsence proves the whole invitation surface is NOT
// registered when no Granter (→ nil InvitationService) is wired: every route
// returns 404, never a 401 from a gated-but-registered route (design §6
// deny-by-absence — the recorded acceptance proof).
func TestInvitationRoutesDenyByAbsence(t *testing.T) {
	h := newTestHandler(t, nil) // Mount(..., nil) → invitations off
	routes := []struct{ method, path string }{
		{"POST", "/auth/invitations/project/p1"},
		{"GET", "/auth/invitations/project/p1"},
		{"GET", "/auth/invitations/mine"},
		{"POST", "/auth/invitations/accept"},
		{"POST", "/auth/invitations/inv-1/cancel"},
		{"POST", "/auth/invitations/inv-1/resend"},
		{"POST", "/auth/invitations/inv-1/decline"},
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
// §6/D3; the check-before-side-effect ordering itself is pinned in invitationsvc).
func TestInvitationCreateRelationDeniedNeverReachesService(t *testing.T) {
	// The policy authorizes creation but forbids the "owner" relation specifically —
	// proving it sees the validated payload, not just the route.
	relationAware := func(_ context.Context, req invitationsvc.InviteCheckRequest) error {
		if req.Action == invitationsvc.InviteCreate && req.Relation == "owner" {
			return fmt.Errorf("cannot invite an owner: %w", sdk.ErrForbidden)
		}
		return nil
	}
	f := newInvitationFixture(t, relationAware, nil)
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "POST", "/auth/invitations/project/p1",
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
	relationAware := func(_ context.Context, req invitationsvc.InviteCheckRequest) error {
		if req.Action == invitationsvc.InviteCreate && req.Relation == "owner" {
			return fmt.Errorf("cannot invite an owner: %w", sdk.ErrForbidden)
		}
		return nil
	}
	f := newInvitationFixture(t, relationAware, nil)
	f.inv.createResult = invitationsvc.CreateResult{Invitation: invitation.Invitation{
		ID: "inv-1", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "bob@example.com", Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}}
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "POST", "/auth/invitations/project/p1",
		`{"identifier":"bob@example.com","relation":"member"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("allowed create = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	if !f.inv.createCalled {
		t.Fatal("allowed create never reached the invitation service")
	}
	if want := (identity.Principal{Type: identity.User, ID: "u1"}); f.inv.lastPrincipal != want {
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
	denyList := func(_ context.Context, req invitationsvc.InviteCheckRequest) error {
		if req.Action == invitationsvc.InviteList {
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
	if want := (identity.Principal{Type: identity.User, ID: "u1"}); f.inv.lastPrincipal != want {
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
		rec := do(t, f.h, "POST", "/auth/invitations/project/p1", body)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("no-session create = %d, want 401; body=%s", rec.Code, rec.Body)
		}
		if f.inv.createCalled {
			t.Fatal("no-session create reached the invitation service")
		}
	})

	t.Run("policy denial", func(t *testing.T) {
		deny := func(context.Context, invitationsvc.InviteCheckRequest) error {
			return fmt.Errorf("denied: %w", sdk.ErrForbidden)
		}
		f := newInvitationFixture(t, deny, nil)
		f.seedLoginUser("u1", "alice@example.com")
		cookie := f.login(t, "alice@example.com")
		rec := do(t, f.h, "POST", "/auth/invitations/project/p1", body, cookie)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("denied create = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if f.inv.createCalled {
			t.Fatal("denied create reached the invitation service")
		}
	})

	t.Run("policy infrastructure error", func(t *testing.T) {
		boom := func(context.Context, invitationsvc.InviteCheckRequest) error {
			return errors.New("policy backend unreachable")
		}
		f := newInvitationFixture(t, boom, nil)
		f.seedLoginUser("u1", "alice@example.com")
		cookie := f.login(t, "alice@example.com")
		rec := do(t, f.h, "POST", "/auth/invitations/project/p1", body, cookie)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("policy-error create = %d, want 500; body=%s", rec.Code, rec.Body)
		}
		if f.inv.createCalled {
			t.Fatal("policy-error create reached the invitation service")
		}
	})
}

// TestInvitationRoutesRejectRevokedSession proves every authenticated invitation
// route rides RequireLiveSession: a revoked (deleted) session presenting an
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

// TestInvitationResponseCarriesInvitedBy proves the owner field is on EVERY
// response built from the invitation DTO mapper — create (201), resource list
// (200), and resend (200) — so a resource list can tell the rows the current admin
// owns from another admin's. It is an identifier, never the token: the response
// still carries no secret. The server enforces ownership on cancel/resend
// regardless of what a client renders (see TestInvitationCancelResend*).
func TestInvitationResponseCarriesInvitedBy(t *testing.T) {
	const owner = "u-owner"
	other := invitation.Invitation{
		ID: "inv-2", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "carol@example.com", InvitedBy: "u-other", Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	mine := invitation.Invitation{
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
		f.inv.createResult = invitationsvc.CreateResult{Invitation: mine}
		f.seedLoginUser(owner, "alice@example.com")
		cookie := f.login(t, "alice@example.com")

		rec := do(t, f.h, "POST", "/auth/invitations/project/p1",
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
		f.inv.listPage = crud.Page[invitation.Invitation]{Items: []invitation.Invitation{mine, other}}
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

		rec := do(t, f.h, "POST", "/auth/invitations/inv-1/resend", "", cookie)
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

// TestInvitationCancelResendOwnershipStaysServerEnforced proves the additive
// invited_by field changed nothing about authorization: cancel and resend still
// pass the SESSION caller's id to the service, which is the value ownership is
// enforced on. A non-owner caller is refused (403) even though the list response
// now tells every admin who owns a row; the field is a rendering hint, never
// authority.
func TestInvitationCancelResendOwnershipStaysServerEnforced(t *testing.T) {
	routes := []struct{ name, path string }{
		{"cancel", "/auth/invitations/inv-1/cancel"},
		{"resend", "/auth/invitations/inv-1/resend"},
	}
	for _, rt := range routes {
		t.Run(rt.name+"/owner", func(t *testing.T) {
			f := newInvitationFixtureWith(t, &ownerAwareInvitationService{owner: "u-owner"}, allowInviteCheck, nil)
			f.seedLoginUser("u-owner", "owner@example.com")
			rec := do(t, f.h, "POST", rt.path, "", f.login(t, "owner@example.com"))
			if rec.Code != http.StatusOK {
				t.Fatalf("owner %s = %d, want 200; body=%s", rt.name, rec.Code, rec.Body)
			}
		})
		t.Run(rt.name+"/non-owner", func(t *testing.T) {
			f := newInvitationFixtureWith(t, &ownerAwareInvitationService{owner: "u-owner"}, allowInviteCheck, nil)
			f.seedLoginUser("u-other", "other@example.com")
			rec := do(t, f.h, "POST", rt.path, "", f.login(t, "other@example.com"))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("non-owner %s = %d, want 403; body=%s", rt.name, rec.Code, rec.Body)
			}
		})
	}
}

// ownerAwareInvitationService enforces InvitedBy ownership like the real service,
// so a handler test can assert the session caller id (never a client-supplied
// value) is what cancel/resend authorize on.
type ownerAwareInvitationService struct {
	stubInvitationService
	owner string
}

func (s *ownerAwareInvitationService) Cancel(_ context.Context, _, currentUserID string) error {
	if currentUserID != s.owner {
		return fmt.Errorf("not the invitation owner: %w", sdk.ErrForbidden)
	}
	return nil
}

func (s *ownerAwareInvitationService) Resend(_ context.Context, _, currentUserID, _ string) (invitation.Invitation, error) {
	if currentUserID != s.owner {
		return invitation.Invitation{}, fmt.Errorf("not the invitation owner: %w", sdk.ErrForbidden)
	}
	return invitation.Invitation{ID: "inv-1", InvitedBy: s.owner, Status: "pending"}, nil
}

// TestInvitationCreateForwardsMetadata proves an optional metadata object on the
// create body reaches the authorized create operation's CreateInput verbatim —
// the service then validates it and shows it to the host policy (pinned in
// invitationsvc) — and that a created invitation carrying none omits the key.
func TestInvitationCreateForwardsMetadata(t *testing.T) {
	f := newInvitationFixture(t, allowInviteCheck, nil)
	f.seedLoginUser("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "POST", "/auth/invitations/project/p1",
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

	rec := do(t, f.h, "POST", "/auth/invitations/project/p1",
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

	rec := do(t, f.h, "POST", "/auth/invitations/project/p1", body, cookie)
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
	withMeta := invitation.Invitation{
		ID: "inv-1", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "bob@example.com", InvitedBy: owner, Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		Metadata: want,
	}

	t.Run("create", func(t *testing.T) {
		f := newInvitationFixture(t, allowInviteCheck, nil)
		f.inv.createResult = invitationsvc.CreateResult{Invitation: withMeta}
		f.seedLoginUser(owner, "alice@example.com")
		cookie := f.login(t, "alice@example.com")

		rec := do(t, f.h, "POST", "/auth/invitations/project/p1",
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
		f.inv.listPage = crud.Page[invitation.Invitation]{Items: []invitation.Invitation{withMeta}}
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

		rec := do(t, f.h, "POST", "/auth/invitations/inv-1/resend", "", cookie)
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
	f.inv.listPage = crud.Page[invitation.Invitation]{Items: []invitation.Invitation{{
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

func (metadataMineInvitationService) Mine(context.Context, string, crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	return crud.Page[invitation.Invitation]{Items: []invitation.Invitation{{
		ID: "inv-1", ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "bob@example.com", InvitedBy: "u-owner", Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		Metadata: map[string]string{"routing_key": "route-1"},
	}}}, nil
}

// compile-time proof the spy satisfies the consumed InvitationService port.
var _ InvitationService = (*spyInvitationService)(nil)

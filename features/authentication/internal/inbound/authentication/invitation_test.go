package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// allowInviteCheck authorizes every invitation operation. Wired where a test
// exercises the routes past the host policy (the create/list allowed contract and
// the revoked-session middleware gate, which denies before the handler body).
func allowInviteCheck(context.Context, invitationsvc.InviteCheckRequest) error { return nil }

// stubInvitationService satisfies InvitationService with inert methods — enough
// to prove the routes register when a Granter is wired.
type stubInvitationService struct{}

func (stubInvitationService) Create(context.Context, invitationsvc.CreateInput) (invitationsvc.CreateResult, error) {
	return invitationsvc.CreateResult{}, nil
}
func (stubInvitationService) ListByResource(context.Context, string, string, crud.ListRequest) (crud.Page[invitation.Invitation], error) {
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

// spyInvitationService records whether each use-case was reached, so a test can
// assert the InviteCheck gate ran BEFORE the service (a denied create/list must
// never touch the service). createResult is the configurable Create outcome the
// allowed-contract test asserts on.
type spyInvitationService struct {
	createCalled bool
	listCalled   bool
	mineCalled   bool
	acceptCalled bool
	cancelCalled bool
	resendCalled bool
	createResult invitationsvc.CreateResult
}

func (s *spyInvitationService) Create(context.Context, invitationsvc.CreateInput) (invitationsvc.CreateResult, error) {
	s.createCalled = true
	return s.createResult, nil
}
func (s *spyInvitationService) ListByResource(context.Context, string, string, crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	s.listCalled = true
	return crud.Page[invitation.Invitation]{}, nil
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
	return invitation.Invitation{}, nil
}

// newInvitationTestHandler mounts the routes with a wired InvitationService, so
// the invitation surface IS registered. The InviteCheck allows every operation —
// the caller-reachability tests here never exercise the policy.
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
	Mount(h, svc, inv, allowInviteCheck, crud.StrategyCursor, MutationSecurity{}, nil)
	return h
}

// invitationFixture wires a real authsvc.Service over the mem stores with a spy
// invitation service, a configurable InviteCheck, and a configurable limiter, so a
// test can log in, revoke the session, and assert the policy/live-session gates.
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
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	spy := &spyInvitationService{}
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
	Mount(h, svc, spy, check, crud.StrategyCursor, MutationSecurity{}, nil)
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
// runs the host InviteCheck with the EXACT requested relation BEFORE the service:
// a resource-allowed but relation-denied create (editor forbidden from inviting an
// owner) maps to 403 and the invitation service is never called (design §6/D3).
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
// service and returns the pending-invitation contract (201 + the DTO).
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
	var resp invitationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if resp.ID != "inv-1" || resp.Relation != "member" || resp.Status != "pending" {
		t.Fatalf("create DTO = %+v, want id=inv-1 relation=member status=pending", resp)
	}
}

// TestInvitationListDeniedNeverReachesService proves the resource-list handler runs
// the InviteCheck (Action list, empty relation) before the service: a denied list
// maps to 403 and never calls ListByResource (design §6/D3).
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
// service and returns 200 (the existing page contract).
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
}

// TestInvitationCreateFailsClosed proves the create handler fails CLOSED on every
// pre-service failure and never calls the invitation service: a missing principal
// (no session) is 401, a policy denial is 403, and a policy infrastructure error is
// 500 (design §6/D3 — an unexpected policy error never falls through to allow).
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

// compile-time proof the spy satisfies the consumed InvitationService port.
var _ InvitationService = (*spyInvitationService)(nil)

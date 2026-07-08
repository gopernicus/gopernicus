package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/gopernicus/gopernicus/features/auth/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/auth/internal/logic/invitationsvc"
	"github.com/gopernicus/gopernicus/features/auth/logic/invitation"
	"github.com/gopernicus/gopernicus/features/auth/logic/session"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
	"github.com/gopernicus/gopernicus/features/auth/logic/verification"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/web"
)

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

// newInvitationTestHandler mounts the routes with a wired InvitationService, so
// the invitation surface IS registered.
func newInvitationTestHandler(t *testing.T, inv InvitationService) http.Handler {
	t.Helper()
	svc := authsvc.NewService(authsvc.Deps{
		Users:     &memUsers{byID: map[string]user.User{}},
		Passwords: &memPasswords{m: map[string]string{}},
		Sessions:  &memSessions{m: map[string]session.Session{}},
		Codes:     &memCodes{m: map[string]verification.Code{}},
		Tokens:    &memTokens{m: map[string]verification.Token{}},
		Hasher:    fakeHasher{},
		Mailer:    nopMailer{},
		MailFrom:  "noreply@example.com",
		Limiter:   ratelimiter.NewMemory(),
		Cookie:    authsvc.CookieConfig{},
	})
	h := web.NewWebHandler()
	Mount(h, svc, inv)
	return h
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

package authenticationhttp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type policyInvitationRepo struct {
	invitations.InvitationRepository
	rows  []invitations.Invitation
	lists int
}

func (r *policyInvitationRepo) Create(_ context.Context, in invitations.Invitation) (invitations.Invitation, error) {
	r.rows = append(r.rows, in)
	return in, nil
}
func (r *policyInvitationRepo) ListByResource(context.Context, string, string, list.Request) (list.Page[invitations.Invitation], error) {
	r.lists++
	return list.Page[invitations.Invitation]{Items: r.rows}, nil
}

type policyGranter struct{ calls []invitations.GrantInput }

func (g *policyGranter) Grant(_ context.Context, in invitations.GrantInput) error {
	g.calls = append(g.calls, in)
	return nil
}

func TestInvitationHTTPPolicyChecksExactPreparedCommand(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, verdict := range []string{"allow", "deny", "error"} {
			t.Run(fmt.Sprintf("direct=%v/%s", direct, verdict), func(t *testing.T) {
				repo := &policyInvitationRepo{}
				granter := &policyGranter{}
				lookups := 0
				router, err := delivery.NewRouter(nopMailer{})
				if err != nil {
					t.Fatal(err)
				}
				inv, err := invitations.New(repo, granter, invitations.WithAccess(invitations.AccessConfig{UserLookup: func(_ context.Context, address string) (string, bool, error) {
					lookups++
					if address != "invitee@x.com" {
						t.Fatalf("lookup=%q", address)
					}
					return "verified-invitee", true, nil
				}}), invitations.WithDelivery(invitations.DeliveryConfig{Mailer: nopMailer{}, Deliver: router, Queue: stubQueue{}}))
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				check := func(_ context.Context, req InviteCheckRequest) error {
					calls++
					if len(repo.rows) != 0 || len(granter.calls) != 0 || lookups != 1 {
						t.Fatalf("effects before admission: rows=%d grants=%d lookups=%d", len(repo.rows), len(granter.calls), lookups)
					}
					if req.Principal.ID != "u1" || req.Action != InviteCreate || req.ResourceType != "project" || req.ResourceID != "p1" || req.Relation != "member" || req.Identifier != "invitee@x.com" || req.IdentifierKind != "email" || req.ResolvedSubjectID != "verified-invitee" || !maps.Equal(req.Metadata, map[string]string{"route": "original"}) {
						t.Fatalf("policy request=%+v", req)
					}
					req.Metadata["route"] = "changed"
					req.Relation = "owner"
					switch verdict {
					case "deny":
						return sdk.ErrForbidden
					case "error":
						return errors.New("policy unavailable")
					}
					return nil
				}
				f := newInvitationFixtureWith(t, inv, check, nil)
				f.seedLoginUser("u1", "inviter@x.com")
				cookie := f.login(t, "inviter@x.com")
				body := fmt.Sprintf(`{"identifier":" Invitee@X.com ","identifier_kind":" email ","relation":" member ","auto_accept":%v,"metadata":{"route":"original"}}`, direct)
				rec := do(t, f.h, "POST", "/auth/invitations/project/p1", body, cookie)
				want := http.StatusCreated
				if direct {
					want = http.StatusOK
				}
				if verdict == "deny" {
					want = http.StatusForbidden
				}
				if verdict == "error" {
					want = http.StatusInternalServerError
				}
				if rec.Code != want || calls != 1 || lookups != 1 {
					t.Fatalf("status=%d body=%s checks=%d lookups=%d", rec.Code, rec.Body, calls, lookups)
				}
				if verdict != "allow" {
					if len(repo.rows) != 0 || len(granter.calls) != 0 {
						t.Fatal("denied policy wrote/granted")
					}
					return
				}
				if direct {
					if len(granter.calls) != 1 || granter.calls[0].Relation != "member" || granter.calls[0].SubjectID != "verified-invitee" || granter.calls[0].Metadata["route"] != "original" {
						t.Fatalf("grant changed command=%+v", granter.calls)
					}
				} else {
					if len(repo.rows) != 1 || repo.rows[0].Relation != "member" || repo.rows[0].Identifier != "invitee@x.com" || repo.rows[0].Metadata["route"] != "original" {
						t.Fatalf("row changed command=%+v", repo.rows)
					}
				}
			})
		}
	}
}
func TestInvitationHTTPListPolicyPrecedesRepository(t *testing.T) {
	repo := &policyInvitationRepo{}
	inv, err := invitations.New(repo, &policyGranter{})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	f := newInvitationFixtureWith(t, inv, func(_ context.Context, req InviteCheckRequest) error {
		calls++
		if req.Action != InviteList || req.Principal.ID != "u1" || req.ResourceType != "project" || req.ResourceID != "p1" || req.Relation != "" || req.Identifier != "" || req.IdentifierKind != "" || req.ResolvedSubjectID != "" || len(req.Metadata) != 0 {
			t.Fatalf("list question=%+v", req)
		}
		return sdk.ErrForbidden
	}, nil)
	f.seedLoginUser("u1", "inviter@x.com")
	cookie := f.login(t, "inviter@x.com")
	rec := do(t, f.h, "GET", "/auth/invitations/project/p1", "", cookie)
	if rec.Code != http.StatusForbidden || calls != 1 || repo.lists != 0 {
		t.Fatalf("status=%d checks=%d reads=%d", rec.Code, calls, repo.lists)
	}
}

func TestHTTPPolicyOptionsRejectContradictoryWiring(t *testing.T) {
	base := newInvitationFixture(t, allowInviteCheck, nil)
	// The fixture's real service provides all baseline authentication collaborators.
	svc := newServiceWithFakes(authenticationFixture{Users: base.users, Identifiers: base.idents, Passwords: base.passwords, Sessions: base.sessions, Hasher: fakeHasher{}, TokenSigner: newFakeSigner()})
	for _, opts := range [][]Option{{WithInvitations(&stubInvitationService{})}, {WithInviteCheck(allowInviteCheck)}, {WithUserAdminCheck(func(context.Context, UserAdminCheckRequest) error { return nil })}} {
		if _, err := New(svc.Service, environment.ModeDevelopment, opts...); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("contradictory HTTP config accepted=%v", err)
		}
	}
	if _, err := New(svc.Service, environment.ModeDevelopment, WithInvitations(&stubInvitationService{}), WithInviteCheck(allowInviteCheck)); err != nil {
		t.Fatal(err)
	}
}

type policyAdminService struct {
	authService
	reads      int
	writes     int
	lastTarget string
	lastActor  sdk.Principal
	lastStatus user.Status
}

func (s *policyAdminService) CurrentUser(context.Context) (string, bool) {
	return "u1", true
}
func (s *policyAdminService) CurrentPrincipal(context.Context) (sdk.Principal, bool) {
	return sdk.Principal{Type: "service", ID: "operator"}, true
}
func (s *policyAdminService) GetUserSummary(_ context.Context, id string) (user.Summary, error) {
	s.reads++
	s.lastTarget = id
	return user.Summary{}, sdk.ErrNotFound
}
func (s *policyAdminService) ListUsers(context.Context, list.Request) (list.Page[user.Summary], error) {
	s.reads++
	return list.Page[user.Summary]{}, nil
}
func (s *policyAdminService) SetUserStatus(_ context.Context, actor sdk.Principal, id string, status user.Status) (user.Summary, user.StatusChange, error) {
	s.writes++
	s.lastTarget, s.lastActor, s.lastStatus = id, actor, status
	return user.Summary{}, user.StatusChange{}, nil
}

func TestUserAdminHTTPPolicyPrecedesTargetReadOrMutation(t *testing.T) {
	for _, action := range []UserAdminAction{UserAdminRead, UserAdminDeactivate} {
		for _, verdict := range []error{sdk.ErrForbidden, errors.New("policy failed"), nil} {
			t.Run(fmt.Sprintf("%s/%v", action, verdict), func(t *testing.T) {
				svc := &policyAdminService{}
				calls := 0
				h := &handlers{svc: svc, userAdminCheck: func(_ context.Context, req UserAdminCheckRequest) error {
					calls++
					if svc.reads != 0 || svc.writes != 0 || req.Principal != (sdk.Principal{Type: "service", ID: "operator"}) || req.Action != action || req.TargetUserID != "target-user" {
						t.Fatalf("wrong admission point=%+v reads=%d", req, svc.reads)
					}
					return verdict
				}}
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/auth/admin/users/target-user", strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				req.SetPathValue("id", "target-user")
				if action == UserAdminRead {
					req.Method = http.MethodGet
					h.adminGetUser(rec, req)
				} else {
					h.adminDeactivateUser(rec, req)
				}
				if calls != 1 {
					t.Fatalf("checks=%d writes=%d", calls, svc.writes)
				}
				if verdict != nil && (svc.reads != 0 || svc.writes != 0) {
					t.Fatal("refused policy resolved target")
				}
				if verdict == nil {
					if svc.lastTarget != "target-user" {
						t.Fatalf("executed target=%q", svc.lastTarget)
					}
					if action == UserAdminRead && (svc.reads != 1 || svc.writes != 0 || rec.Code != http.StatusNotFound) {
						t.Fatalf("admitted lookup=%d writes=%d status=%d", svc.reads, svc.writes, rec.Code)
					}
					if action == UserAdminDeactivate && (svc.reads != 0 || svc.writes != 1 || rec.Code != http.StatusOK || svc.lastActor != (sdk.Principal{Type: "service", ID: "operator"}) || svc.lastStatus != user.StatusDeactivated) {
						t.Fatalf("admitted mutation=%+v status=%d", svc, rec.Code)
					}
				}
			})
		}
	}
}

func TestHTTPPolicyFailsClosedWhenMissingOrCanceled(t *testing.T) {
	for _, operation := range []string{"invite-create", "invite-list", "admin-read", "admin-deactivate"} {
		for _, cancelInPolicy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/canceled=%v", operation, cancelInPolicy), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				svc := &policyAdminService{}
				inv := &spyInvitationService{}
				h := &handlers{svc: svc, inv: inv, listStrategy: list.StrategyCursor}
				checks := 0
				if cancelInPolicy {
					h.inviteCheck = func(context.Context, InviteCheckRequest) error { checks++; cancel(); return nil }
					h.userAdminCheck = func(context.Context, UserAdminCheckRequest) error { checks++; cancel(); return nil }
				}
				body := `{}`
				if operation == "invite-create" {
					body = `{"identifier":"invitee@x.com","relation":"member"}`
				}
				req := httptest.NewRequest("POST", "/", strings.NewReader(body)).WithContext(ctx)
				req.Header.Set("Content-Type", "application/json")
				req.SetPathValue("resource_type", "project")
				req.SetPathValue("resource_id", "p1")
				req.SetPathValue("id", "target-user")
				rec := httptest.NewRecorder()
				switch operation {
				case "invite-create":
					h.createInvitation(rec, req)
				case "invite-list":
					req.Method = http.MethodGet
					h.listResourceInvitations(rec, req)
				case "admin-read":
					req.Method = http.MethodGet
					h.adminGetUser(rec, req)
				case "admin-deactivate":
					h.adminDeactivateUser(rec, req)
				}
				if rec.Code < 400 || inv.createCalled || inv.listCalled || svc.reads != 0 || svc.writes != 0 {
					t.Fatalf("policy admitted: status=%d create=%v list=%v reads=%d writes=%d", rec.Code, inv.createCalled, inv.listCalled, svc.reads, svc.writes)
				}
				if cancelInPolicy && (checks != 1 || !errors.Is(ctx.Err(), context.Canceled)) {
					t.Fatalf("policy calls=%d cancellation=%v", checks, ctx.Err())
				}
				if !cancelInPolicy && (checks != 0 || rec.Code != http.StatusForbidden) {
					t.Fatalf("missing policy: calls=%d status=%d", checks, rec.Code)
				}
			})
		}
	}
}

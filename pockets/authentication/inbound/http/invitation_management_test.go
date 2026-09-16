package authenticationhttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
)

type invitationManagementRepo struct {
	invitations.InvitationRepository
	row           invitations.Invitation
	reads, writes int
	readError     error
}

func (s *invitationManagementRepo) Get(context.Context, string) (invitations.Invitation, error) {
	s.reads++
	return s.row, s.readError
}
func (s *invitationManagementRepo) UpdateStatus(_ context.Context, id string, upd invitations.StatusUpdate) (invitations.Invitation, error) {
	if id != s.row.ID || upd.ExpectedTokenHash != s.row.TokenHash {
		return invitations.Invitation{}, sdk.ErrConflict
	}
	s.writes++
	s.row.Status, s.row.TokenHash, s.row.ExpiresAt = upd.Status, upd.TokenHash, upd.ExpiresAt
	return s.row, nil
}

type managementDelivery struct {
	stubQueue
	replacements int
}

func (q *managementDelivery) Replace(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	q.replacements++
	return q.stubQueue.Replace(ctx, cmd)
}

func TestInvitationManagementHTTPAdmissionBeforeWritesOrDelivery(t *testing.T) {
	for _, operation := range []string{"cancel", "resend"} {
		for _, scenario := range []string{"issuer", "other-user", "empty-issuer", "wrong-target", "lookup-error"} {
			t.Run(operation+"/"+scenario, func(t *testing.T) {
				repo := &invitationManagementRepo{row: invitations.Invitation{ID: "inv-1", InvitedBy: "u1", ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "invitee@x.com", IdentifierKind: "email", TokenHash: "initial", Status: invitations.StatusPending, ExpiresAt: time.Now().Add(time.Hour)}}
				caller, want := "u1", http.StatusOK
				switch scenario {
				case "other-user":
					caller, want = "u2", http.StatusForbidden
				case "empty-issuer":
					repo.row.InvitedBy, want = "", http.StatusForbidden
				case "wrong-target":
					repo.row.ID, want = "different-target", http.StatusBadRequest
				case "lookup-error":
					repo.readError, want = sdk.ErrUnavailable, http.StatusServiceUnavailable
				}
				queue := &managementDelivery{}
				router, err := delivery.NewRouter(nopMailer{})
				if err != nil {
					t.Fatal(err)
				}
				inv, err := invitations.New(repo, &policyGranter{}, invitations.WithDelivery(invitations.DeliveryConfig{Mailer: nopMailer{}, Deliver: router, Queue: queue}))
				if err != nil {
					t.Fatal(err)
				}
				f := newInvitationFixtureWith(t, inv, allowInviteCheck, nil)
				f.seedLoginUser(caller, "caller@x.com")
				rec := do(t, f.h, "POST", "/auth/invitations/inv-1/"+operation, "", f.login(t, "caller@x.com"))
				if rec.Code != want || repo.reads != 1 {
					t.Fatalf("status=%d want=%d reads=%d body=%s", rec.Code, want, repo.reads, rec.Body)
				}
				if scenario != "issuer" {
					if repo.writes != 0 || queue.replacements != 0 || repo.row.TokenHash != "initial" {
						t.Fatalf("denied effects: writes=%d deliveries=%d row=%+v", repo.writes, queue.replacements, repo.row)
					}
					return
				}
				if repo.writes != 1 {
					t.Fatalf("admitted writes=%d", repo.writes)
				}
				if operation == "resend" && (queue.replacements != 1 || repo.row.TokenHash == "initial") {
					t.Fatalf("resend deliveries=%d token=%s", queue.replacements, repo.row.TokenHash)
				}
				if operation == "cancel" && (queue.replacements != 0 || repo.row.Status != invitations.StatusCancelled) {
					t.Fatalf("cancel deliveries=%d status=%s", queue.replacements, repo.row.Status)
				}
			})
		}
	}
}

type managementPreparationOverride struct {
	spyInvitationService
	prepare func(context.Context, string) (invitations.PreparedManagement, error)
}

func (s *managementPreparationOverride) PrepareManagement(ctx context.Context, id string) (invitations.PreparedManagement, error) {
	return s.prepare(ctx, id)
}

func TestInvitationManagementRejectsInvalidOrCanceledPreparation(t *testing.T) {
	for _, operation := range []string{"cancel", "resend"} {
		for _, scenario := range []string{"zero", "different-target", "canceled"} {
			t.Run(fmt.Sprintf("%s/%s", operation, scenario), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				inv := &managementPreparationOverride{prepare: func(ctx context.Context, id string) (invitations.PreparedManagement, error) {
					if scenario == "zero" {
						return invitations.PreparedManagement{}, nil
					}
					if scenario == "different-target" {
						id = "another-invitation"
					}
					p, err := preparedHTTPManagement(ctx, id, "u1")
					if scenario == "canceled" {
						cancel()
					}
					return p, err
				}}
				h := &handlers{svc: &policyAdminService{}, inv: inv}
				req := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
				req.SetPathValue("id", "inv-1")
				rec := httptest.NewRecorder()
				if operation == "cancel" {
					h.cancelInvitation(rec, req)
				} else {
					h.resendInvitation(rec, req)
				}
				if rec.Code < 400 || inv.cancelCalled || inv.resendCalled {
					t.Fatalf("invalid preparation admitted: status=%d cancel=%v resend=%v", rec.Code, inv.cancelCalled, inv.resendCalled)
				}
			})
		}
	}
}

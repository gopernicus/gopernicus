package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// The failure occurs after the host's real grant commits and before authentication
// finalizes it. A normal completed acceptance would bypass the Granter on retry.
type failInvitationCompletionOnce struct {
	invitations.InvitationRepository
	mu     sync.Mutex
	failed bool
}

func (r *failInvitationCompletionOnce) CompleteAcceptance(ctx context.Context, id string, claim invitations.Acceptance) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.failed {
		r.failed = true
		return invitations.Invitation{}, errors.New("injected invitation completion failure")
	}
	return r.InvitationRepository.CompleteAcceptance(ctx, id, claim)
}

func TestInvitationCommittedGrantRecoveryThroughHTTP(t *testing.T) {
	components := hostAuthz(t)
	g, _ := hostGranter(components.Mutations, resourceKey(demoResourceType, demoResourceID))
	sender := &recordingSender{}
	cfg, err := buildAuthConfig(quietLog(), g)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.Mailer = sender
	cfg.InviteCheck = hostInviteCheck(components.Decisions)
	repos := authmem.New().Repositories()
	repos.Invitations = &failInvitationCompletionOnce{InvitationRepository: repos.Invitations}
	svc, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatal(err)
	}
	stop := runDelivery(t, svc)
	t.Cleanup(stop)
	srv := httptest.NewServer(mountInProcess(t, svc))
	t.Cleanup(srv.Close)
	host := &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: hostAllowedOrigins(t)[0]}
	manager := host.signUp("ledger-manager@example.com")
	invitee := host.signUp("ledger-invitee@example.com")
	ids := &machineHost{linkHost: host, system: components.Mutations}
	managerID, inviteeID := ids.currentUserID(manager), ids.currentUserID(invitee)
	seedTrustedOwner(t, components.Mutations, demoResourceID, managerID)

	before := sender.count()
	resp, body := manager.mutate("/auth/invitations/project/demo", `{"identifier":"ledger-invitee@example.com","relation":"member","auto_accept":false}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invitation = %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("decode invitation %s: %v", body, err)
	}
	tokenPattern := regexp.MustCompile(`[?&]token=([A-Za-z0-9._~-]+)`)
	var token string
	deadline := time.Now().Add(5 * time.Second)
	for token == "" && time.Now().Before(deadline) {
		for _, msg := range sender.all()[before:] {
			if addressedTo(msg.To, "ledger-invitee@example.com") {
				if match := tokenPattern.FindStringSubmatch(msg.Text + " " + msg.HTML); len(match) == 2 {
					token = match[1]
				}
			}
		}
		if token == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if token == "" {
		t.Fatal("invitation token was not delivered")
	}
	acceptBody := `{"token":"` + token + `"}`
	resp, body = invitee.mutate("/auth/invitations/accept", acceptBody)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("fault-injected accept = %d: %s", resp.StatusCode, body)
	}
	if !allowed(t, components, inviteeID, demoPermission, demoResourceID) {
		t.Fatal("grant did not commit before completion failure")
	}
	inv, err := repos.Invitations.Get(context.Background(), created.ID)
	if err != nil || inv.Status != invitations.StatusAccepting {
		t.Fatalf("failed completion: invitation=%+v err=%v", inv, err)
	}
	if _, err := admitRevokeRelationship(context.Background(), components, actor(managerID), mutations.RevokeRelationshipCommand{
		ResourceType: demoResourceType, ResourceID: demoResourceID, Relation: demoRelation,
		Subject: relationships.SubjectRef{Type: "user", ID: inviteeID},
	}); err != nil {
		t.Fatal(err)
	}
	resp, body = invitee.mutate("/auth/invitations/accept", acceptBody)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("superseded accept retry = %d: %s", resp.StatusCode, body)
	}
	if allowed(t, components, inviteeID, demoPermission, demoResourceID) {
		t.Fatal("accept retry restored revoked access")
	}
	resp, body = manager.do(http.MethodGet, "/auth/invitations/project/demo", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list invitations = %d: %s", resp.StatusCode, body)
	}
	var page struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != created.ID || page.Items[0].Status != invitations.StatusAccepted {
		t.Fatalf("accepted invitation listing = %s: %v", body, err)
	}
	// Later accepted-token calls retain the existing successful short-circuit.
	resp, body = invitee.mutate("/auth/invitations/accept", acceptBody)
	if resp.StatusCode != http.StatusOK || allowed(t, components, inviteeID, demoPermission, demoResourceID) {
		t.Fatalf("accepted-token retry = %d: %s; access must remain revoked", resp.StatusCode, body)
	}
}

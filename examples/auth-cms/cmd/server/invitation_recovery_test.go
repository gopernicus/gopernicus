package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
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

// pausingGranter holds a Grant after the claim committed and before the host
// Granter runs, so a cancel can race the first grant.
type pausingGranter struct {
	inner   invitations.Granter
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *pausingGranter) Grant(ctx context.Context, in invitations.GrantInput) error {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return g.inner.Grant(ctx, in)
}

type settlementHost struct {
	components authorization.Components
	registry   *hostResourceRegistry
	granter    integrityRelationshipGranter
	repos      auth.Repositories
	host       *linkHost
	manager    *linkClient
	invitee    *linkClient
	inviteeID  string
}

// newSettlementHost boots the invitation surface the way run() wires it: the
// ledger-backed Granter and the Fencer over one authorization store.
func newSettlementHost(t *testing.T, wrapGranter func(invitations.Granter) invitations.Granter, wrapRepo func(invitations.InvitationRepository) invitations.InvitationRepository) *settlementHost {
	t.Helper()
	components := hostAuthz(t)
	g, registry := hostGranter(components.Mutations, resourceKey(demoResourceType, demoResourceID))
	var granter invitations.Granter = g
	if wrapGranter != nil {
		granter = wrapGranter(g)
	}
	sender := &recordingSender{}
	cfg, err := buildAuthConfig(quietLog(), granter)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.Mailer = sender
	cfg.InviteCheck = hostInviteCheck(components.Decisions)
	cfg.Fencer = integrityOperationFencer{system: components.Mutations}
	repos := authmem.New().Repositories()
	if wrapRepo != nil {
		repos.Invitations = wrapRepo(repos.Invitations)
	}
	svc, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatal(err)
	}
	stop := runDelivery(t, svc)
	t.Cleanup(stop)
	srv := httptest.NewServer(mountInProcess(t, svc))
	t.Cleanup(srv.Close)
	host := &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: hostAllowedOrigins(t)[0]}
	manager := host.signUp("settle-manager@example.com")
	invitee := host.signUp("settle-invitee@example.com")
	ids := &machineHost{linkHost: host, system: components.Mutations}
	seedTrustedOwner(t, components.Mutations, demoResourceID, ids.currentUserID(manager))
	return &settlementHost{
		components: components, registry: registry, granter: g, repos: repos, host: host,
		manager: manager, invitee: invitee, inviteeID: ids.currentUserID(invitee),
	}
}

// invite creates a member invitation for the invitee and returns its id and token.
func (h *settlementHost) invite(t *testing.T) (string, string) {
	t.Helper()
	before := h.host.sender.count()
	resp, body := h.manager.mutate("/auth/invitations/project/demo", `{"identifier":"settle-invitee@example.com","relation":"member","auto_accept":false}`)
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
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, msg := range h.host.sender.all()[before:] {
			if addressedTo(msg.To, "settle-invitee@example.com") {
				if match := tokenPattern.FindStringSubmatch(msg.Text + " " + msg.HTML); len(match) == 2 {
					return created.ID, match[1]
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("invitation token was not delivered")
	return "", ""
}

func (h *settlementHost) status(t *testing.T, id string) string {
	t.Helper()
	inv, err := h.repos.Invitations.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get invitation %s: %v", id, err)
	}
	return inv.Status
}

func (h *settlementHost) cancel(id string) (*http.Response, []byte) {
	return h.manager.mutate("/auth/invitations/"+id+"/cancel", "")
}

func (h *settlementHost) restoreResource() {
	h.registry.mu.Lock()
	defer h.registry.mu.Unlock()
	h.registry.live[resourceKey(demoResourceType, demoResourceID)] = struct{}{}
}

func TestInvitationCancelDuringPausedGrantThroughHTTP(t *testing.T) {
	pause := &pausingGranter{entered: make(chan struct{}), release: make(chan struct{})}
	h := newSettlementHost(t, func(g invitations.Granter) invitations.Granter {
		pause.inner = g
		return pause
	}, nil)
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(pause.release) }) }
	t.Cleanup(resume)
	id, token := h.invite(t)
	acceptBody := `{"token":"` + token + `"}`

	csrf := h.invitee.csrfToken()
	type outcome struct {
		status int
		body   string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, h.host.srv.URL+"/auth/invitations/accept", strings.NewReader(acceptBody))
		if err != nil {
			done <- outcome{err: err}
			return
		}
		req.Header.Set("Origin", h.host.origin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := h.invitee.http.Do(req)
		if err != nil {
			done <- outcome{err: err}
			return
		}
		defer resp.Body.Close()
		payload, err := io.ReadAll(resp.Body)
		done <- outcome{status: resp.StatusCode, body: string(payload), err: err}
	}()
	select {
	case <-pause.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("accept never reached the Granter")
	}
	if got := h.status(t, id); got != invitations.StatusAccepting {
		t.Fatalf("paused claim status = %q, want accepting", got)
	}

	resp, body := h.cancel(id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel accepting claim = %d: %s", resp.StatusCode, body)
	}
	if got := h.status(t, id); got != invitations.StatusCancelled {
		t.Fatalf("after cancel status = %q, want cancelled", got)
	}

	resume()
	var paused outcome
	select {
	case paused = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("paused accept never returned")
	}
	if paused.err != nil {
		t.Fatal(paused.err)
	}
	if paused.status != http.StatusConflict {
		t.Fatalf("resumed accept = %d: %s, want 409", paused.status, paused.body)
	}
	if allowed(t, h.components, h.inviteeID, demoPermission, demoResourceID) {
		t.Fatal("resumed grant committed after cancel")
	}
	if got := h.status(t, id); got != invitations.StatusCancelled {
		t.Fatalf("after resumed grant status = %q, want cancelled", got)
	}
	outcomeRecorded, recorded, err := h.components.Mutations.Recorded(context.Background(), invitationOperationID(id))
	if err != nil || !recorded || outcomeRecorded != mutations.OutcomeFenced {
		t.Fatalf("ledger = %q recorded=%v err=%v, want fenced", outcomeRecorded, recorded, err)
	}
	resp, body = h.invitee.mutate("/auth/invitations/accept", acceptBody)
	if resp.StatusCode == http.StatusOK || allowed(t, h.components, h.inviteeID, demoPermission, demoResourceID) {
		t.Fatalf("late accept = %d: %s; access must stay absent", resp.StatusCode, body)
	}
}

func TestInvitationRecordedOperationSettlesAfterResourceDeleted(t *testing.T) {
	h := newSettlementHost(t, nil, func(r invitations.InvitationRepository) invitations.InvitationRepository {
		return &failInvitationCompletionOnce{InvitationRepository: r}
	})
	id, token := h.invite(t)
	acceptBody := `{"token":"` + token + `"}`

	resp, body := h.invitee.mutate("/auth/invitations/accept", acceptBody)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("fault-injected accept = %d: %s", resp.StatusCode, body)
	}
	if got := h.status(t, id); got != invitations.StatusAccepting {
		t.Fatalf("failed completion status = %q, want accepting", got)
	}
	h.registry.remove(demoResourceType, demoResourceID)

	// A fresh operation is still refused by the existence probe.
	if err := h.granter.Grant(context.Background(), demoGrant("never-recorded", demoRelation, h.inviteeID)); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("unrecorded grant against deleted resource = %v, want not found", err)
	}

	resp, body = h.invitee.mutate("/auth/invitations/accept", acceptBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recorded retry after resource deletion = %d: %s, want 200", resp.StatusCode, body)
	}
	if got := h.status(t, id); got != invitations.StatusAccepted {
		t.Fatalf("settled status = %q, want accepted", got)
	}
}

func TestFencedInvitationNotRevivedByResourceRestore(t *testing.T) {
	h := newSettlementHost(t, nil, nil)
	id, token := h.invite(t)
	acceptBody := `{"token":"` + token + `"}`

	h.registry.remove(demoResourceType, demoResourceID)
	resp, body := h.invitee.mutate("/auth/invitations/accept", acceptBody)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("accept against deleted resource = %d: %s, want 404", resp.StatusCode, body)
	}
	if got := h.status(t, id); got != invitations.StatusAccepting {
		t.Fatalf("refused first grant status = %q, want accepting", got)
	}

	resp, body = h.cancel(id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel accepting claim = %d: %s", resp.StatusCode, body)
	}
	h.restoreResource()

	resp, body = h.invitee.mutate("/auth/invitations/accept", acceptBody)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("accept after restore = %d: %s; a fenced invitation must stay dead", resp.StatusCode, body)
	}
	if err := h.granter.Grant(context.Background(), demoGrant(id, demoRelation, h.inviteeID)); !errors.Is(err, invitations.ErrGrantFenced) {
		t.Fatalf("direct grant after restore = %v, want ErrGrantFenced", err)
	}
	if allowed(t, h.components, h.inviteeID, demoPermission, demoResourceID) {
		t.Fatal("resource restore revived a fenced invitation")
	}
	if got := h.status(t, id); got != invitations.StatusCancelled {
		t.Fatalf("final status = %q, want cancelled", got)
	}
}

func TestFencerNamespaceMatchesGranter(t *testing.T) {
	components := hostAuthz(t)
	seedTrustedOwner(t, components.Mutations, demoResourceID, "namespace-owner")
	g, _ := hostGranter(components.Mutations, resourceKey(demoResourceType, demoResourceID))
	f := integrityOperationFencer{system: components.Mutations}
	ctx := context.Background()

	for range 2 {
		got, err := f.Fence(ctx, invitations.FenceInput{OperationID: "fenced-first"})
		if err != nil || got != invitations.FenceOutcomeFenced {
			t.Fatalf("Fence = %q, %v; want fenced", got, err)
		}
	}
	if err := g.Grant(ctx, demoGrant("fenced-first", demoRelation, "namespace-invitee")); !errors.Is(err, invitations.ErrGrantFenced) {
		t.Fatalf("grant after fence = %v, want ErrGrantFenced", err)
	}
	if allowed(t, components, "namespace-invitee", demoPermission, demoResourceID) {
		t.Fatal("fenced grant wrote access")
	}

	if err := g.Grant(ctx, demoGrant("granted-first", demoRelation, "namespace-member")); err != nil {
		t.Fatal(err)
	}
	got, err := f.Fence(ctx, invitations.FenceInput{OperationID: "granted-first"})
	if err != nil || got != invitations.FenceOutcomeCommitted {
		t.Fatalf("Fence after grant = %q, %v; want committed", got, err)
	}
}

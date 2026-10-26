package invitations

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// multiIdentifier lists user-9 as holding a primary email, a second verified
// email and a verified phone.
func multiIdentifier(_ context.Context, userID string) ([]sdk.IdentityAddress, error) {
	if userID != "user-9" {
		return nil, nil
	}
	return []sdk.IdentityAddress{
		{Kind: sdk.AddressKindEmail, Value: "primary@x.com"},
		{Kind: sdk.AddressKindEmail, Value: "second@x.com"},
		{Kind: sdk.AddressKindPhone, Value: "+15550100"},
	}, nil
}

// grantRows returns the invitation_granted rows recorded with status.
func grantRows(events *fakeSecurityEvents, status string) []securityevent.SecurityEvent {
	var rows []securityevent.SecurityEvent
	for _, evt := range events.created {
		if evt.EventType == securityevent.TypeInvitationGranted && evt.EventStatus == status {
			rows = append(rows, evt)
		}
	}
	return rows
}

// TestInviteLinkCarriesTokenInFragment pins the default link shape: the token
// rides the URL fragment of the destination, never a query parameter, and the
// configured InvitationURL replaces the relative "/" fallback.
func TestInviteLinkCarriesTokenInFragment(t *testing.T) {
	for _, tc := range []struct {
		name, invitationURL, wantPrefix string
	}{
		{"same-origin fallback", "", "/#token="},
		{"invitation url", "https://app.example.com/invite", "https://app.example.com/invite#token="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := &recordingHook{}
			svc := newSvc(t, newFakeInvRepo(), &fakeGranter{}, constructorConfig{UserLookup: unknownUser})
			svc.invitationURL = tc.invitationURL
			wireSyncDeliveryHook(t, svc, &recordingMailer{}, nil, hook.hook)
			if _, err := svc.Create(context.Background(), CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "invitee@x.com", InvitedBy: "inviter"}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			link := hook.only(t, delivery.PurposeInvitation).data["Link"].(string)
			if !strings.HasPrefix(link, tc.wantPrefix) || len(link) != len(tc.wantPrefix)+tokenSecretLen {
				t.Errorf("Link = %q, want %s<token>", link, tc.wantPrefix)
			}
			if strings.Contains(link, "?token=") || strings.Contains(link, "&token=") {
				t.Errorf("Link carries the token in its query: %q", link)
			}
		})
	}
}

// TestInviteLinkTokenInQueryHatch is the one test pinned to the one-release
// hatch: WithLinkTokenInQuery restores the "?token=" join, preserving an
// existing query.
func TestInviteLinkTokenInQueryHatch(t *testing.T) {
	hook := &recordingHook{}
	svc := newSvc(t, newFakeInvRepo(), &fakeGranter{}, constructorConfig{UserLookup: unknownUser})
	svc.invitationURL = "https://app.example.com/invite?from=mail"
	svc.linkTokenInQuery = true
	wireSyncDeliveryHook(t, svc, &recordingMailer{}, nil, hook.hook)
	if _, err := svc.Create(context.Background(), CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "invitee@x.com", InvitedBy: "inviter"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	link := hook.only(t, delivery.PurposeInvitation).data["Link"].(string)
	if !strings.HasPrefix(link, "https://app.example.com/invite?from=mail&token=") || strings.Contains(link, "#") {
		t.Errorf("Link = %q, want the query hatch shape", link)
	}
}

// TestRedirectWithFragmentRefused proves a requested redirect carrying "#" is
// refused at Create and Resend before anything persists or rotates.
func TestRedirectWithFragmentRefused(t *testing.T) {
	repo := newFakeInvRepo()
	svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{UserLookup: unknownUser})
	svc.redirects = redirect.New([]string{"https://x.example/#a"})
	_, err := svc.Create(context.Background(), CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "invitee@x.com", InvitedBy: "inviter", Redirect: "https://x.example/#a"})
	if !errors.Is(err, ErrRedirectFragment) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Create err = %v, want ErrRedirectFragment", err)
	}
	if len(repo.byID) != 0 {
		t.Fatalf("a refused create persisted %d rows", len(repo.byID))
	}
	inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "inviter", "secret-r", false, time.Now().Add(time.Hour))
	if _, err := svc.Resend(context.Background(), prepareManagement(t, svc, inv.ID), "https://x.example/#a"); !errors.Is(err, ErrRedirectFragment) {
		t.Fatalf("Resend err = %v, want ErrRedirectFragment", err)
	}
	if got, _ := repo.Get(context.Background(), inv.ID); got.TokenHash != inv.TokenHash {
		t.Error("a refused resend rotated the token")
	}
}

// TestResolveInvitationsAnyVerifiedIdentifier proves resolution is kind
// generic and matches every verified identifier, primary or not, recording
// resolved_by=verification with the invitation and operation ids.
func TestResolveInvitationsAnyVerifiedIdentifier(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	events := &fakeSecurityEvents{}
	svc := newSvc(t, repo, granter, constructorConfig{VerifiedIdentifiers: multiIdentifier, SecurityEvents: events,
		BodySenders: map[string]delivery.BodySender{sdk.AddressKindPhone: &fakeNotifier{}}})
	future := time.Now().Add(time.Hour)
	second := seedInvite(t, repo, "project", "A", "member", "second@x.com", "inviter", "s-a", true, future)
	phone := seedInviteKind(t, repo, "project", "B", "member", "+15550100", sdk.AddressKindPhone, "inviter", "s-b", true, future)
	manual := seedInvite(t, repo, "project", "C", "member", "second@x.com", "inviter", "s-c", false, future)

	if n, err := svc.ResolveInvitations(context.Background(), sdk.AddressKindEmail, "Second@X.com", "user", "user-9"); err != nil || n != 1 {
		t.Fatalf("resolve email = %d, %v; want 1", n, err)
	}
	if n, err := svc.ResolveInvitations(context.Background(), sdk.AddressKindPhone, "+15550100", "user", "user-9"); err != nil || n != 1 {
		t.Fatalf("resolve phone = %d, %v; want 1", n, err)
	}
	if n, err := svc.ResolveInvitations(context.Background(), sdk.AddressKindEmail, "unheld@x.com", "user", "user-9"); err != nil || n != 0 {
		t.Fatalf("resolve unheld = %d, %v; want 0", n, err)
	}
	for _, id := range []string{second.ID, phone.ID} {
		if got, _ := repo.Get(context.Background(), id); got.Status != StatusAccepted || got.ResolvedSubjectID != "user-9" {
			t.Errorf("invitation %s = %+v, want accepted by user-9", id, got)
		}
	}
	if got, _ := repo.Get(context.Background(), manual.ID); got.Status != StatusPending {
		t.Errorf("a non-AutoAccept invitation resolved: %+v", got)
	}
	rows := grantRows(events, securityevent.StatusSuccess)
	if len(rows) != 2 {
		t.Fatalf("granted rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if row.Details["resolved_by"] != "verification" || row.Details["invitation_id"] == nil || row.Details["operation_id"] != row.Details["invitation_id"] {
			t.Errorf("resolution provenance = %+v", row.Details)
		}
		if _, ok := row.Details["direct_add"]; ok {
			t.Errorf("a resolution row claims direct_add: %+v", row.Details)
		}
	}
}

// TestResolvePendingInvitationsSkipsAccepting proves the on-demand sync walks
// pending rows only: an accepting claim is left for a verification to
// re-drive, and a repeat sync resolves nothing.
func TestResolvePendingInvitationsSkipsAccepting(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{failOn: "A"}
	events := &fakeSecurityEvents{}
	svc := newSvc(t, repo, granter, constructorConfig{SecurityEvents: events})
	future := time.Now().Add(time.Hour)
	stuck := seedInvite(t, repo, "project", "A", "member", "invitee@x.com", "inviter", "s-a", true, future)
	if n, _ := svc.ResolveInvitations(context.Background(), sdk.AddressKindEmail, "invitee@x.com", "user", "user-9"); n != 0 {
		t.Fatalf("failing grant resolved %d", n)
	}
	if got, _ := repo.Get(context.Background(), stuck.ID); got.Status != StatusAccepting {
		t.Fatalf("stuck claim = %+v, want accepting", got)
	}
	granter.failOn = ""
	fresh := seedInvite(t, repo, "project", "B", "member", "invitee@x.com", "inviter", "s-b", true, future)
	attempts := len(granter.calls)

	if n, err := svc.ResolvePendingInvitations(context.Background(), sdk.AddressKindEmail, "invitee@x.com", "user", "user-9"); err != nil || n != 1 {
		t.Fatalf("sync = %d, %v; want 1", n, err)
	}
	if len(granter.calls) != attempts+1 || granter.calls[len(granter.calls)-1].resourceID != "B" {
		t.Errorf("sync re-drove the accepting claim: %+v", granter.calls[attempts:])
	}
	if got, _ := repo.Get(context.Background(), stuck.ID); got.Status != StatusAccepting {
		t.Errorf("sync touched the accepting claim: %+v", got)
	}
	if got, _ := repo.Get(context.Background(), fresh.ID); got.Status != StatusAccepted {
		t.Errorf("sync did not accept the pending row: %+v", got)
	}
	if n, err := svc.ResolvePendingInvitations(context.Background(), sdk.AddressKindEmail, "invitee@x.com", "user", "user-9"); err != nil || n != 0 {
		t.Errorf("repeat sync = %d, %v; want 0", n, err)
	}
	var sync int
	for _, row := range grantRows(events, securityevent.StatusSuccess) {
		if row.Details["resolved_by"] == "sync" {
			sync++
		}
	}
	if sync != 1 {
		t.Errorf("resolved_by=sync rows = %d, want 1", sync)
	}
}

// TestAcceptMatchesNonPrimaryVerifiedEmail proves the accept-time match is
// membership over every verified identifier: an invitation addressed to a
// second, non-primary verified email accepts, recording resolved_by=accept.
func TestAcceptMatchesNonPrimaryVerifiedEmail(t *testing.T) {
	repo := newFakeInvRepo()
	events := &fakeSecurityEvents{}
	svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{VerifiedIdentifiers: multiIdentifier, SecurityEvents: events})
	inv := seedInvite(t, repo, "project", "p1", "member", "second@x.com", "inviter", "secret-n", false, time.Now().Add(time.Hour))
	if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-n", SubjectType: "user", SubjectID: "user-9"}); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	rows := grantRows(events, securityevent.StatusSuccess)
	if len(rows) != 1 || rows[0].Details["resolved_by"] != "accept" || rows[0].Details["invitation_id"] != inv.ID || rows[0].Details["operation_id"] != inv.ID {
		t.Fatalf("accept provenance = %+v", rows)
	}
	if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-n", SubjectType: "user", SubjectID: "user-7"}); err == nil {
		t.Error("a subject without the identifier accepted")
	}
}

// TestDirectAddRecordsProvenance proves a direct add says who granted it: the
// inviter, the minted operation id, direct_add and resolved_by=direct_add.
func TestDirectAddRecordsProvenance(t *testing.T) {
	events := &fakeSecurityEvents{}
	granter := &fakeGranter{}
	svc := newSvc(t, newFakeInvRepo(), granter, constructorConfig{SecurityEvents: events,
		UserLookup: func(context.Context, string) (string, bool, error) { return "user-known", true, nil }})
	res, err := svc.Create(context.Background(), CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "known@x.com", InvitedBy: "inviter", AutoAccept: true})
	if err != nil || !res.DirectlyAdded {
		t.Fatalf("Create = %+v, %v; want a direct add", res, err)
	}
	rows := grantRows(events, securityevent.StatusSuccess)
	if len(rows) != 1 {
		t.Fatalf("granted rows = %d, want 1", len(rows))
	}
	d := rows[0].Details
	if d["direct_add"] != true || d["invited_by"] != "inviter" || d["resolved_by"] != "direct_add" || d["operation_id"] != granter.ops[0] {
		t.Errorf("direct-add provenance = %+v", d)
	}
	if _, ok := d["invitation_id"]; ok {
		t.Errorf("a direct add names an invitation: %+v", d)
	}
}

// TestOpenKindInvitationAcceptsByToken pins the named residual: an open-kind
// invitation resolves only for a verified identifier of that kind (the rail
// verifies none today), and otherwise the token accepts it for any subject
// holding at least one verified identifier.
func TestOpenKindInvitationAcceptsByToken(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, constructorConfig{VerifiedIdentifiers: func(_ context.Context, userID string) ([]sdk.IdentityAddress, error) {
		if userID == "user-4" {
			return nil, nil
		}
		return []sdk.IdentityAddress{{Kind: "chat", Value: "@alice"}}, nil
	}, BodySenders: map[string]delivery.BodySender{"chat": &fakeNotifier{}}})
	seedInviteKind(t, repo, "project", "p1", "member", "@alice", "chat", "inviter", "secret-c", true, time.Now().Add(time.Hour))
	if n, err := svc.ResolveInvitations(context.Background(), "chat", "@alice", "user", "user-3"); err != nil || n != 1 {
		t.Fatalf("a verified open-kind identifier resolves: %d, %v", n, err)
	}
	other := seedInviteKind(t, repo, "project", "p2", "member", "@bob", "chat", "inviter", "secret-d", true, time.Now().Add(time.Hour))
	if n, _ := svc.ResolveInvitations(context.Background(), "chat", "@bob", "user", "user-3"); n != 0 {
		t.Fatalf("an unverified open-kind value resolved %d", n)
	}
	if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-d", SubjectType: "user", SubjectID: "user-4"}); !errors.Is(err, ErrIdentifierMismatch) {
		t.Fatalf("open-kind accept by a subject with no verified identifier: err = %v, want ErrIdentifierMismatch", err)
	}
	if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-d", SubjectType: "user", SubjectID: "user-3"}); err != nil {
		t.Fatalf("open-kind token accept: %v", err)
	}
	if got, _ := repo.Get(context.Background(), other.ID); got.Status != StatusAccepted {
		t.Errorf("open-kind accept = %+v", got)
	}
}

// barrierGranter holds a Grant briefly so a second resolution path can reach
// the same invitation while the first is inside the Granter.
type barrierGranter struct {
	mu  sync.Mutex
	ops []string
}

func (g *barrierGranter) Grant(_ context.Context, in GrantInput) error {
	g.mu.Lock()
	g.ops = append(g.ops, in.OperationID)
	g.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	return nil
}

// lockedEvents is a fakeSecurityEvents safe for concurrent recording.
type lockedEvents struct {
	mu sync.Mutex
	fakeSecurityEvents
}

func (r *lockedEvents) Create(ctx context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fakeSecurityEvents.Create(ctx, evt)
}

// TestConcurrentResolutionDuplicatesShareOperationID pins the documented
// concurrency residual (BE-R2): a verification and a sync racing on one
// invitation may both reach the Granter and both record invitation_granted,
// but every row and every grant carries the invitation's one operation_id, the
// key an auditor and the host's Granter dedupe on.
func TestConcurrentResolutionDuplicatesShareOperationID(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &barrierGranter{}
	events := &lockedEvents{}
	svc := newSvc(t, repo, granter, constructorConfig{VerifiedIdentifiers: multiIdentifier, SecurityEvents: events})
	// The synchronous test outbox is not safe for concurrent sends; the
	// member-added notice is not what this case is about.
	svc.deliverer, svc.queue = nil, nil
	inv := seedInvite(t, repo, "project", "A", "member", "second@x.com", "inviter", "s-a", true, time.Now().Add(time.Hour))
	var wg sync.WaitGroup
	for _, resolve := range []func(context.Context, string, string, string, string) (int, error){svc.ResolveInvitations, svc.ResolvePendingInvitations} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := resolve(context.Background(), sdk.AddressKindEmail, "second@x.com", "user", "user-9"); err != nil {
				t.Errorf("resolve: %v", err)
			}
		}()
	}
	wg.Wait()
	rows := grantRows(&events.fakeSecurityEvents, securityevent.StatusSuccess)
	if len(rows) == 0 || len(granter.ops) == 0 {
		t.Fatalf("no grant recorded: rows=%d grants=%d", len(rows), len(granter.ops))
	}
	for _, row := range rows {
		if row.Details["operation_id"] != inv.ID {
			t.Errorf("row operation_id = %v, want %s", row.Details["operation_id"], inv.ID)
		}
	}
	for _, op := range granter.ops {
		if op != inv.ID {
			t.Errorf("grant operation id = %s, want %s", op, inv.ID)
		}
	}
	if got, _ := repo.Get(context.Background(), inv.ID); got.Status != StatusAccepted {
		t.Errorf("invitation = %+v, want accepted", got)
	}
}

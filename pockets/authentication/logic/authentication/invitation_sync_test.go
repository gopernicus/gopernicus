package authentication

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// hookedResolver is a fakeResolver that runs onResolve before recording a
// verification-path call.
type hookedResolver struct {
	fakeResolver
	onResolve func()
}

func (h *hookedResolver) ResolveInvitations(ctx context.Context, kind, value, subjectType, subjectID string) (int, error) {
	if h.onResolve != nil {
		h.onResolve()
	}
	return h.fakeResolver.ResolveInvitations(ctx, kind, value, subjectType, subjectID)
}

// TestConfirmIdentifierChangeResolvesInvitationsLast proves a confirmed
// identifier add resolves the invitations addressed to the new value, after
// the change notices are enqueued, and that a failing resolver never fails the
// confirm.
func TestConfirmIdentifierChangeResolvesInvitationsLast(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "owner@example.com", "password123456789")
	noticesAtResolve := -1
	resolver := &hookedResolver{fakeResolver: fakeResolver{err: errors.New("grant boom")}}
	resolver.onResolve = func() { noticesAtResolve = h.mailer.noticeToCount("owner@example.com") }
	h.svc.invitations = resolver

	if err := h.startAndConfirmEmail(t, sessionID, userID, "Second@Example.com", identifier.Uses{Login: true, Notification: true}, false); err != nil {
		t.Fatalf("confirm with a failing resolver: %v", err)
	}
	want := []resolverCall{{string(identifier.KindEmail), "second@example.com", PrincipalUser, userID}}
	if !slices.Equal(resolver.calls, want) {
		t.Fatalf("resolver calls = %+v, want %+v", resolver.calls, want)
	}
	if noticesAtResolve != 1 {
		t.Errorf("notices enqueued before resolution = %d, want 1 (resolution runs last)", noticesAtResolve)
	}
}

// TestConfirmIdentifierChangeWrongCodeResolvesNothing proves a refused confirm
// never resolves.
func TestConfirmIdentifierChangeWrongCodeResolvesNothing(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "owner@example.com", "password123456789")
	resolver := &fakeResolver{}
	h.svc.invitations = resolver
	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Value: "second@example.com", Uses: identifier.Uses{Login: true}}); err != nil {
		t.Fatalf("StartIdentifierChange: %v", err)
	}
	if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: "000000"}); err == nil {
		t.Fatal("a wrong code confirmed")
	}
	if len(resolver.calls) != 0 {
		t.Errorf("a refused confirm resolved: %+v", resolver.calls)
	}
}

// TestSyncInvitationsWalksVerifiedIdentifiersUnderBudget proves the on-demand
// sync asks the pending-only resolver once per verified identifier and that
// the eleventh call in a minute is ErrRateLimited.
func TestSyncInvitationsWalksVerifiedIdentifiersUnderBudget(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, _ := h.mustVerifiedLogin(t, "owner@example.com", "password123456789")
	resolver := &fakeResolver{}
	h.svc.invitations = resolver

	addresses, err := h.svc.VerifiedIdentifiers(ctx, userID)
	if err != nil || len(addresses) != 1 || addresses[0] != (sdk.IdentityAddress{Kind: sdk.AddressKindEmail, Value: "owner@example.com"}) {
		t.Fatalf("VerifiedIdentifiers = %+v, %v", addresses, err)
	}
	for i := range 10 {
		if _, err := h.svc.SyncInvitations(ctx, userID); err != nil {
			t.Fatalf("sync %d: %v", i+1, err)
		}
	}
	if len(resolver.calls) != 10 || resolver.calls[0] != (resolverCall{"sync:email", "owner@example.com", PrincipalUser, userID}) {
		t.Fatalf("sync calls = %+v", resolver.calls)
	}
	if _, err := h.svc.SyncInvitations(ctx, userID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("11th sync err = %v, want ErrRateLimited", err)
	}
	if len(resolver.calls) != 10 {
		t.Errorf("a rate-limited sync resolved: %d calls", len(resolver.calls))
	}
}

// TestListSecurityEventsRecordsAuditRead proves every listing records one
// audit_read row naming the filter keys present, never their values.
func TestListSecurityEventsRecordsAuditRead(t *testing.T) {
	h := newHarness(t, nil)
	ctx := sdk.WithPrincipal(context.Background(), sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "admin-1"})
	if !h.svc.SecurityEventsEnabled() {
		t.Fatal("SecurityEventsEnabled = false with a repository wired")
	}
	filter := securityevent.ListFilter{EventType: securityevent.TypeLogin, Since: time.Now().Add(-time.Hour)}
	for range 2 {
		if _, err := h.svc.ListSecurityEvents(ctx, filter, list.Request{Limit: 25}); err != nil {
			t.Fatalf("ListSecurityEvents: %v", err)
		}
	}
	var reads []securityevent.SecurityEvent
	for _, e := range h.events.recorded() {
		if e.EventType == securityevent.TypeAuditRead {
			reads = append(reads, e)
		}
	}
	if len(reads) != 2 {
		t.Fatalf("audit_read rows = %d, want 2 (no dedupe)", len(reads))
	}
	d := reads[0].Details
	if reads[0].Actor.ID != "admin-1" || d["rail"] != "security_events" || d["limit"] != 25 || d["returned"] != 0 || d["strategy"] != "cursor" {
		t.Errorf("audit_read row = %+v", reads[0])
	}
	if keys, _ := d["filters"].([]string); !slices.Equal(keys, []string{"event_type", "since"}) {
		t.Errorf("filters = %v, want [event_type since]", d["filters"])
	}
}

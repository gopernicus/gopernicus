package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
	listing "github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The retention conformance cases (SOC 2 S3). Repositories.Maintenance is
// OPTIONAL, so the group skips LOUDLY when it is unwired. Every cutoff is
// relative to time.Now — never suiteBase — and every fixture instant is
// microsecond-aligned, so a SQL store's timestamp precision cannot move a row
// across a <= boundary. Rail fixtures sit hundreds of days in the past, well
// beyond the 365-day database floor measured on the store's own clock.

// day is one calendar day.
const day = 24 * time.Hour

// runMaintenance registers the retention group.
func runMaintenance(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()
	t.Run("Maintenance", func(t *testing.T) {
		if newRepos(t).Maintenance == nil {
			t.Skip("Maintenance not wired — retention conformance NOT verified for this Repositories")
		}
		for _, tc := range []struct {
			name string
			test func(*testing.T, auth.Repositories)
		}{
			{"PurgeExpiredSessionsKeepsLiveAndBoundary", testMaintenanceSessions},
			{"PurgeExpiredOAuthStates", testMaintenanceOAuthStates},
			{"PurgeExpiredGrants", testMaintenanceGrants},
			{"PurgeExpiredContactChanges", testMaintenanceContactChanges},
			{"PurgeInvitationsTerminalAndExpiredPending", testMaintenanceInvitationsPurged},
			{"PurgeInvitationsKeepsAcceptingAndLivePending", testMaintenanceInvitationsKept},
			{"CountStaleAcceptingInvitations", testMaintenanceStaleClaims},
			{"PruneOAuth2Bounded", testMaintenancePruneOAuth2},
			{"ExportSecurityEventsOrderedCursoredAndTiesWhole", testMaintenanceExport},
			{"PurgeSecurityEventsAppendsPurgeRowEachCall", testMaintenancePurgeRowEachCall},
			{"PurgeSecurityEventsBoundaryAndLimit", testMaintenancePurgeBoundary},
			{"PurgeSecurityEventsFloorPlusOneDayAcceptedMinusOneRefused", testMaintenancePurgeFloor},
			{"LimitZeroRefused", testMaintenanceLimitZero},
			{"ConcurrentPassesAreSafe", testMaintenanceConcurrent},
		} {
			t.Run(tc.name, func(t *testing.T) { tc.test(t, newRepos(t)) })
		}
	})
}

// retentionNow is the wall clock at microsecond precision.
func retentionNow() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// detailInt reads a numeric Details value whatever its decoded type.
func detailInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == float64(int(n))
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

// seedRailEvent appends one rail row with an explicit id and instant.
func seedRailEvent(t *testing.T, repos auth.Repositories, id, userID string, at time.Time) securityevent.SecurityEvent {
	t.Helper()
	evt := securityevent.SecurityEvent{ID: id, UserID: userID, EventType: securityevent.TypeLogin, EventStatus: securityevent.StatusSuccess,
		Details: map[string]any{"reason": "fixture"}, IPAddress: "203.0.113.9", UserAgent: "fixture-agent", CreatedAt: at}
	created, err := repos.SecurityEvents.Create(context.Background(), evt)
	if err != nil {
		t.Fatalf("seed rail row %s: %v", id, err)
	}
	return created
}

// railRows returns every rail row, oldest first, split into the purge
// evidence rows and the rest.
func railRows(t *testing.T, repos auth.Repositories) (purges, others []securityevent.SecurityEvent) {
	t.Helper()
	page, err := repos.SecurityEvents.List(context.Background(), securityevent.ListFilter{}, listing.Request{Limit: 500})
	if err != nil {
		t.Fatalf("List rail: %v", err)
	}
	for i := len(page.Items) - 1; i >= 0; i-- {
		evt := page.Items[i]
		if evt.EventType == securityevent.TypeSecurityEventsPurged {
			purges = append(purges, evt)
		} else {
			others = append(others, evt)
		}
	}
	return purges, others
}

// checkPurgeRow asserts the portable shape of one evidence row: type, status,
// count and the parsed cutoff — never the actor id or the JSON formatting.
func checkPurgeRow(t *testing.T, evt securityevent.SecurityEvent, cutoff time.Time, count int) {
	t.Helper()
	if evt.EventType != securityevent.TypeSecurityEventsPurged || evt.EventStatus != securityevent.StatusSuccess {
		t.Errorf("purge row = %s/%s", evt.EventType, evt.EventStatus)
	}
	if got, ok := detailInt(evt.Details["count"]); !ok || got != count {
		t.Errorf("purge row count = %v, want %d", evt.Details["count"], count)
	}
	raw, _ := evt.Details["cutoff"].(string)
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || !parsed.Equal(cutoff) {
		t.Errorf("purge row cutoff = %q (%v), want %s", raw, err, cutoff.Format(time.RFC3339Nano))
	}
}

func testMaintenanceSessions(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	now := retentionNow()
	before := now.Add(-time.Hour)
	mk := func(label string, expires time.Time) string {
		s := newSession("maintenance-user", "maintenance-"+label, time.Hour, now.Add(-3*time.Hour))
		s.ExpiresAt = expires
		created, err := repos.Sessions.Create(ctx, s)
		if err != nil {
			t.Fatalf("Create(%s): %v", label, err)
		}
		return created.ID
	}
	old := mk("old", before.Add(-time.Minute))
	boundary := mk("boundary", before)
	after := mk("after", before.Add(time.Microsecond))
	live := mk("live", now.Add(time.Hour))

	n, err := repos.Maintenance.PurgeExpiredSessions(ctx, before, 100)
	if err != nil || n != 2 {
		t.Fatalf("PurgeExpiredSessions = %d, %v; want 2 (the old row and the row exactly at before)", n, err)
	}
	for _, id := range []string{old, boundary} {
		if _, err := repos.Sessions.Get(ctx, id); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("purged session %s: Get err = %v, want ErrNotFound", id, err)
		}
	}
	if _, err := repos.Sessions.Get(ctx, after); errors.Is(err, sdk.ErrNotFound) {
		t.Error("a session one microsecond past before was purged")
	}
	if _, err := repos.Sessions.Get(ctx, live); err != nil {
		t.Errorf("live session: %v", err)
	}

	for i := range 3 {
		mk(fmt.Sprintf("bulk-%d", i), before.Add(-time.Duration(i+1)*time.Minute))
	}
	for i, want := range []int{2, 1, 0} {
		if n, err := repos.Maintenance.PurgeExpiredSessions(ctx, before, 2); err != nil || n != want {
			t.Errorf("bounded call %d = %d, %v; want %d", i, n, err, want)
		}
	}
}

func testMaintenanceOAuthStates(t *testing.T, repos auth.Repositories) {
	if repos.OAuthStates == nil {
		t.Skip("OAuthStates not wired — the OAuth-state purge NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	expired := oauthstate.New("google", oauthstate.PurposePendingLink, []byte("old"), time.Minute, now.Add(-2*time.Hour))
	live := oauthstate.New("google", oauthstate.PurposePendingLink, []byte("live"), time.Hour, now)
	for _, st := range []oauthstate.State{expired, live} {
		if _, err := repos.OAuthStates.Create(ctx, st); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if n, err := repos.Maintenance.PurgeExpiredOAuthStates(ctx, now.Add(-time.Hour), 100); err != nil || n != 1 {
		t.Fatalf("PurgeExpiredOAuthStates = %d, %v; want 1", n, err)
	}
	if _, err := repos.OAuthStates.Consume(ctx, expired.Token); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("purged state: Consume err = %v, want ErrNotFound", err)
	}
	if _, err := repos.OAuthStates.Consume(ctx, live.Token); err != nil {
		t.Errorf("live state: %v", err)
	}
}

func testMaintenanceGrants(t *testing.T, repos auth.Repositories) {
	if repos.AuthenticationGrants == nil {
		t.Skip("AuthenticationGrants not wired — the grant purge NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	g := grantFixture{repos}
	if _, err := g.Create(ctx, newGrant("maintenance-grant-session", "maintenance-grant-user", "set_password", "ctx-old", -2*time.Hour, now)); err != nil {
		t.Fatalf("Create(old): %v", err)
	}
	if _, err := g.Create(ctx, newGrant("maintenance-grant-session", "maintenance-grant-user", "change_password", "ctx-live", time.Hour, now)); err != nil {
		t.Fatalf("Create(live): %v", err)
	}
	if n, err := repos.Maintenance.PurgeExpiredGrants(ctx, now.Add(-time.Hour), 100); err != nil || n != 1 {
		t.Fatalf("PurgeExpiredGrants = %d, %v; want 1", n, err)
	}
	if _, err := g.Consume(ctx, "maintenance-grant-session", "change_password", "ctx-live", time.Now()); err != nil {
		t.Errorf("the live grant did not survive: %v", err)
	}
}

func testMaintenanceContactChanges(t *testing.T, repos auth.Repositories) {
	if repos.ContactChanges == nil {
		t.Skip("ContactChanges not wired — the contact-change purge NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	old := contactchange.New("maintenance-cc-old", identifier.KindEmail, "old@example.com", loginRecoveryUses, true, "", time.Minute, now.Add(-2*time.Hour))
	live := contactchange.New("maintenance-cc-live", identifier.KindEmail, "live@example.com", loginRecoveryUses, true, "", time.Hour, now)
	for _, p := range []contactchange.PendingChange{old, live} {
		if _, err := repos.ContactChanges.Create(ctx, p); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if n, err := repos.Maintenance.PurgeExpiredContactChanges(ctx, now.Add(-time.Hour), 100); err != nil || n != 1 {
		t.Fatalf("PurgeExpiredContactChanges = %d, %v; want 1", n, err)
	}
	if _, err := repos.ContactChanges.Get(ctx, "maintenance-cc-old", identifier.KindEmail); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("purged change: Get err = %v, want ErrNotFound", err)
	}
	if _, err := repos.ContactChanges.Get(ctx, "maintenance-cc-live", identifier.KindEmail); err != nil {
		t.Errorf("live change: %v", err)
	}
}

// seedRetentionInvitation creates a pending invitation of kind to address,
// issued by invitedBy at created, valid for ttl.
func seedRetentionInvitation(t *testing.T, repos auth.Repositories, label, kind, address, invitedBy string, created time.Time, ttl time.Duration) invitations.Invitation {
	t.Helper()
	inv, err := invitations.NewInvitation(ids, "project", "retention-"+label, "member", address, kind, invitedBy, "hash-"+label+"-"+ids.MustGenerate(), false, ttl, created)
	if err != nil {
		t.Fatalf("NewInvitation(%s): %v", label, err)
	}
	inv.Metadata = map[string]string{"route": "r-" + label}
	out, err := repos.Invitations.Create(context.Background(), inv)
	if err != nil {
		t.Fatalf("Create invitation %s: %v", label, err)
	}
	return out
}

// declineInvitation moves a pending invitation to declined at at.
func declineInvitation(t *testing.T, repos auth.Repositories, inv invitations.Invitation, at time.Time) {
	t.Helper()
	if _, err := repos.Invitations.UpdateStatus(context.Background(), inv.ID, invitations.StatusUpdate{
		ExpectedTokenHash: inv.TokenHash, Status: invitations.StatusDeclined, TokenHash: inv.TokenHash, ExpiresAt: inv.ExpiresAt, UpdatedAt: at,
	}); err != nil {
		t.Fatalf("decline %s: %v", inv.ID, err)
	}
}

// claimInvitation binds inv to subjectID at at (accepting); complete also
// accepts it.
func claimRetentionInvitation(t *testing.T, repos auth.Repositories, inv invitations.Invitation, subjectID string, at time.Time, complete bool) {
	t.Helper()
	claim := invitations.Acceptance{TokenHash: inv.TokenHash, SubjectType: "user", SubjectID: subjectID, Now: at}
	if _, err := repos.Invitations.ClaimAcceptance(context.Background(), inv.ID, claim); err != nil {
		t.Fatalf("claim %s: %v", inv.ID, err)
	}
	if complete {
		if _, err := repos.Invitations.CompleteAcceptance(context.Background(), inv.ID, claim); err != nil {
			t.Fatalf("complete %s: %v", inv.ID, err)
		}
	}
}

func testMaintenanceInvitationsPurged(t *testing.T, repos auth.Repositories) {
	if repos.Invitations == nil {
		t.Skip("Invitations not wired — the invitation purge NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	before := now.Add(-day)
	old := now.Add(-3 * day)
	expiredPending := seedRetentionInvitation(t, repos, "expired-pending", "", "expired@example.com", "inviter-1", old, time.Hour)
	declined := seedRetentionInvitation(t, repos, "declined", "", "declined@example.com", "inviter-1", old, time.Hour)
	declineInvitation(t, repos, declined, old.Add(time.Minute))
	accepted := seedRetentionInvitation(t, repos, "accepted", "", "accepted@example.com", "inviter-1", old, time.Hour)
	claimRetentionInvitation(t, repos, accepted, "member-1", old.Add(time.Minute), true)
	boundary := seedRetentionInvitation(t, repos, "boundary", "", "boundary@example.com", "inviter-1", before.Add(-time.Hour), time.Hour)
	recent := seedRetentionInvitation(t, repos, "recent-declined", "", "recent@example.com", "inviter-1", now.Add(-time.Hour), time.Hour)
	declineInvitation(t, repos, recent, now.Add(-time.Minute))

	n, err := repos.Maintenance.PurgeInvitations(ctx, before, 100)
	if err != nil || n != 4 {
		t.Fatalf("PurgeInvitations = %d, %v; want 4 (expired pending, declined, accepted, the pending expiring exactly at before)", n, err)
	}
	for _, inv := range []invitations.Invitation{expiredPending, declined, accepted, boundary} {
		if _, err := repos.Invitations.Get(ctx, inv.ID); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("purged invitation %s: Get err = %v, want ErrNotFound", inv.ID, err)
		}
	}
	if _, err := repos.Invitations.Get(ctx, recent.ID); err != nil {
		t.Errorf("a terminal invitation inside retention was purged: %v", err)
	}
	if n, err := repos.Maintenance.PurgeInvitations(ctx, before, 100); err != nil || n != 0 {
		t.Errorf("a repeat purge = %d, %v; want 0", n, err)
	}
}

func testMaintenanceInvitationsKept(t *testing.T, repos auth.Repositories) {
	if repos.Invitations == nil {
		t.Skip("Invitations not wired — the invitation purge NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	before := now.Add(-day)
	accepting := seedRetentionInvitation(t, repos, "accepting", "", "accepting@example.com", "inviter-1", now.Add(-30*day), time.Hour)
	claimRetentionInvitation(t, repos, accepting, "claimant-1", now.Add(-30*day).Add(time.Minute), false)
	livePending := seedRetentionInvitation(t, repos, "live-pending", "", "live@example.com", "inviter-1", now, 7*day)
	lateExpiry := seedRetentionInvitation(t, repos, "late-expiry", "", "late@example.com", "inviter-1", before.Add(-time.Hour), time.Hour+time.Microsecond)

	if n, err := repos.Maintenance.PurgeInvitations(ctx, before, 100); err != nil || n != 0 {
		t.Fatalf("PurgeInvitations = %d, %v; want 0", n, err)
	}
	for _, inv := range []invitations.Invitation{accepting, livePending, lateExpiry} {
		got, err := repos.Invitations.Get(ctx, inv.ID)
		if err != nil {
			t.Errorf("kept invitation %s: %v", inv.ID, err)
			continue
		}
		if inv.ID == accepting.ID && got.Status != invitations.StatusAccepting {
			t.Errorf("the accepting claim changed to %s — maintenance never settles a claim", got.Status)
		}
	}
}

func testMaintenanceStaleClaims(t *testing.T, repos auth.Repositories) {
	if repos.Invitations == nil {
		t.Skip("Invitations not wired — the stale-claim count NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	before := now.Add(-90 * day)
	stale := seedRetentionInvitation(t, repos, "stale", "", "stale@example.com", "inviter-1", now.Add(-100*day), time.Hour)
	claimRetentionInvitation(t, repos, stale, "claimant-1", now.Add(-100*day).Add(time.Minute), false)
	fresh := seedRetentionInvitation(t, repos, "fresh", "", "fresh@example.com", "inviter-1", now.Add(-time.Hour), 2*time.Hour)
	claimRetentionInvitation(t, repos, fresh, "claimant-2", now.Add(-time.Minute), false)
	accepted := seedRetentionInvitation(t, repos, "settled", "", "settled@example.com", "inviter-1", now.Add(-100*day), time.Hour)
	claimRetentionInvitation(t, repos, accepted, "claimant-3", now.Add(-100*day).Add(time.Minute), true)

	if n, err := repos.Maintenance.CountStaleAcceptingInvitations(ctx, before); err != nil || n != 1 {
		t.Fatalf("CountStaleAcceptingInvitations = %d, %v; want 1", n, err)
	}
}

func testMaintenancePruneOAuth2(t *testing.T, repos auth.Repositories) {
	if repos.OAuth2 == nil {
		t.Skip("OAuth2 not wired — the bounded OAuth2 prune NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	for i := range 3 {
		c := oauth2.Client{ID: fmt.Sprintf("https://expired-%d.test/metadata.json", i), Name: "Expired", RedirectURIs: []string{"https://expired.test/cb"}, ExpiresAt: now.Add(-time.Duration(i+1) * time.Minute)}
		if err := repos.OAuth2.PutClient(ctx, c); err != nil {
			t.Fatalf("PutClient: %v", err)
		}
	}
	live := oauth2.Client{ID: "https://live.test/metadata.json", Name: "Live", RedirectURIs: []string{"https://live.test/cb"}, ExpiresAt: now.Add(time.Hour)}
	if err := repos.OAuth2.PutClient(ctx, live); err != nil {
		t.Fatalf("PutClient: %v", err)
	}
	for i, want := range []int{2, 1, 0} {
		if n, err := repos.Maintenance.PruneOAuth2(ctx, now, 2); err != nil || n != want {
			t.Errorf("bounded prune %d = %d, %v; want %d", i, n, err, want)
		}
	}
	if _, err := repos.OAuth2.GetClient(ctx, live.ID); err != nil {
		t.Errorf("the live client was pruned: %v", err)
	}
}

func testMaintenanceExport(t *testing.T, repos auth.Repositories) {
	if repos.SecurityEvents == nil {
		t.Skip("SecurityEvents not wired — the rail export NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	base := now.Add(-500 * day)
	before := now.Add(-400 * day)
	seedRailEvent(t, repos, "rail-e1", "u1", base)
	seedRailEvent(t, repos, "rail-e2", "u1", base.Add(time.Second))
	for _, id := range []string{"rail-t-c", "rail-t-a", "rail-t-b"} {
		seedRailEvent(t, repos, id, "u2", base.Add(2*time.Second))
	}
	seedRailEvent(t, repos, "rail-e3", "u1", base.Add(3*time.Second))
	seedRailEvent(t, repos, "rail-fresh", "u1", now.Add(-time.Hour))

	var cursor maintenance.ExportCursor
	var pages [][]string
	for range 5 {
		rows, next, err := repos.Maintenance.ExportSecurityEvents(ctx, before, cursor, 2)
		if err != nil {
			t.Fatalf("ExportSecurityEvents: %v", err)
		}
		if len(rows) == 0 {
			if next != cursor {
				t.Errorf("an empty page moved the cursor: %+v → %+v", cursor, next)
			}
			break
		}
		var ids []string
		for _, row := range rows {
			ids = append(ids, row.ID)
			if row.Details["reason"] != "fixture" || row.IPAddress != "203.0.113.9" || row.UserAgent != "fixture-agent" {
				t.Errorf("exported row %s lost content: %+v", row.ID, row.SecurityEvent)
			}
		}
		last := rows[len(rows)-1]
		if next.ID != last.ID || !next.CreatedAt.Equal(last.CreatedAt) {
			t.Errorf("cursor %+v is not the page's last row %s", next, last.ID)
		}
		pages = append(pages, ids)
		cursor = next
	}
	got := fmt.Sprint(pages)
	want := fmt.Sprint([][]string{{"rail-e1", "rail-e2"}, {"rail-t-a", "rail-t-b", "rail-t-c"}, {"rail-e3"}})
	if got != want {
		t.Errorf("export pages = %s, want %s (ascending, the tied instant whole, nothing past before)", got, want)
	}
}

func testMaintenancePurgeRowEachCall(t *testing.T, repos auth.Repositories) {
	if repos.SecurityEvents == nil {
		t.Skip("SecurityEvents not wired — the rail purge NOT verified")
	}
	ctx := context.Background()
	before := retentionNow().Add(-366 * day)
	for i := range 2 {
		res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, before, 10)
		if err != nil || res.Count != 0 || !res.Cutoff.Equal(before) {
			t.Fatalf("empty purge %d = %+v, %v; want count 0 at the cutoff", i, res, err)
		}
	}
	purges, _ := railRows(t, repos)
	if len(purges) != 2 {
		t.Fatalf("purge rows = %d, want one per call (a zero count included)", len(purges))
	}
	for _, evt := range purges {
		checkPurgeRow(t, evt, before, 0)
	}
}

func testMaintenancePurgeBoundary(t *testing.T, repos auth.Repositories) {
	if repos.SecurityEvents == nil {
		t.Skip("SecurityEvents not wired — the rail purge NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	before := now.Add(-400 * day)
	seedRailEvent(t, repos, "rail-o1", "u1", before.Add(-3*time.Second))
	seedRailEvent(t, repos, "rail-o2", "u1", before.Add(-2*time.Second))
	seedRailEvent(t, repos, "rail-o3", "u1", before.Add(-time.Second))
	seedRailEvent(t, repos, "rail-at", "u1", before)
	seedRailEvent(t, repos, "rail-after", "u1", before.Add(time.Microsecond))

	for i, want := range []int{2, 2, 0} {
		res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, before, 2)
		if err != nil || res.Count != want {
			t.Fatalf("purge %d = %+v, %v; want %d", i, res, err, want)
		}
	}
	purges, others := railRows(t, repos)
	if len(others) != 1 || others[0].ID != "rail-after" {
		t.Errorf("rows left = %v, want only the row one microsecond past before", others)
	}
	if len(purges) != 3 {
		t.Fatalf("purge rows = %d, want 3", len(purges))
	}
	for i, want := range []int{2, 2, 0} {
		checkPurgeRow(t, purges[i], before, want)
	}
}

func testMaintenancePurgeFloor(t *testing.T, repos auth.Repositories) {
	if repos.SecurityEvents == nil {
		t.Skip("SecurityEvents not wired — the rail purge NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	seedRailEvent(t, repos, "rail-ancient", "u1", now.Add(-500*day))
	// Never exactly at the floor: the store's clock and this one differ, so the
	// cases sit a day either side of it.
	if res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, now.Add(-366*day), 10); err != nil || res.Count != 1 {
		t.Fatalf("floor + 1 day = %+v, %v; want accepted, 1 row", res, err)
	}
	_, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, now.Add(-364*day), 10)
	if !errors.Is(err, maintenance.ErrBelowRetentionFloor) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("floor - 1 day: err = %v, want ErrBelowRetentionFloor", err)
	}
	if purges, _ := railRows(t, repos); len(purges) != 1 {
		t.Errorf("purge rows = %d, want 1 — a refused cutoff writes no evidence", len(purges))
	}
}

func testMaintenanceLimitZero(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	now := retentionNow()
	m := repos.Maintenance
	for _, limit := range []int{0, -1} {
		for name, call := range map[string]func() error{
			"PurgeExpiredSessions":       func() error { _, err := m.PurgeExpiredSessions(ctx, now, limit); return err },
			"PurgeExpiredOAuthStates":    func() error { _, err := m.PurgeExpiredOAuthStates(ctx, now, limit); return err },
			"PurgeExpiredGrants":         func() error { _, err := m.PurgeExpiredGrants(ctx, now, limit); return err },
			"PurgeExpiredContactChanges": func() error { _, err := m.PurgeExpiredContactChanges(ctx, now, limit); return err },
			"PurgeInvitations":           func() error { _, err := m.PurgeInvitations(ctx, now, limit); return err },
			"PruneOAuth2":                func() error { _, err := m.PruneOAuth2(ctx, now, limit); return err },
			"ExportSecurityEvents": func() error {
				_, _, err := m.ExportSecurityEvents(ctx, now.Add(-400*day), maintenance.ExportCursor{}, limit)
				return err
			},
			"PurgeSecurityEventsBefore": func() error {
				_, err := m.PurgeSecurityEventsBefore(ctx, now.Add(-400*day), limit)
				return err
			},
		} {
			if err := call(); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("%s(limit %d): err = %v, want ErrInvalidInput (never unbounded)", name, limit, err)
			}
		}
	}
	if repos.SecurityEvents != nil {
		if purges, _ := railRows(t, repos); len(purges) != 0 {
			t.Errorf("a refused limit wrote %d purge rows", len(purges))
		}
	}
}

func testMaintenanceConcurrent(t *testing.T, repos auth.Repositories) {
	if repos.SecurityEvents == nil {
		t.Skip("SecurityEvents not wired — the rail purge NOT verified")
	}
	ctx := context.Background()
	now := retentionNow()
	sessionsBefore := now.Add(-time.Hour)
	railBefore := now.Add(-400 * day)
	const sessions, rail = 40, 30
	for i := range sessions {
		s := newSession("maintenance-concurrent", fmt.Sprintf("concurrent-%d", i), time.Hour, now.Add(-3*time.Hour))
		s.ExpiresAt = sessionsBefore.Add(-time.Duration(i+1) * time.Microsecond)
		if _, err := repos.Sessions.Create(ctx, s); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}
	for i := range rail {
		seedRailEvent(t, repos, fmt.Sprintf("rail-concurrent-%02d", i), "u1", railBefore.Add(-time.Duration(i+1)*time.Second))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string
	purgedSessions, purgedRail := 0, 0
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, err := repos.Maintenance.PurgeExpiredSessions(ctx, sessionsBefore, 5)
				res, railErr := repos.Maintenance.PurgeSecurityEventsBefore(ctx, railBefore, 5)
				mu.Lock()
				purgedSessions += n
				purgedRail += res.Count
				if err != nil || railErr != nil {
					errs = append(errs, fmt.Sprint(err, railErr))
				}
				mu.Unlock()
				if err != nil || railErr != nil || (n == 0 && res.Count == 0) {
					return
				}
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent purges failed: %s", strings.Join(errs, "; "))
	}
	if purgedSessions != sessions || purgedRail != rail {
		t.Errorf("concurrent purges removed %d sessions and %d rail rows, want %d and %d — each row exactly once", purgedSessions, purgedRail, sessions, rail)
	}
	if _, others := railRows(t, repos); len(others) != 0 {
		t.Errorf("%d rail rows survived", len(others))
	}
}

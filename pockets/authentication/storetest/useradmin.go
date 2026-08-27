package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// The user-administration and fenced-session-mint conformance cases
// (coordination-hub-auth-upstream CHAU-1.3 / CHAU-1.4). Both ports are OPTIONAL,
// so their groups skip LOUDLY when unwired — a silent green would falsely claim
// lifecycle conformance for a store that implements none of it.
//
// The concurrency cases below are the reason this port exists at all: a store
// that passes the sequential cases but loses the deactivate-versus-mint race is
// exactly the implementation the contract forbids, and only the concurrent cases
// catch it. Run them under -race, and against a live database as well as memory:
// a mutex-based reference passes trivially where a SQL store needs real row
// locking.

// notifyOnlyUses is a contact-only identifier: it makes no login/recovery
// authentication claim, so several users may hold the same address.
var notifyOnlyUses = identifier.Uses{Notification: true}

// runUserAdmin registers the lifecycle conformance groups.
func runUserAdmin(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()

	t.Run("UserDirectory", func(t *testing.T) {
		if newRepos(t).UserAdmin == nil {
			t.Skip("UserAdmin not wired — user directory conformance NOT verified for this Repositories")
		}
		t.Run("SummaryProjection", func(t *testing.T) { testDirectoryProjection(t, newRepos(t)) })
		t.Run("GetSummaryAbsent", func(t *testing.T) { testDirectoryGetAbsent(t, newRepos(t)) })
		t.Run("OrderingAndCursorParity", func(t *testing.T) { testDirectoryOrdering(t, newRepos(t)) })
		t.Run("OffsetAndCount", func(t *testing.T) { testDirectoryOffsetCount(t, newRepos(t)) })
		t.Run("RetiredIdentifiersDoNotDuplicate", func(t *testing.T) { testDirectoryNoDuplicates(t, newRepos(t)) })
	})

	t.Run("UserLifecycle", func(t *testing.T) {
		if newRepos(t).UserAdmin == nil {
			t.Skip("UserAdmin not wired — lifecycle conformance NOT verified for this Repositories")
		}
		t.Run("DeactivateRevokesAndBumpsRevision", func(t *testing.T) { testLifecycleDeactivate(t, newRepos(t)) })
		t.Run("ReactivateMintsNothing", func(t *testing.T) { testLifecycleReactivate(t, newRepos(t)) })
		t.Run("RepeatedTransitionIsIdempotent", func(t *testing.T) { testLifecycleIdempotent(t, newRepos(t)) })
		t.Run("UnknownAndInvalidInputs", func(t *testing.T) { testLifecycleBadInputs(t, newRepos(t)) })
	})

	t.Run("ActiveSessionMint", func(t *testing.T) {
		if newRepos(t).ActiveSessions == nil {
			t.Skip("ActiveSessions not wired — fenced session-mint conformance NOT verified for this Repositories")
		}
		t.Run("ActiveUserRoundTrip", func(t *testing.T) { testActiveMintRoundTrip(t, newRepos(t)) })
		t.Run("DeactivatedUserRefused", func(t *testing.T) { testActiveMintRefused(t, newRepos(t)) })
		t.Run("UnknownUserNotFound", func(t *testing.T) { testActiveMintUnknown(t, newRepos(t)) })
		t.Run("DuplicateRefreshHashConflicts", func(t *testing.T) { testActiveMintDuplicate(t, newRepos(t)) })

		if newRepos(t).UserAdmin == nil {
			t.Skip("UserAdmin not wired — the deactivate-versus-mint race is NOT verified for this Repositories")
		}
		t.Run("ConcurrentDeactivateVersusMint", func(t *testing.T) { testActiveMintRace(t, newRepos(t)) })
	})
}

// seedDirectoryUser creates a user with a primary email identifier at the given
// created_at, so the directory ordering cases can pin exact page order.
func seedDirectoryUser(t *testing.T, repos auth.Repositories, email string, verifiedAt, createdAt time.Time) user.User {
	t.Helper()
	ctx := context.Background()

	u := user.NewUser(dbIDs, "Directory User", createdAt)
	u.CreatedAt = createdAt.UTC()
	u.UpdatedAt = createdAt.UTC()

	uses := loginRecoveryUses
	if verifiedAt.IsZero() {
		// Login/recovery use requires proof; an unverified address is contact-only
		// here, which is what a store must project as EmailVerified=false.
		uses = notifyOnlyUses
	}
	ident, err := identifier.New(dbIDs, idNorm, "", identifier.KindEmail, email, uses, true, verifiedAt, createdAt)
	if err != nil {
		t.Fatalf("identifier.New(%q): %v", email, err)
	}
	created, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier(%q): %v", email, err)
	}
	return created
}

// seedEmaillessUser creates a user whose only identifier is a PHONE, so the
// directory must project an empty PrimaryEmail rather than dropping the row.
func seedEmaillessUser(t *testing.T, repos auth.Repositories, phone string, createdAt time.Time) user.User {
	t.Helper()
	ctx := context.Background()

	u := user.NewUser(dbIDs, "Phone Only", createdAt)
	u.CreatedAt = createdAt.UTC()
	u.UpdatedAt = createdAt.UTC()

	ident, err := identifier.New(dbIDs, idNorm, "", identifier.KindPhone, phone, loginRecoveryUses, true, createdAt, createdAt)
	if err != nil {
		t.Fatalf("identifier.New(%q): %v", phone, err)
	}
	created, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier(%q): %v", phone, err)
	}
	return created
}

// findSummary locates a user's row in a page.
func findSummary(page crud.Page[user.Summary], id string) (user.Summary, bool) {
	for _, s := range page.Items {
		if s.ID == id {
			return s, true
		}
	}
	return user.Summary{}, false
}

// testDirectoryProjection covers every projection shape the contract names:
// verified, unverified, and email-less, in both lifecycle statuses.
func testDirectoryProjection(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()

	verified := seedDirectoryUser(t, repos, "verified@example.com", suiteBase, suiteBase)
	unverified := seedDirectoryUser(t, repos, "unverified@example.com", time.Time{}, suiteBase.Add(time.Second))
	emailless := seedEmaillessUser(t, repos, "+15551230000", suiteBase.Add(2*time.Second))

	// Deactivate one so both statuses appear in the same page.
	if _, err := repos.UserAdmin.SetStatus(ctx, unverified.ID, user.StatusDeactivated, suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("SetStatus(deactivated): %v", err)
	}

	page, err := repos.UserAdmin.List(ctx, crud.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("List returned %d users, want 3", len(page.Items))
	}

	tests := []struct {
		name          string
		id            string
		wantEmail     string
		wantVerified  bool
		wantStatus    user.Status
		wantChangedAt bool
	}{
		{"verified active", verified.ID, "verified@example.com", true, user.StatusActive, false},
		{"unverified deactivated", unverified.ID, "unverified@example.com", false, user.StatusDeactivated, true},
		{"email-less active", emailless.ID, "", false, user.StatusActive, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findSummary(page, tt.id)
			if !ok {
				t.Fatalf("user %q missing from the directory page", tt.id)
			}
			if got.PrimaryEmail != tt.wantEmail {
				t.Errorf("PrimaryEmail = %q, want %q", got.PrimaryEmail, tt.wantEmail)
			}
			if got.EmailVerified != tt.wantVerified {
				t.Errorf("EmailVerified = %v, want %v", got.EmailVerified, tt.wantVerified)
			}
			if user.NormalizeStatus(got.Status) != tt.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.StatusChangedAt.IsZero() == tt.wantChangedAt {
				t.Errorf("StatusChangedAt zero = %v, want zero = %v", got.StatusChangedAt.IsZero(), !tt.wantChangedAt)
			}
			if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
				t.Errorf("timestamps not projected: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
			}

			// The same projection must come back from the single-row read.
			one, err := repos.UserAdmin.GetSummary(ctx, tt.id)
			if err != nil {
				t.Fatalf("GetSummary: %v", err)
			}
			if one.PrimaryEmail != got.PrimaryEmail || one.EmailVerified != got.EmailVerified ||
				user.NormalizeStatus(one.Status) != user.NormalizeStatus(got.Status) {
				t.Errorf("GetSummary %+v disagrees with the List projection %+v", one, got)
			}
		})
	}
}

func testDirectoryGetAbsent(t *testing.T, repos auth.Repositories) {
	if _, err := repos.UserAdmin.GetSummary(context.Background(), "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetSummary(absent): err=%v, want ErrNotFound", err)
	}
}

// testDirectoryOrdering pins the contractual (created_at DESC, id DESC) order —
// including a same-created_at collision, where the id tiebreak is the only thing
// keeping pages stable — and that the emitted cursor is byte-identical across
// implementations for the same data.
func testDirectoryOrdering(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()

	// Three users share one created_at; a fourth is older. Only the id tiebreak
	// can order the first three.
	collide := suiteBase.Add(10 * time.Minute)
	var ids []string
	for i := range 3 {
		u := seedDirectoryUser(t, repos, fmt.Sprintf("collide-%d@example.com", i), suiteBase, collide)
		ids = append(ids, u.ID)
	}
	older := seedDirectoryUser(t, repos, "older@example.com", suiteBase, collide.Add(-time.Hour))

	page, err := repos.UserAdmin.List(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("first page has %d items, want 2", len(page.Items))
	}
	if !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first page HasMore=%v NextCursor=%q, want more", page.HasMore, page.NextCursor)
	}

	// created_at DESC then id DESC: the two highest ids from the collision group.
	first := page.Items[0]
	second := page.Items[1]
	if !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("first page crossed the collision group: %v vs %v", first.CreatedAt, second.CreatedAt)
	}
	if first.ID <= second.ID {
		t.Errorf("id tiebreak is not DESC: %q then %q", first.ID, second.ID)
	}

	next, err := repos.UserAdmin.List(ctx, crud.ListRequest{Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("List(cursor): %v", err)
	}
	if len(next.Items) != 2 {
		t.Fatalf("second page has %d items, want 2", len(next.Items))
	}
	if next.Items[1].ID != older.ID {
		t.Errorf("last row = %q, want the oldest user %q", next.Items[1].ID, older.ID)
	}

	// Every seeded user appears exactly once across the two pages.
	seen := map[string]int{}
	for _, s := range append(append([]user.Summary{}, page.Items...), next.Items...) {
		seen[s.ID]++
	}
	for _, id := range append(ids, older.ID) {
		if seen[id] != 1 {
			t.Errorf("user %q appeared %d times across the pages, want 1", id, seen[id])
		}
	}

	// The cursor must encode the contractual (created_at, id) key so it is
	// byte-identical across dialects for the same boundary row.
	wantCursor, err := crud.EncodeCursor("created_at", second.CreatedAt, second.ID)
	if err != nil {
		t.Fatalf("EncodeCursor: %v", err)
	}
	if page.NextCursor != wantCursor {
		t.Errorf("NextCursor = %q, want the (created_at, id) encoding %q", page.NextCursor, wantCursor)
	}
}

// testDirectoryOffsetCount covers the offset strategy and WithCount, which the
// contract says behave as they do for every other paged auth port.
func testDirectoryOffsetCount(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()

	for i := range 4 {
		seedDirectoryUser(t, repos, fmt.Sprintf("page-%d@example.com", i), suiteBase, suiteBase.Add(time.Duration(i)*time.Minute))
	}

	page, err := repos.UserAdmin.List(ctx, crud.ListRequest{Limit: 2, Offset: 1, Strategy: crud.StrategyOffset, WithCount: true})
	if err != nil {
		t.Fatalf("List(offset): %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("offset page has %d items, want 2", len(page.Items))
	}
	if page.Total == nil || *page.Total != 4 {
		t.Errorf("Total = %v, want 4", page.Total)
	}
	if !page.HasMore {
		t.Error("HasMore = false with one row left after the offset window")
	}

	// The offset window must skip exactly the newest row.
	all, err := repos.UserAdmin.List(ctx, crud.ListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all.Items) != 4 {
		t.Fatalf("full list has %d items, want 4", len(all.Items))
	}
	if page.Items[0].ID != all.Items[1].ID {
		t.Errorf("offset=1 first row = %q, want %q", page.Items[0].ID, all.Items[1].ID)
	}
}

// testDirectoryNoDuplicates proves the projection joins only the ACTIVE primary
// email: a user whose identifier history contains retired rows must still appear
// exactly once, carrying the current address.
func testDirectoryNoDuplicates(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if repos.Identifiers == nil {
		t.Skip("Identifiers not wired — retired-history projection NOT verified")
	}

	u, ident := seedUserWithIdentifier(t, repos, "old@example.com", "old@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	// Replace the primary email; the old row is retired (ReplacedAt set), not
	// deleted, so a naive join would return the user twice.
	if _, err := applyEmailChange(repos, u.ID, "new@example.com", loginRecoveryUses, true, ident.ID, 0, suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("ApplyVerifiedChange: %v", err)
	}

	page, err := repos.UserAdmin.List(ctx, crud.ListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	count := 0
	var got user.Summary
	for _, s := range page.Items {
		if s.ID == u.ID {
			count++
			got = s
		}
	}
	if count != 1 {
		t.Fatalf("user appeared %d times after an identifier replacement, want 1", count)
	}
	if got.PrimaryEmail != "new@example.com" {
		t.Errorf("PrimaryEmail = %q, want the current active primary %q", got.PrimaryEmail, "new@example.com")
	}
	if !got.EmailVerified {
		t.Error("EmailVerified = false for a verified replacement identifier")
	}
}

// seedLifecycleUser creates a user with two live sessions and a grant bound to
// one of them, so a transition has something real to revoke.
func seedLifecycleUser(t *testing.T, repos auth.Repositories) (user.User, []session.Session) {
	t.Helper()
	ctx := context.Background()

	u := seedDirectoryUser(t, repos, "lifecycle@example.com", suiteBase, suiteBase)

	var sessions []session.Session
	for i := range 2 {
		s := newSession(u.ID, fmt.Sprintf("lifecycle-refresh-%d", i), time.Hour, time.Now())
		created, err := repos.Sessions.Create(ctx, s)
		if err != nil {
			t.Fatalf("Sessions.Create: %v", err)
		}
		sessions = append(sessions, created)
	}

	if repos.AuthenticationGrants != nil {
		g := newGrant(sessions[0].ID, u.ID, "set_password", "ctx", time.Hour, time.Now())
		if _, err := repos.AuthenticationGrants.Create(ctx, g); err != nil {
			t.Fatalf("AuthenticationGrants.Create: %v", err)
		}
	}
	return u, sessions
}

// testLifecycleDeactivate is the core CHAU-1.4 case: ONE transition writes the
// status, increments auth_revision exactly once, and deletes every session and
// grant.
func testLifecycleDeactivate(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, sessions := seedLifecycleUser(t, repos)

	before, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get: %v", err)
	}

	now := suiteBase.Add(24 * time.Hour)
	change, err := repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, now)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if !change.Changed {
		t.Error("Changed = false for a real transition")
	}
	if change.Status != user.StatusDeactivated {
		t.Errorf("Status = %q, want deactivated", change.Status)
	}
	if change.ChangedAt.IsZero() {
		t.Error("ChangedAt is zero for an applied transition")
	}

	after, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get after: %v", err)
	}
	if user.NormalizeStatus(after.Status) != user.StatusDeactivated {
		t.Errorf("persisted status = %q, want deactivated", after.Status)
	}
	if after.StatusChangedAt.IsZero() {
		t.Error("persisted StatusChangedAt is zero")
	}
	if after.AuthRevision != before.AuthRevision+1 {
		t.Errorf("auth_revision = %d, want exactly one increment from %d", after.AuthRevision, before.AuthRevision)
	}

	for _, s := range sessions {
		if _, err := repos.Sessions.Get(ctx, s.ID); !errors.Is(err, sdk.ErrNotFound) && !errors.Is(err, sdk.ErrExpired) {
			t.Errorf("session %q survived deactivation: err=%v", s.ID, err)
		}
	}
	if change.RevokedSessions != 0 && change.RevokedSessions != len(sessions) {
		t.Errorf("RevokedSessions = %d, want 0 (uncounted) or %d", change.RevokedSessions, len(sessions))
	}

	if repos.AuthenticationGrants != nil {
		_, err := repos.AuthenticationGrants.Consume(ctx, sessions[0].ID, "set_password", "ctx", time.Now())
		if !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("grant survived deactivation: err=%v, want ErrNotFound", err)
		}
	}
}

// testLifecycleReactivate proves the reverse transition changes the status and
// fabricates nothing.
func testLifecycleReactivate(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, _ := seedLifecycleUser(t, repos)

	if _, err := repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("SetStatus(deactivated): %v", err)
	}
	deactivated, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get: %v", err)
	}

	change, err := repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusActive, suiteBase.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("SetStatus(active): %v", err)
	}
	if !change.Changed || change.Status != user.StatusActive {
		t.Fatalf("reactivate change = %+v, want changed active", change)
	}
	if change.RevokedSessions != 0 {
		t.Errorf("reactivate revoked %d sessions; it must have nothing to revoke", change.RevokedSessions)
	}

	after, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get after: %v", err)
	}
	if user.NormalizeStatus(after.Status) != user.StatusActive {
		t.Errorf("persisted status = %q, want active", after.Status)
	}
	if after.AuthRevision != deactivated.AuthRevision+1 {
		t.Errorf("auth_revision = %d, want one increment from %d", after.AuthRevision, deactivated.AuthRevision)
	}

	// Reactivation must not fabricate a session for the user.
	if repos.ActiveSessions != nil {
		s := newSession(u.ID, "post-reactivate", time.Hour, time.Now())
		if _, err := repos.ActiveSessions.CreateForActiveUser(ctx, s); err != nil {
			t.Fatalf("a reactivated user cannot mint a session: %v", err)
		}
	}
}

// testLifecycleIdempotent proves a replayed transition is a no-op rather than a
// conflict, a not-found, or a second revision increment.
func testLifecycleIdempotent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, _ := seedLifecycleUser(t, repos)

	if _, err := repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("first SetStatus: %v", err)
	}
	first, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get: %v", err)
	}

	change, err := repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, suiteBase.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("replayed SetStatus: err=%v, want success", err)
	}
	if change.Changed {
		t.Error("Changed = true for a replayed status")
	}
	if change.Status != user.StatusDeactivated {
		t.Errorf("Status = %q, want the current deactivated", change.Status)
	}

	second, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get after replay: %v", err)
	}
	if second.AuthRevision != first.AuthRevision {
		t.Errorf("auth_revision moved on a replay: %d → %d", first.AuthRevision, second.AuthRevision)
	}
	if !second.StatusChangedAt.Equal(first.StatusChangedAt) {
		t.Errorf("StatusChangedAt moved on a replay: %v → %v", first.StatusChangedAt, second.StatusChangedAt)
	}
}

// testLifecycleBadInputs pins the sentinel mapping for unknown and invalid input.
func testLifecycleBadInputs(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, _ := seedLifecycleUser(t, repos)

	if _, err := repos.UserAdmin.SetStatus(ctx, "nope", user.StatusDeactivated, suiteBase); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("SetStatus(unknown user): err=%v, want ErrNotFound", err)
	}

	for _, bad := range []user.Status{"", "suspended", "DEACTIVATED", "deleted"} {
		if _, err := repos.UserAdmin.SetStatus(ctx, u.ID, bad, suiteBase); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("SetStatus(%q): err=%v, want ErrInvalidInput", bad, err)
		}
	}

	// An invalid status must not have written anything.
	after, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get: %v", err)
	}
	if user.NormalizeStatus(after.Status) != user.StatusActive {
		t.Errorf("a rejected status was persisted: %q", after.Status)
	}
}

func testActiveMintRoundTrip(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u := seedDirectoryUser(t, repos, "mint@example.com", suiteBase, suiteBase)

	s := newSession(u.ID, "fenced-refresh", time.Hour, time.Now())
	created, err := repos.ActiveSessions.CreateForActiveUser(ctx, s)
	if err != nil {
		t.Fatalf("CreateForActiveUser: %v", err)
	}
	if created.ID != s.ID || created.UserID != u.ID || created.RefreshTokenHash != s.RefreshTokenHash {
		t.Errorf("fenced mint returned %+v, want the proposed session shape %+v", created, s)
	}

	// The row must be readable through the ORDINARY session port: a fenced mint
	// writes the same table, not a parallel one.
	got, err := repos.Sessions.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Sessions.Get after fenced mint: %v", err)
	}
	if got.UserID != u.ID {
		t.Errorf("Sessions.Get returned user %q, want %q", got.UserID, u.ID)
	}
	matched, match, err := repos.Sessions.GetByRefreshHash(ctx, s.RefreshTokenHash)
	if err != nil || matched.ID != created.ID || match != session.RefreshMatchCurrent {
		t.Errorf("GetByRefreshHash after fenced mint: %+v match=%v err=%v", matched, match, err)
	}
}

func testActiveMintRefused(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u := seedDirectoryUser(t, repos, "refused@example.com", suiteBase, suiteBase)

	if _, err := repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	s := newSession(u.ID, "refused-refresh", time.Hour, time.Now())
	if _, err := repos.ActiveSessions.CreateForActiveUser(ctx, s); !errors.Is(err, session.ErrUserNotActive) {
		t.Fatalf("CreateForActiveUser(deactivated): err=%v, want ErrUserNotActive", err)
	}
	if _, err := repos.Sessions.Get(ctx, s.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("a refused mint left a session row: err=%v, want ErrNotFound", err)
	}
}

func testActiveMintUnknown(t *testing.T, repos auth.Repositories) {
	s := newSession("no-such-user", "unknown-refresh", time.Hour, time.Now())
	if _, err := repos.ActiveSessions.CreateForActiveUser(context.Background(), s); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("CreateForActiveUser(unknown user): err=%v, want ErrNotFound", err)
	}
}

func testActiveMintDuplicate(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u := seedDirectoryUser(t, repos, "dup@example.com", suiteBase, suiteBase)

	first := newSession(u.ID, "shared-refresh", time.Hour, time.Now())
	if _, err := repos.ActiveSessions.CreateForActiveUser(ctx, first); err != nil {
		t.Fatalf("first fenced mint: %v", err)
	}
	second := newSession(u.ID, "shared-refresh", time.Hour, time.Now())
	if _, err := repos.ActiveSessions.CreateForActiveUser(ctx, second); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("duplicate refresh hash: err=%v, want ErrAlreadyExists", err)
	}
}

// testActiveMintRace is the case the whole port exists for. Deactivation and a
// fenced mint run concurrently, repeatedly; whichever wins, the end state must
// never be "a live session on a deactivated user".
//
// The two legal outcomes are:
//
//   - the mint commits first and the deactivation deletes it; or
//   - the deactivation commits first and the mint refuses.
//
// Run under -race, and against a live database: a mutex-based reference passes
// this trivially where a SQL store needs real row locking, so a memory-only pass
// does NOT close the live gate.
func testActiveMintRace(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()

	const rounds = 12
	for round := range rounds {
		u := seedDirectoryUser(t, repos, fmt.Sprintf("race-%d@example.com", round), suiteBase, suiteBase)

		var (
			wg       sync.WaitGroup
			mintErr  error
			minted   session.Session
			statusEr error
		)
		start := make(chan struct{})

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			s := newSession(u.ID, fmt.Sprintf("race-refresh-%d", round), time.Hour, time.Now())
			minted, mintErr = repos.ActiveSessions.CreateForActiveUser(ctx, s)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, statusEr = repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, suiteBase.Add(time.Hour))
		}()
		close(start)
		wg.Wait()

		if statusEr != nil {
			t.Fatalf("round %d: SetStatus: %v", round, statusEr)
		}
		if mintErr != nil && !errors.Is(mintErr, session.ErrUserNotActive) {
			t.Fatalf("round %d: unexpected mint error: %v", round, mintErr)
		}

		// The invariant: no live session may remain on a deactivated user.
		if mintErr == nil {
			_, err := repos.Sessions.Get(ctx, minted.ID)
			if err == nil {
				t.Fatalf("round %d: session %q is LIVE on a deactivated user — the mint committed after deactivation without being revoked", round, minted.ID)
			}
			if !errors.Is(err, sdk.ErrNotFound) && !errors.Is(err, sdk.ErrExpired) {
				t.Fatalf("round %d: Sessions.Get: %v", round, err)
			}
		}

		after, err := repos.Users.Get(ctx, u.ID)
		if err != nil {
			t.Fatalf("round %d: Users.Get: %v", round, err)
		}
		if user.NormalizeStatus(after.Status) != user.StatusDeactivated {
			t.Fatalf("round %d: status = %q, want deactivated", round, after.Status)
		}
	}
}

//go:build integration && !live

package firestore

import (
	"context"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// The DIRECTORY PROJECTION cases (N-D3, SCHEMA.md §6). The conformance suite's
// UserDirectory group checks what a page RENDERS; these check what is PERSISTED
// on the users document after each writer that can change it, because that is
// where the projection can silently go stale. A store whose projection is
// written once at creation and never again passes every read-shaped assertion
// until the first address change.
//
// The rows of SCHEMA §6.3 that exist in this task's scope are 1 (atomic create),
// 2 (pure add), 3 (replacement), 4 (primary switch), 5 (verification) and 15
// (Users.Update must not disturb it). Rows 6–12 belong to the credential,
// passwordless and admin paths and extend these tests when they land.

// TestProjectionIsWrittenByTheAtomicCreate is §6.3 row 1, in both directions:
// the first identifier sets the projection when it is a primary email, and
// writes it EMPTY — never absent — when it is not.
func TestProjectionIsWrittenByTheAtomicCreate(t *testing.T) {
	r, db := openRepos(t)

	verified, _ := seedUser(t, r, identifier.KindEmail, "verified@example.com",
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase, testBase)
	if email, ok := storedProjection(t, db, verified.ID); email != "verified@example.com" || !ok {
		t.Errorf("verified primary email projected as (%q, %v), want (verified@example.com, true)", email, ok)
	}

	// An UNVERIFIED primary email is still the address the directory shows: the
	// projection's predicate is the primary index (kind, primary, active), not
	// the authentication claim.
	unverified, _ := seedUser(t, r, identifier.KindEmail, "unverified@example.com",
		identifier.Uses{Notification: true}, true, time.Time{}, testBase)
	if email, ok := storedProjection(t, db, unverified.ID); email != "unverified@example.com" || ok {
		t.Errorf("unverified primary email projected as (%q, %v), want (unverified@example.com, false)", email, ok)
	}

	// A subject whose only identifier is a phone has no address on file. The
	// fields are written EMPTY rather than omitted — an absent field is an
	// absent index entry, and the row would drop out of the directory's own
	// ordering.
	phoneOnly, _ := seedUser(t, r, identifier.KindPhone, "+15551230000",
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase, testBase)
	if email, ok := storedProjection(t, db, phoneOnly.ID); email != "" || ok {
		t.Errorf("email-less subject projected as (%q, %v), want empty and unverified", email, ok)
	}
	// Absence must not remove the subject from the directory.
	page, err := r.UserAdmin.List(context.Background(), crud.ListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, s := range page.Items {
		if s.ID == phoneOnly.ID {
			found = true
			if s.PrimaryEmail != "" || s.EmailVerified {
				t.Errorf("email-less summary = %+v, want empty address and false", s)
			}
		}
	}
	if !found {
		t.Error("a subject with no email identifier is missing from the directory page")
	}
	if len(page.Items) != 3 {
		t.Errorf("directory page has %d rows, want 3", len(page.Items))
	}
}

// TestProjectionFollowsEveryIdentifierWriter walks the four in-scope mutation
// shapes on ONE subject, asserting the persisted pair after each: a pure add
// that is not primary leaves it alone, a promotion moves it, verification flips
// the flag, and a replacement with no replacement primary CLEARS it.
func TestProjectionFollowsEveryIdentifierWriter(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, first := seedUser(t, r, identifier.KindEmail, "start@example.com",
		identifier.Uses{Notification: true}, true, time.Time{}, testBase)
	assertProjection(t, db, u.ID, "start@example.com", false, "after the atomic create")

	// §6.3 row 2, the NON-primary half: a second email that is not primary must
	// not become the projection.
	if _, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:          u.ID,
		Kind:            identifier.KindEmail,
		NormalizedValue: "sidecar@example.com",
		LoginEnabled:    true,
	}, 0, testBase.Add(time.Hour)); err != nil {
		t.Fatalf("ApplyVerifiedChange(non-primary add): %v", err)
	}
	assertProjection(t, db, u.ID, "start@example.com", false, "after a non-primary add")

	// A phone, primary for its own kind, must not touch the EMAIL projection.
	if _, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:          u.ID,
		Kind:            identifier.KindPhone,
		NormalizedValue: "+15557654321",
		LoginEnabled:    true,
		MakePrimary:     true,
	}, 1, testBase.Add(2*time.Hour)); err != nil {
		t.Fatalf("ApplyVerifiedChange(primary phone): %v", err)
	}
	assertProjection(t, db, u.ID, "start@example.com", false, "after a primary phone of another kind")

	// §6.3 rows 3–5 together: a verified replacement that becomes primary
	// retires the old row, moves the projection, and flips the verification flag.
	if _, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:               u.ID,
		Kind:                 identifier.KindEmail,
		NormalizedValue:      "proven@example.com",
		LoginEnabled:         true,
		RecoveryEnabled:      true,
		MakePrimary:          true,
		ReplacesIdentifierID: first.ID,
	}, 2, testBase.Add(3*time.Hour)); err != nil {
		t.Fatalf("ApplyVerifiedChange(verified replacement): %v", err)
	}
	assertProjection(t, db, u.ID, "proven@example.com", true, "after a verified primary replacement")

	// §6.3 row 3's CLEARING half: retiring the projected primary with a
	// replacement that is NOT primary leaves the subject with no active primary
	// email, and both fields must be cleared rather than left stale.
	proven, err := r.Identifiers.GetLogin(ctx, emailKind, "proven@example.com")
	if err != nil {
		t.Fatalf("GetLogin: %v", err)
	}
	if _, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:               u.ID,
		Kind:                 identifier.KindEmail,
		NormalizedValue:      "orphan@example.com",
		LoginEnabled:         true,
		ReplacesIdentifierID: proven.ID,
	}, 3, testBase.Add(4*time.Hour)); err != nil {
		t.Fatalf("ApplyVerifiedChange(retire the projected primary): %v", err)
	}
	assertProjection(t, db, u.ID, "", false, "after the projected primary was retired with no replacement primary")
}

// TestUsersUpdateLeavesTheProjectionAlone is §6.3 row 15 — the
// whole-document-Set hazard, made executable. user.User carries no email fields,
// so a profile write implemented as a document Set would blank the projection
// and the directory would start answering empty addresses with nothing failing.
func TestUsersUpdateLeavesTheProjectionAlone(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, _ := seedUser(t, r, identifier.KindEmail, "keep@example.com",
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase, testBase)

	got, err := r.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got.DisplayName = "Renamed"
	got.UpdatedAt = testBase.Add(time.Hour)
	if _, err := r.Users.Update(ctx, got.ID, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	assertProjection(t, db, u.ID, "keep@example.com", true, "after a profile update")
	summary, err := r.UserAdmin.GetSummary(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if summary.DisplayName != "Renamed" {
		t.Errorf("DisplayName = %q, want Renamed", summary.DisplayName)
	}
}

// TestDirectoryPageReadsNoIdentifiers is the port's explicit performance
// contract ("an implementation must not issue one identifier read per user"),
// asserted by COUNTING document reads rather than by trusting the query. The
// page must cost exactly one query over the users collection and zero point
// reads; anything else means the projection is being resolved per row.
func TestDirectoryPageReadsNoIdentifiers(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	for i := range 4 {
		seedUser(t, r, identifier.KindEmail, "counted-"+string(rune('a'+i))+"@example.com",
			identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase,
			testBase.Add(time.Duration(i)*time.Minute))
	}

	counting := &countingReader{reader: db.ReaderFrom(ctx)}
	page, err := firestoredb.List(ctx, counting, listUsers(db), crud.ListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("page has %d rows, want 4", len(page.Items))
	}
	for _, s := range page.Items {
		if s.PrimaryEmail == "" || !s.EmailVerified {
			t.Errorf("summary %+v lost its projection", s)
		}
	}
	if counting.gets != 0 {
		t.Errorf("the directory page issued %d point reads, want 0 — the projection must come from the users query itself", counting.gets)
	}
	if counting.queries != 1 {
		t.Errorf("the directory page issued %d queries, want exactly 1", counting.queries)
	}
	if counting.documents != 4 {
		t.Errorf("the directory page read %d documents, want 4 (the page itself)", counting.documents)
	}
}

// assertProjection compares the PERSISTED projection with the expected pair.
func assertProjection(t *testing.T, db *firestoredb.DB, userID, wantEmail string, wantVerified bool, when string) {
	t.Helper()
	email, verified := storedProjection(t, db, userID)
	if email != wantEmail || verified != wantVerified {
		t.Errorf("projection %s = (%q, %v), want (%q, %v)", when, email, verified, wantEmail, wantVerified)
	}
}

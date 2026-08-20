package invitationsvc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/sdk"
)

// seedInviteMeta seeds a pending email-kind invitation carrying host metadata.
func seedInviteMeta(t *testing.T, repo *fakeInvRepo, rt, rid, rel, ident, secret string, autoAccept bool, expiresAt time.Time, md map[string]string) invitation.Invitation {
	t.Helper()
	inv := seedInvite(t, repo, rt, rid, rel, ident, "inviter", secret, autoAccept, expiresAt)
	inv.Metadata = md
	repo.byID[inv.ID] = inv
	return inv
}

// TestAcceptDeliversMetadata: accept round-trips the stored row's metadata into
// the Granter verbatim.
func TestAcceptDeliversMetadata(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{})

	want := map[string]string{"vendor_org_id": "org-42"}
	seedInviteMeta(t, repo, "project", "p1", "member", "invitee@x.com", "secret-a", false, time.Now().Add(time.Hour), want)

	if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-a", SubjectType: "user", SubjectID: "user-9", Identifier: "invitee@x.com"}); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if len(granter.metas) != 1 || !reflect.DeepEqual(granter.metas[0], want) {
		t.Fatalf("Grant metadata = %+v, want %+v", granter.metas, want)
	}
}

// TestDirectAddDeliversMetadata: the direct-add path carries CreateInput.Metadata
// into the Granter verbatim.
func TestDirectAddDeliversMetadata(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{
		UserLookup: func(_ context.Context, _ string) (string, bool, error) { return "user-known", true, nil },
	})

	want := map[string]string{"plan": "pro"}
	if _, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "known@x.com", InvitedBy: "inviter", AutoAccept: true, Metadata: want,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(granter.metas) != 1 || !reflect.DeepEqual(granter.metas[0], want) {
		t.Fatalf("Grant metadata = %+v, want %+v", granter.metas, want)
	}
}

// TestResolveDeliversMetadata: resolve-on-registration carries the stored row's
// metadata into the Granter verbatim.
func TestResolveDeliversMetadata(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{})

	want := map[string]string{"vendor_org_id": "org-7"}
	seedInviteMeta(t, repo, "project", "A", "member", "sub@x.com", "s-a", true, time.Now().Add(time.Hour), want)

	n, err := svc.ResolveInvitations(context.Background(), "sub@x.com", "user", "user-7")
	if err != nil || n != 1 {
		t.Fatalf("ResolveInvitations n=%d err=%v", n, err)
	}
	if len(granter.metas) != 1 || !reflect.DeepEqual(granter.metas[0], want) {
		t.Fatalf("Grant metadata = %+v, want %+v", granter.metas, want)
	}
}

// TestGrantMetadataIsDefensiveCopy: the Granter receives a copy it cannot use to
// mutate the persisted row, and a legacy row with no metadata delivers a non-nil
// empty map (never nil).
func TestGrantMetadataIsDefensiveCopy(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{})

	orig := map[string]string{"k": "v"}
	inv := seedInviteMeta(t, repo, "project", "p1", "member", "invitee@x.com", "secret-a", false, time.Now().Add(time.Hour), orig)

	if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-a", SubjectType: "user", SubjectID: "user-9", Identifier: "invitee@x.com"}); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	// Mutate the map the Granter received; the persisted row must be untouched.
	granter.metas[0]["k"] = "tampered"
	granter.metas[0]["injected"] = "x"
	stored, _ := repo.Get(context.Background(), inv.ID)
	if stored.Metadata["k"] != "v" || len(stored.Metadata) != 1 {
		t.Errorf("Granter mutated persisted metadata: %+v", stored.Metadata)
	}

	// A legacy row (no metadata) delivers a non-nil empty map to the Granter.
	granter2 := &fakeGranter{}
	svc2 := newSvc(t, repo, granter2, Deps{})
	seedInvite(t, repo, "project", "p2", "member", "legacy@x.com", "inviter", "secret-b", false, time.Now().Add(time.Hour))
	if _, err := svc2.Accept(context.Background(), AcceptInput{Token: "secret-b", SubjectType: "user", SubjectID: "user-1", Identifier: "legacy@x.com"}); err != nil {
		t.Fatalf("Accept legacy: %v", err)
	}
	if len(granter2.metas) != 1 || granter2.metas[0] == nil || len(granter2.metas[0]) != 0 {
		t.Errorf("legacy Grant metadata = %#v, want non-nil empty map", granter2.metas)
	}
}

// TestCreateRejectsInvalidMetadata: Create validates metadata before any side
// effect — an over-limit map is rejected with ErrInvalidInput and neither grants
// nor persists a pending row.
func TestCreateRejectsInvalidMetadata(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{
		UserLookup: func(_ context.Context, _ string) (string, bool, error) { return "user-known", true, nil },
	})

	tooMany := make(map[string]string, invitation.MetadataMaxEntries+1)
	for i := range invitation.MetadataMaxEntries + 1 {
		tooMany[string(rune('a'+i%26))+string(rune('0'+i/26))] = "v"
	}
	_, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "known@x.com", InvitedBy: "inviter", AutoAccept: true, Metadata: tooMany,
	})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Create(over-limit metadata): err=%v, want ErrInvalidInput", err)
	}
	if len(granter.calls) != 0 {
		t.Errorf("Grant called despite invalid metadata: %+v", granter.calls)
	}
	if len(repo.byID) != 0 {
		t.Errorf("invalid metadata persisted a row: %d rows", len(repo.byID))
	}
}

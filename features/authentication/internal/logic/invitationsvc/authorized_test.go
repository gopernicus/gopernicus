package invitationsvc

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// inviter is the resolved caller the authorized operations are posed for — the
// INVITER, never the invitee.
var inviter = identity.Principal{Type: identity.User, ID: "u-inviter"}

// recordingCheck captures every InviteCheckRequest the service poses and returns
// a configurable verdict, so a test can assert both the content of the question
// and the state of the world at the moment it was asked.
type recordingCheck struct {
	reqs []InviteCheckRequest
	// verdict is consulted for each request; nil authorizes.
	verdict func(InviteCheckRequest) error
	// observe runs at check time, before any side effect — where a test asserts
	// nothing has been persisted or granted yet.
	observe func()
}

func (c *recordingCheck) check(_ context.Context, req InviteCheckRequest) error {
	c.reqs = append(c.reqs, req)
	if c.observe != nil {
		c.observe()
	}
	if c.verdict != nil {
		return c.verdict(req)
	}
	return nil
}

func (c *recordingCheck) last(t *testing.T) InviteCheckRequest {
	t.Helper()
	if len(c.reqs) != 1 {
		t.Fatalf("InviteCheck calls = %d, want exactly 1", len(c.reqs))
	}
	return c.reqs[0]
}

// TestCreateAuthorizedPreparesInviteeContextBeforeCheck: the authorized create
// validates metadata, normalizes the invitee identifier and its kind, and runs the
// invitee lookup BEFORE posing the check — and poses it while the world is still
// untouched (no row, no grant).
func TestCreateAuthorizedPreparesInviteeContextBeforeCheck(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	check := &recordingCheck{}
	svc := newSvc(t, repo, granter, Deps{
		InviteCheck: check.check,
		UserLookup: func(_ context.Context, email string) (string, bool, error) {
			if email != "known@x.com" {
				return "", false, fmt.Errorf("lookup got %q, want the NORMALIZED identifier", email)
			}
			return "u-known", true, nil
		},
	})
	check.observe = func() {
		if len(repo.byID) != 0 {
			t.Errorf("invitation persisted before the check: %d rows", len(repo.byID))
		}
		if len(granter.calls) != 0 {
			t.Errorf("grant attempted before the check: %+v", granter.calls)
		}
	}

	if _, err := svc.CreateAuthorized(context.Background(), inviter, CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "  Known@X.com ", InvitedBy: inviter.ID,
		Metadata: map[string]string{"routing_key": "route-1"},
	}); err != nil {
		t.Fatalf("CreateAuthorized: %v", err)
	}

	got := check.last(t)
	if got.Principal != inviter {
		t.Errorf("check principal = %+v, want the inviter %+v", got.Principal, inviter)
	}
	if got.Action != InviteCreate || got.ResourceType != "project" || got.ResourceID != "p1" || got.Relation != "member" {
		t.Errorf("check request = %+v, want the parsed create question", got)
	}
	if got.Identifier != "known@x.com" {
		t.Errorf("check identifier = %q, want the normalized %q", got.Identifier, "known@x.com")
	}
	if got.IdentifierKind != identity.KindEmail {
		t.Errorf("check identifier kind = %q, want the defaulted %q", got.IdentifierKind, identity.KindEmail)
	}
	if got.ResolvedSubjectID != "u-known" {
		t.Errorf("check resolved subject = %q, want %q", got.ResolvedSubjectID, "u-known")
	}
	if !maps.Equal(got.Metadata, map[string]string{"routing_key": "route-1"}) {
		t.Errorf("check metadata = %+v, want the validated request metadata", got.Metadata)
	}
}

// TestCreateAuthorizedResolvedSubjectCases: the check sees the resolved subject
// for a KNOWN email invitee, an empty one for an UNKNOWN email invitee, and an
// empty one for a NON-EMAIL identifier — which the kind field disambiguates,
// because the feature's lookup is email-kind only and never asks for other kinds.
func TestCreateAuthorizedResolvedSubjectCases(t *testing.T) {
	lookup := func(_ context.Context, email string) (string, bool, error) {
		if email == "known@x.com" {
			return "u-known", true, nil
		}
		return "", false, nil
	}
	cases := []struct {
		name           string
		identifier     string
		kind           string
		wantIdentifier string
		wantKind       string
		wantSubject    string
	}{
		{"known user", "known@x.com", "", "known@x.com", identity.KindEmail, "u-known"},
		{"unknown user", "stranger@x.com", "", "stranger@x.com", identity.KindEmail, ""},
		{"non-email identifier", "+1 (555) 010-2345", identity.KindPhone, "+15550102345", identity.KindPhone, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeInvRepo()
			check := &recordingCheck{}
			svc := newSvc(t, repo, &fakeGranter{}, Deps{
				InviteCheck: check.check,
				UserLookup:  lookup,
				Notifiers:   map[string]notify.Notifier{identity.KindPhone: &fakeNotifier{kind: identity.KindPhone}},
			})

			if _, err := svc.CreateAuthorized(context.Background(), inviter, CreateInput{
				ResourceType: "project", ResourceID: "p1", Relation: "member",
				Identifier: tc.identifier, IdentifierKind: tc.kind, InvitedBy: inviter.ID,
			}); err != nil {
				t.Fatalf("CreateAuthorized: %v", err)
			}
			got := check.last(t)
			if got.Identifier != tc.wantIdentifier || got.IdentifierKind != tc.wantKind {
				t.Errorf("check identifier/kind = %q/%q, want %q/%q",
					got.Identifier, got.IdentifierKind, tc.wantIdentifier, tc.wantKind)
			}
			if got.ResolvedSubjectID != tc.wantSubject {
				t.Errorf("check resolved subject = %q, want %q", got.ResolvedSubjectID, tc.wantSubject)
			}
		})
	}
}

// TestCreateAuthorizedRefusalLeavesNothingBehind: a refusal on EITHER branch —
// the pending-create branch (unknown invitee) and the direct-add branch (known
// invitee + auto-accept) — persists no row and attempts no grant. The three
// policies are generic host classes: per-subject eligibility, quota/deduplication,
// and account compatibility over the opaque routing metadata.
func TestCreateAuthorizedRefusalLeavesNothingBehind(t *testing.T) {
	policies := []struct {
		name    string
		verdict func(InviteCheckRequest) error
	}{
		{
			// Per-subject eligibility: the resolved subject is not eligible for this
			// resource, whatever the relation.
			name: "per-subject eligibility",
			verdict: func(req InviteCheckRequest) error {
				if req.ResolvedSubjectID == "u-known" || req.Identifier == "stranger@x.com" {
					return fmt.Errorf("subject is not eligible: %w", sdk.ErrForbidden)
				}
				return nil
			},
		},
		{
			// Quota / deduplication: the host has already issued its budget of
			// invitations for this resource.
			name: "quota",
			verdict: func(req InviteCheckRequest) error {
				if req.ResourceID == "p1" {
					return fmt.Errorf("invitation quota exhausted: %w", sdk.ErrConflict)
				}
				return nil
			},
		},
		{
			// Account compatibility: the opaque routing value is not valid for this
			// invitee, which only the host can judge.
			name: "account compatibility",
			verdict: func(req InviteCheckRequest) error {
				if req.Metadata["routing_key"] != "route-ok" {
					return fmt.Errorf("routing key is not valid for this invitee: %w", sdk.ErrInvalidReference)
				}
				return nil
			},
		},
	}
	branches := []struct {
		name       string
		identifier string
		autoAccept bool
	}{
		{"pending", "stranger@x.com", false},
		{"direct add", "known@x.com", true},
	}
	for _, p := range policies {
		for _, b := range branches {
			t.Run(p.name+"/"+b.name, func(t *testing.T) {
				repo := newFakeInvRepo()
				granter := &fakeGranter{}
				check := &recordingCheck{verdict: p.verdict}
				svc := newSvc(t, repo, granter, Deps{
					InviteCheck: check.check,
					UserLookup: func(_ context.Context, email string) (string, bool, error) {
						return "u-known", email == "known@x.com", nil
					},
				})

				_, err := svc.CreateAuthorized(context.Background(), inviter, CreateInput{
					ResourceType: "project", ResourceID: "p1", Relation: "member",
					Identifier: b.identifier, InvitedBy: inviter.ID, AutoAccept: b.autoAccept,
					Metadata: map[string]string{"routing_key": "route-bad"},
				})
				if err == nil {
					t.Fatal("CreateAuthorized succeeded despite a refusing policy")
				}
				if len(repo.byID) != 0 {
					t.Errorf("refused create persisted %d rows", len(repo.byID))
				}
				if len(granter.calls) != 0 {
					t.Errorf("refused create granted: %+v", granter.calls)
				}
			})
		}
	}
}

// TestCreateAuthorizedCheckCannotTaintCreate: the policy is handed its OWN clone
// of the metadata, so a check that mutates the map it receives cannot change what
// the service then persists.
func TestCreateAuthorizedCheckCannotTaintCreate(t *testing.T) {
	repo := newFakeInvRepo()
	check := &recordingCheck{verdict: func(req InviteCheckRequest) error {
		req.Metadata["routing_key"] = "TAMPERED"
		req.Metadata["injected"] = "x"
		return nil
	}}
	svc := newSvc(t, repo, &fakeGranter{}, Deps{InviteCheck: check.check})

	res, err := svc.CreateAuthorized(context.Background(), inviter, CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "invitee@x.com", InvitedBy: inviter.ID,
		Metadata: map[string]string{"routing_key": "route-1"},
	})
	if err != nil {
		t.Fatalf("CreateAuthorized: %v", err)
	}
	want := map[string]string{"routing_key": "route-1"}
	if !maps.Equal(res.Invitation.Metadata, want) {
		t.Fatalf("persisted metadata = %+v, want the authorized %+v", res.Invitation.Metadata, want)
	}
}

// TestCreateAuthorizedRejectsInvalidMetadataBeforeCheck: preparation fails first,
// so an invalid map is refused without ever asking the host.
func TestCreateAuthorizedRejectsInvalidMetadataBeforeCheck(t *testing.T) {
	repo := newFakeInvRepo()
	check := &recordingCheck{}
	svc := newSvc(t, repo, &fakeGranter{}, Deps{InviteCheck: check.check})

	tooMany := make(map[string]string, invitation.MetadataMaxEntries+1)
	for i := range invitation.MetadataMaxEntries + 1 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	_, err := svc.CreateAuthorized(context.Background(), inviter, CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "invitee@x.com", InvitedBy: inviter.ID, Metadata: tooMany,
	})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("CreateAuthorized(over-limit metadata) = %v, want ErrInvalidInput", err)
	}
	if len(check.reqs) != 0 {
		t.Errorf("invalid metadata reached the host policy: %+v", check.reqs)
	}
	if len(repo.byID) != 0 {
		t.Errorf("invalid metadata persisted a row: %d rows", len(repo.byID))
	}
}

// TestListByResourceAuthorizedPosesListCheck: the authorized list poses the
// InviteList question with the principal and the resource, and — per the seam's
// contract — an empty relation, metadata, and invitee context.
func TestListByResourceAuthorizedPosesListCheck(t *testing.T) {
	repo := newFakeInvRepo()
	check := &recordingCheck{}
	svc := newSvc(t, repo, &fakeGranter{}, Deps{InviteCheck: check.check})
	seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", inviter.ID, "secret-a", false, time.Now().Add(time.Hour))

	page, err := svc.ListByResourceAuthorized(context.Background(), inviter, "project", "p1", crud.ListRequest{})
	if err != nil {
		t.Fatalf("ListByResourceAuthorized: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("page items = %d, want 1", len(page.Items))
	}
	got := check.last(t)
	if got.Principal != inviter || got.Action != InviteList {
		t.Errorf("check request = %+v, want the InviteList question for %+v", got, inviter)
	}
	if got.ResourceType != "project" || got.ResourceID != "p1" {
		t.Errorf("check resource = %q/%q, want project/p1", got.ResourceType, got.ResourceID)
	}
	if got.Relation != "" || len(got.Metadata) != 0 || got.Identifier != "" || got.IdentifierKind != "" || got.ResolvedSubjectID != "" {
		t.Errorf("list check carried invitee context: %+v", got)
	}
}

// TestListByResourceAuthorizedDenialReadsNothing: a refused list returns the
// denial and an empty page, even though the resource has rows.
func TestListByResourceAuthorizedDenialReadsNothing(t *testing.T) {
	repo := newFakeInvRepo()
	check := &recordingCheck{verdict: func(InviteCheckRequest) error {
		return fmt.Errorf("cannot list invitations: %w", sdk.ErrForbidden)
	}}
	svc := newSvc(t, repo, &fakeGranter{}, Deps{InviteCheck: check.check})
	seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", inviter.ID, "secret-a", false, time.Now().Add(time.Hour))

	page, err := svc.ListByResourceAuthorized(context.Background(), inviter, "project", "p1", crud.ListRequest{})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("denied list = %v, want ErrForbidden", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("denied list returned %d items", len(page.Items))
	}
}

// TestTrustedOperationsStayCheckFree: the trusted composition methods never pose
// the host policy, even with one wired that refuses everything — a host driving
// them directly owns that decision.
func TestTrustedOperationsStayCheckFree(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	check := &recordingCheck{verdict: func(InviteCheckRequest) error {
		return fmt.Errorf("denied: %w", sdk.ErrForbidden)
	}}
	svc := newSvc(t, repo, granter, Deps{InviteCheck: check.check})

	res, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "invitee@x.com", InvitedBy: inviter.ID,
		Metadata: map[string]string{"routing_key": "route-1"},
	})
	if err != nil {
		t.Fatalf("trusted Create: %v", err)
	}
	if res.Invitation.ID == "" {
		t.Fatal("trusted Create persisted no invitation")
	}
	page, err := svc.ListByResource(context.Background(), "project", "p1", crud.ListRequest{})
	if err != nil {
		t.Fatalf("trusted ListByResource: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("trusted list items = %d, want 1", len(page.Items))
	}
	if len(check.reqs) != 0 {
		t.Errorf("a trusted composition method posed the host policy: %+v", check.reqs)
	}
}

// TestAuthorizedOperationsFailClosedWithoutCheck: package auth requires an
// InviteCheck whenever a Granter enables invitations, so reaching an authorized
// operation without one is a wiring bug — refused, never allowed by default.
func TestAuthorizedOperationsFailClosedWithoutCheck(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{}) // no InviteCheck

	if _, err := svc.CreateAuthorized(context.Background(), inviter, CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "invitee@x.com", InvitedBy: inviter.ID,
	}); !errors.Is(err, errInviteCheckNotWired) {
		t.Errorf("CreateAuthorized without a check = %v, want errInviteCheckNotWired", err)
	}
	if len(repo.byID) != 0 || len(granter.calls) != 0 {
		t.Errorf("unauthorized create left state behind: %d rows, %+v grants", len(repo.byID), granter.calls)
	}
	if _, err := svc.ListByResourceAuthorized(context.Background(), inviter, "project", "p1", crud.ListRequest{}); !errors.Is(err, errInviteCheckNotWired) {
		t.Errorf("ListByResourceAuthorized without a check = %v, want errInviteCheckNotWired", err)
	}
}

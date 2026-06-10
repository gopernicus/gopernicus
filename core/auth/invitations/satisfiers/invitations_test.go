package satisfiers

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/invitations"
	invitationsrepo "github.com/gopernicus/gopernicus/core/repositories/rebac/invitations"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// fakeRepo captures inputs and returns canned records so tests can verify
// the satisfier maps correctly in both directions.
type fakeRepo struct {
	createInput invitationsrepo.CreateInvitation
	updateID    string
	updateInput invitationsrepo.UpdateInvitation

	byResourceFilter   invitationsrepo.FilterListByResource
	bySubjectFilter    invitationsrepo.FilterListBySubject
	byIdentifierFilter invitationsrepo.FilterListByIdentifier

	record  invitationsrepo.Invitation
	records []invitationsrepo.Invitation
}

func (f *fakeRepo) Create(_ context.Context, input invitationsrepo.CreateInvitation) (invitationsrepo.Invitation, error) {
	f.createInput = input
	return f.record, nil
}

func (f *fakeRepo) Get(_ context.Context, _ string) (invitationsrepo.Invitation, error) {
	return f.record, nil
}

func (f *fakeRepo) GetByToken(_ context.Context, _ string, _ time.Time) (invitationsrepo.Invitation, error) {
	return f.record, nil
}

func (f *fakeRepo) Update(_ context.Context, id string, input invitationsrepo.UpdateInvitation) (invitationsrepo.Invitation, error) {
	f.updateID = id
	f.updateInput = input
	return f.record, nil
}

func (f *fakeRepo) Delete(_ context.Context, _ string) error { return nil }

func (f *fakeRepo) ListByResource(_ context.Context, filter invitationsrepo.FilterListByResource, _, _ string, _ fop.Order, _ fop.PageStringCursor) ([]invitationsrepo.Invitation, fop.Pagination, error) {
	f.byResourceFilter = filter
	return f.records, fop.Pagination{}, nil
}

func (f *fakeRepo) ListBySubject(_ context.Context, filter invitationsrepo.FilterListBySubject, _ string, _ fop.Order, _ fop.PageStringCursor) ([]invitationsrepo.Invitation, fop.Pagination, error) {
	f.bySubjectFilter = filter
	return f.records, fop.Pagination{}, nil
}

func (f *fakeRepo) ListByIdentifier(_ context.Context, filter invitationsrepo.FilterListByIdentifier, _, _ string, _ time.Time, _ fop.Order, _ fop.PageStringCursor) ([]invitationsrepo.Invitation, fop.Pagination, error) {
	f.byIdentifierFilter = filter
	return f.records, fop.Pagination{}, nil
}

func fullRepoInvitation() invitationsrepo.Invitation {
	subjectID := "user-1"
	acceptedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	redirectURL := "https://example.com/accept"
	return invitationsrepo.Invitation{
		InvitationID:      "inv-1",
		ResourceType:      "tenant",
		ResourceID:        "t-1",
		Relation:          "member",
		Identifier:        "alice@example.com",
		IdentifierType:    "email",
		ResolvedSubjectID: &subjectID,
		InvitedBy:         "inviter-1",
		TokenHash:         "hash-1",
		AutoAccept:        true,
		InvitationStatus:  "pending",
		ExpiresAt:         time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC),
		AcceptedAt:        &acceptedAt,
		RecordState:       "active",
		CreatedAt:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		RedirectURL:       &redirectURL,
	}
}

// TestStructParity guards the plain-conversion contract: engine types must
// stay field-identical (names, types, order) to the generated repo types.
func TestStructParity(t *testing.T) {
	pairs := []struct {
		name   string
		engine any
		repo   any
	}{
		{"Invitation", invitations.Invitation{}, invitationsrepo.Invitation{}},
		{"CreateInvitation", invitations.CreateInvitation{}, invitationsrepo.CreateInvitation{}},
		{"UpdateInvitation", invitations.UpdateInvitation{}, invitationsrepo.UpdateInvitation{}},
		{"FilterListByResource", invitations.FilterListByResource{}, invitationsrepo.FilterListByResource{}},
		{"FilterListBySubject", invitations.FilterListBySubject{}, invitationsrepo.FilterListBySubject{}},
		{"FilterListByIdentifier", invitations.FilterListByIdentifier{}, invitationsrepo.FilterListByIdentifier{}},
	}
	for _, pair := range pairs {
		et := reflect.TypeOf(pair.engine)
		rt := reflect.TypeOf(pair.repo)
		if et.NumField() != rt.NumField() {
			t.Fatalf("%s: engine has %d fields, repo has %d", pair.name, et.NumField(), rt.NumField())
		}
		for i := 0; i < et.NumField(); i++ {
			ef, rf := et.Field(i), rt.Field(i)
			if ef.Name != rf.Name || ef.Type != rf.Type {
				t.Fatalf("%s field %d: engine %s %s != repo %s %s", pair.name, i, ef.Name, ef.Type, rf.Name, rf.Type)
			}
		}
	}
}

func TestCreate_MapsBothDirections(t *testing.T) {
	repo := &fakeRepo{record: fullRepoInvitation()}
	s := NewInvitationSatisfier(repo)

	subjectID := "user-1"
	redirectURL := "https://example.com/accept"
	input := invitations.CreateInvitation{
		ResourceType:      "tenant",
		ResourceID:        "t-1",
		Relation:          "member",
		Identifier:        "alice@example.com",
		IdentifierType:    "email",
		ResolvedSubjectID: &subjectID,
		InvitedBy:         "inviter-1",
		TokenHash:         "hash-1",
		AutoAccept:        true,
		InvitationStatus:  "pending",
		ExpiresAt:         time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC),
		RecordState:       "active",
		RedirectURL:       &redirectURL,
	}

	got, err := s.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Engine → repo direction.
	if repo.createInput != invitationsrepo.CreateInvitation(input) {
		t.Fatalf("repo received %+v, want %+v", repo.createInput, input)
	}

	// Repo → engine direction.
	if !reflect.DeepEqual(got, invitations.Invitation(repo.record)) {
		t.Fatalf("engine received %+v", got)
	}
}

func TestUpdate_MapsBothDirections(t *testing.T) {
	repo := &fakeRepo{record: fullRepoInvitation()}
	s := NewInvitationSatisfier(repo)

	status := "accepted"
	acceptedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	input := invitations.UpdateInvitation{
		InvitationStatus: &status,
		AcceptedAt:       &acceptedAt,
	}

	got, err := s.Update(context.Background(), "inv-1", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.updateID != "inv-1" {
		t.Fatalf("expected id inv-1, got %s", repo.updateID)
	}
	if repo.updateInput.InvitationStatus == nil || *repo.updateInput.InvitationStatus != status {
		t.Fatalf("expected status %q passed to repo, got %v", status, repo.updateInput.InvitationStatus)
	}
	if repo.updateInput.AcceptedAt == nil || !repo.updateInput.AcceptedAt.Equal(acceptedAt) {
		t.Fatalf("expected accepted_at passed to repo, got %v", repo.updateInput.AcceptedAt)
	}
	if !reflect.DeepEqual(got, invitations.Invitation(repo.record)) {
		t.Fatalf("engine received %+v", got)
	}
}

func TestGetByToken_MapsResult(t *testing.T) {
	repo := &fakeRepo{record: fullRepoInvitation()}
	s := NewInvitationSatisfier(repo)

	got, err := s.GetByToken(context.Background(), "hash-1", time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, invitations.Invitation(repo.record)) {
		t.Fatalf("engine received %+v", got)
	}
}

func TestLists_MapFiltersAndResults(t *testing.T) {
	repo := &fakeRepo{records: []invitationsrepo.Invitation{fullRepoInvitation()}}
	s := NewInvitationSatisfier(repo)

	status := "pending"
	autoAccept := true
	resourceType := "tenant"
	authorizedIDs := []string{"inv-1"}

	byResource, _, err := s.ListByResource(context.Background(), invitations.FilterListByResource{
		InvitationStatus: &status,
		AutoAccept:       &autoAccept,
		AuthorizedIDs:    authorizedIDs,
	}, "tenant", "t-1", fop.Order{}, fop.PageStringCursor{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.byResourceFilter.InvitationStatus == nil || *repo.byResourceFilter.InvitationStatus != status {
		t.Fatal("expected status filter to pass through")
	}
	if !reflect.DeepEqual(repo.byResourceFilter.AuthorizedIDs, authorizedIDs) {
		t.Fatal("expected AuthorizedIDs to pass through")
	}
	if len(byResource) != 1 || !reflect.DeepEqual(byResource[0], invitations.Invitation(repo.records[0])) {
		t.Fatalf("unexpected ListByResource result: %+v", byResource)
	}

	bySubject, _, err := s.ListBySubject(context.Background(), invitations.FilterListBySubject{
		ResourceType: &resourceType,
	}, "user-1", fop.Order{}, fop.PageStringCursor{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.bySubjectFilter.ResourceType == nil || *repo.bySubjectFilter.ResourceType != resourceType {
		t.Fatal("expected resource type filter to pass through")
	}
	if len(bySubject) != 1 {
		t.Fatalf("unexpected ListBySubject result: %+v", bySubject)
	}

	byIdentifier, _, err := s.ListByIdentifier(context.Background(), invitations.FilterListByIdentifier{
		AutoAccept: &autoAccept,
	}, "alice@example.com", "email", time.Now().UTC(), fop.Order{}, fop.PageStringCursor{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.byIdentifierFilter.AutoAccept == nil || !*repo.byIdentifierFilter.AutoAccept {
		t.Fatal("expected auto accept filter to pass through")
	}
	if len(byIdentifier) != 1 {
		t.Fatalf("unexpected ListByIdentifier result: %+v", byIdentifier)
	}
}

// TestSatisfierAcceptsGeneratedRepository proves the framework's generated
// *invitationsrepo.Repository satisfies the satisfier's repo interface.
func TestSatisfierAcceptsGeneratedRepository(t *testing.T) {
	var repo *invitationsrepo.Repository
	var _ invitationRepo = repo
}

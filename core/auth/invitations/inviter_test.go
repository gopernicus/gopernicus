package invitations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authorization"
	invitationsrepo "github.com/gopernicus/gopernicus/core/repositories/rebac/invitations"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// =============================================================================
// Mock invitations store
// =============================================================================

type mockInvitationStore struct {
	invitations map[string]invitationsrepo.Invitation // by ID
	byToken     map[string]invitationsrepo.Invitation // by token_hash
	createErr   error
	updateErr   error
	deleteErr   error

	createdInputs []invitationsrepo.CreateInvitation
	updatedInputs []struct {
		ID    string
		Input invitationsrepo.UpdateInvitation
	}
	deletedIDs []string
}

func newMockInvitationStore() *mockInvitationStore {
	return &mockInvitationStore{
		invitations: make(map[string]invitationsrepo.Invitation),
		byToken:     make(map[string]invitationsrepo.Invitation),
	}
}

func (m *mockInvitationStore) addInvitation(inv invitationsrepo.Invitation) {
	m.invitations[inv.InvitationID] = inv
	if inv.TokenHash != "" {
		m.byToken[inv.TokenHash] = inv
	}
}

func (m *mockInvitationStore) Get(_ context.Context, id string) (invitationsrepo.Invitation, error) {
	inv, ok := m.invitations[id]
	if !ok {
		return invitationsrepo.Invitation{}, invitationsrepo.ErrInvitationNotFound
	}
	return inv, nil
}

func (m *mockInvitationStore) GetByToken(_ context.Context, tokenHash string, _ time.Time) (invitationsrepo.Invitation, error) {
	inv, ok := m.byToken[tokenHash]
	if !ok {
		return invitationsrepo.Invitation{}, invitationsrepo.ErrInvitationNotFound
	}
	return inv, nil
}

func (m *mockInvitationStore) Create(_ context.Context, input invitationsrepo.CreateInvitation) (invitationsrepo.Invitation, error) {
	if m.createErr != nil {
		return invitationsrepo.Invitation{}, m.createErr
	}
	m.createdInputs = append(m.createdInputs, input)
	inv := invitationsrepo.Invitation{
		InvitationID:      input.InvitationID,
		ResourceType:      input.ResourceType,
		ResourceID:        input.ResourceID,
		Relation:          input.Relation,
		Identifier:        input.Identifier,
		IdentifierType:    input.IdentifierType,
		ResolvedSubjectID: input.ResolvedSubjectID,
		InvitedBy:         input.InvitedBy,
		TokenHash:         input.TokenHash,
		AutoAccept:        input.AutoAccept,
		RedirectURL:       input.RedirectURL,
		InvitationStatus:  input.InvitationStatus,
		ExpiresAt:         input.ExpiresAt,
		RecordState:       input.RecordState,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	m.addInvitation(inv)
	return inv, nil
}

func (m *mockInvitationStore) Update(_ context.Context, id string, input invitationsrepo.UpdateInvitation) (invitationsrepo.Invitation, error) {
	if m.updateErr != nil {
		return invitationsrepo.Invitation{}, m.updateErr
	}
	inv, ok := m.invitations[id]
	if !ok {
		return invitationsrepo.Invitation{}, invitationsrepo.ErrInvitationNotFound
	}
	m.updatedInputs = append(m.updatedInputs, struct {
		ID    string
		Input invitationsrepo.UpdateInvitation
	}{id, input})

	if input.InvitationStatus != nil {
		inv.InvitationStatus = *input.InvitationStatus
	}
	if input.TokenHash != nil {
		inv.TokenHash = *input.TokenHash
	}
	if input.ExpiresAt != nil {
		inv.ExpiresAt = *input.ExpiresAt
	}
	if input.AcceptedAt != nil {
		inv.AcceptedAt = input.AcceptedAt
	}
	if input.ResolvedSubjectID != nil {
		inv.ResolvedSubjectID = input.ResolvedSubjectID
	}

	inv.UpdatedAt = time.Now().UTC()
	m.invitations[id] = inv
	return inv, nil
}

func (m *mockInvitationStore) Delete(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.invitations[id]; !ok {
		return invitationsrepo.ErrInvitationNotFound
	}
	m.deletedIDs = append(m.deletedIDs, id)
	delete(m.invitations, id)
	return nil
}

func (m *mockInvitationStore) ListByResource(_ context.Context, _ invitationsrepo.FilterListByResource, _, _ string, _ fop.Order, _ fop.PageStringCursor, _ bool) ([]invitationsrepo.Invitation, error) {
	return nil, nil
}

func (m *mockInvitationStore) ListByIdentifier(_ context.Context, filter invitationsrepo.FilterListByIdentifier, identifier string, _ string, _ time.Time, _ fop.Order, _ fop.PageStringCursor, _ bool) ([]invitationsrepo.Invitation, error) {
	var results []invitationsrepo.Invitation
	for _, inv := range m.invitations {
		if inv.Identifier != identifier {
			continue
		}
		if inv.RecordState != "active" {
			continue
		}
		if filter.InvitationStatus != nil && inv.InvitationStatus != *filter.InvitationStatus {
			continue
		}
		if filter.AutoAccept != nil && inv.AutoAccept != *filter.AutoAccept {
			continue
		}
		results = append(results, inv)
	}
	return results, nil
}

func (m *mockInvitationStore) ListBySubject(_ context.Context, filter invitationsrepo.FilterListBySubject, subjectID string, _ fop.Order, _ fop.PageStringCursor, _ bool) ([]invitationsrepo.Invitation, error) {
	var results []invitationsrepo.Invitation
	for _, inv := range m.invitations {
		if inv.ResolvedSubjectID == nil || *inv.ResolvedSubjectID != subjectID {
			continue
		}
		if inv.RecordState != "active" {
			continue
		}
		if filter.InvitationStatus != nil && inv.InvitationStatus != *filter.InvitationStatus {
			continue
		}
		if filter.ResourceType != nil && inv.ResourceType != *filter.ResourceType {
			continue
		}
		results = append(results, inv)
	}
	return results, nil
}

func (m *mockInvitationStore) List(_ context.Context, _ invitationsrepo.FilterList, _ fop.Order, _ fop.PageStringCursor, _ bool) ([]invitationsrepo.Invitation, error) {
	return nil, nil
}

func (m *mockInvitationStore) SoftDelete(_ context.Context, _ string) error { return nil }
func (m *mockInvitationStore) Archive(_ context.Context, _ string) error    { return nil }
func (m *mockInvitationStore) Restore(_ context.Context, _ string) error    { return nil }

// =============================================================================
// Mock authorization store
// =============================================================================

type mockAuthzStore struct {
	created []authorization.CreateRelationship
	err     error
}

func (m *mockAuthzStore) CheckRelationWithGroupExpansion(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, m.err
}
func (m *mockAuthzStore) GetRelationTargets(_ context.Context, _, _, _ string) ([]authorization.RelationTarget, error) {
	return nil, m.err
}
func (m *mockAuthzStore) CheckRelationExists(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, m.err
}
func (m *mockAuthzStore) CheckBatchDirect(_ context.Context, _ string, _ []string, _, _, _ string) (map[string]bool, error) {
	return nil, m.err
}
func (m *mockAuthzStore) CreateRelationships(_ context.Context, rels []authorization.CreateRelationship) error {
	if m.err != nil {
		return m.err
	}
	m.created = append(m.created, rels...)
	return nil
}
func (m *mockAuthzStore) DeleteResourceRelationships(_ context.Context, _, _ string) error {
	return m.err
}
func (m *mockAuthzStore) DeleteRelationship(_ context.Context, _, _, _, _, _ string) error {
	return m.err
}
func (m *mockAuthzStore) DeleteByResourceAndSubject(_ context.Context, _, _, _, _ string) error {
	return m.err
}
func (m *mockAuthzStore) CountByResourceAndRelation(_ context.Context, _, _, _ string) (int, error) {
	return 0, m.err
}
func (m *mockAuthzStore) ListRelationshipsBySubject(_ context.Context, _, _ string, _ authorization.SubjectRelationshipFilter, _ fop.Order, _ fop.PageStringCursor) ([]authorization.SubjectRelationship, fop.Pagination, error) {
	return nil, fop.Pagination{}, m.err
}
func (m *mockAuthzStore) ListRelationshipsByResource(_ context.Context, _, _ string, _ authorization.ResourceRelationshipFilter, _ fop.Order, _ fop.PageStringCursor) ([]authorization.ResourceRelationship, fop.Pagination, error) {
	return nil, fop.Pagination{}, m.err
}
func (m *mockAuthzStore) LookupResourceIDs(_ context.Context, _ string, _ []string, _, _ string) ([]string, error) {
	return nil, m.err
}
func (m *mockAuthzStore) LookupResourceIDsByRelationTarget(_ context.Context, _, _, _ string, _ []string) ([]string, error) {
	return nil, m.err
}

// =============================================================================
// Mock event bus
// =============================================================================

type mockBus struct {
	emitted []events.Event
}

func (m *mockBus) Emit(_ context.Context, event events.Event, _ ...events.EmitOption) error {
	m.emitted = append(m.emitted, event)
	return nil
}

func (m *mockBus) Subscribe(_ string, _ events.Handler) (events.Subscription, error) {
	return nil, nil
}

func (m *mockBus) Close(_ context.Context) error { return nil }

// =============================================================================
// Test helpers
// =============================================================================

func testSchema() authorization.Schema {
	return authorization.NewSchema([]authorization.ResourceSchema{
		{Name: "platform", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
		}},
		{Name: "tenant", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
				"admin":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]authorization.PermissionRule{
				"manage": authorization.AnyOf(authorization.Direct("owner"), authorization.Direct("admin")),
				"read":   authorization.AnyOf(authorization.Direct("owner"), authorization.Direct("admin"), authorization.Direct("member")),
			},
		}},
		{Name: "group", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]authorization.PermissionRule{
				"manage": authorization.AnyOf(authorization.Direct("owner")),
			},
		}},
		{Name: "invitation", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
				"tenant": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "tenant"}}},
				"group":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "group"}}},
			},
		}},
	})
}

type testHarness struct {
	store   *mockInvitationStore
	authz   *mockAuthzStore
	bus     *mockBus
	useCase *Inviter
}

func newTestHarness(opts ...Option) *testHarness {
	store := newMockInvitationStore()
	authzStore := &mockAuthzStore{}
	bus := &mockBus{}

	repo := invitationsrepo.NewRepository(store)
	authorizer := authorization.NewAuthorizer(authzStore, testSchema(), authorization.Config{MaxTraversalDepth: 10})

	allOpts := append([]Option{}, opts...)
	c := NewInviter(repo, authorizer, bus, allOpts...)

	return &testHarness{
		store:   store,
		authz:   authzStore,
		bus:     bus,
		useCase: c,
	}
}

func pendingInvitation(id, resourceType, resourceID, relation, email, tokenHash string, autoAccept bool) invitationsrepo.Invitation {
	return invitationsrepo.Invitation{
		InvitationID:     id,
		ResourceType:     resourceType,
		ResourceID:       resourceID,
		Relation:         relation,
		Identifier:       email,
		IdentifierType:   "email",
		InvitedBy:        "inviter-1",
		TokenHash:        tokenHash,
		AutoAccept:       autoAccept,
		InvitationStatus: "pending",
		ExpiresAt:        time.Now().UTC().Add(7 * 24 * time.Hour),
		RecordState:      "active",
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
}

// =============================================================================
// Create Tests — Four Scenarios
// =============================================================================

func TestCreate_AutoAccept_KnownUser_DirectAdd(t *testing.T) {
	h := newTestHarness(
		WithUserLookup(func(_ context.Context, email string) (string, string, error) {
			if email == "alice@example.com" {
				return "user", "user-alice", nil
			}
			return "", "", nil
		}),
		WithMemberCheck(func(_ context.Context, _, _, _, _ string) (bool, error) {
			return false, nil
		}),
	)

	result, err := h.useCase.Create(context.Background(), CreateInput{
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Relation:     "member",
		Identifier:   "alice@example.com",
		InvitedBy:    "inviter-1",
		AutoAccept:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.DirectlyAdded {
		t.Fatal("expected DirectlyAdded to be true")
	}
	if result.Invitation != nil {
		t.Fatal("expected no invitation record for direct add")
	}

	// Verify relationship was created.
	if len(h.authz.created) == 0 {
		t.Fatal("expected relationship to be created")
	}
	rel := h.authz.created[0]
	if rel.ResourceType != "tenant" || rel.ResourceID != "t-1" || rel.Relation != "member" {
		t.Fatalf("unexpected relationship: %+v", rel)
	}
	if rel.SubjectType != "user" || rel.SubjectID != "user-alice" {
		t.Fatalf("unexpected subject: %s:%s", rel.SubjectType, rel.SubjectID)
	}

	// Verify MemberAddedEvent was emitted.
	if len(h.bus.emitted) != 1 {
		t.Fatalf("expected 1 event, got %d", len(h.bus.emitted))
	}
	if _, ok := h.bus.emitted[0].(MemberAddedEvent); !ok {
		t.Fatalf("expected MemberAddedEvent, got %T", h.bus.emitted[0])
	}

	// Verify no invitation was created in the store.
	if len(h.store.createdInputs) != 0 {
		t.Fatal("expected no invitation to be created in store")
	}
}

func TestCreate_AutoAccept_UnknownUser_PendingWithAutoAccept(t *testing.T) {
	h := newTestHarness(
		WithUserLookup(func(_ context.Context, _ string) (string, string, error) {
			return "", "", nil // user not found
		}),
	)

	result, err := h.useCase.Create(context.Background(), CreateInput{
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Relation:     "member",
		Identifier:   "unknown@example.com",
		InvitedBy:    "inviter-1",
		AutoAccept:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.DirectlyAdded {
		t.Fatal("expected DirectlyAdded to be false")
	}
	if result.Invitation == nil {
		t.Fatal("expected invitation to be created")
	}
	if !result.Invitation.AutoAccept {
		t.Fatal("expected AutoAccept to be true on invitation")
	}
	if result.Invitation.InvitationStatus != "pending" {
		t.Fatalf("expected pending status, got %s", result.Invitation.InvitationStatus)
	}
	if result.Invitation.ResolvedSubjectID != nil {
		t.Fatal("expected resolved_subject_id to be nil for unknown user")
	}

	// Verify InvitationSentEvent was emitted.
	var sentEvent InvitationSentEvent
	for _, e := range h.bus.emitted {
		if se, ok := e.(InvitationSentEvent); ok {
			sentEvent = se
			break
		}
	}
	if sentEvent.InvitationID == "" {
		t.Fatal("expected InvitationSentEvent to be emitted")
	}
	if sentEvent.Token == "" {
		t.Fatal("expected plaintext token in InvitationSentEvent")
	}

	// Verify ReBAC tuples: owner + tenant (relation name matches resource type).
	if len(h.authz.created) != 2 {
		t.Fatalf("expected 2 ReBAC tuples, got %d", len(h.authz.created))
	}
	ownerTuple := h.authz.created[0]
	if ownerTuple.ResourceType != "invitation" || ownerTuple.Relation != "owner" {
		t.Fatalf("expected invitation#owner tuple, got %s#%s", ownerTuple.ResourceType, ownerTuple.Relation)
	}
	if ownerTuple.SubjectType != "user" || ownerTuple.SubjectID != "inviter-1" {
		t.Fatalf("unexpected owner subject: %s:%s", ownerTuple.SubjectType, ownerTuple.SubjectID)
	}
	parentTuple := h.authz.created[1]
	if parentTuple.ResourceType != "invitation" || parentTuple.Relation != "tenant" {
		t.Fatalf("expected invitation#tenant tuple, got %s#%s", parentTuple.ResourceType, parentTuple.Relation)
	}
	if parentTuple.SubjectType != "tenant" || parentTuple.SubjectID != "t-1" {
		t.Fatalf("unexpected parent subject: %s:%s", parentTuple.SubjectType, parentTuple.SubjectID)
	}
}

func TestCreate_PendingInvitation_GroupResource(t *testing.T) {
	h := newTestHarness(
		WithUserLookup(func(_ context.Context, _ string) (string, string, error) {
			return "", "", nil // user not found
		}),
	)

	result, err := h.useCase.Create(context.Background(), CreateInput{
		ResourceType: "group",
		ResourceID:   "g-1",
		Relation:     "member",
		Identifier:   "unknown@example.com",
		InvitedBy:    "inviter-1",
		AutoAccept:   false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Invitation == nil {
		t.Fatal("expected invitation to be created")
	}

	// Verify ReBAC tuples: owner + group (relation name matches resource type).
	if len(h.authz.created) != 2 {
		t.Fatalf("expected 2 ReBAC tuples, got %d", len(h.authz.created))
	}
	ownerTuple := h.authz.created[0]
	if ownerTuple.ResourceType != "invitation" || ownerTuple.Relation != "owner" {
		t.Fatalf("expected invitation#owner tuple, got %s#%s", ownerTuple.ResourceType, ownerTuple.Relation)
	}
	parentTuple := h.authz.created[1]
	if parentTuple.ResourceType != "invitation" || parentTuple.Relation != "group" {
		t.Fatalf("expected invitation#group tuple, got %s#%s", parentTuple.ResourceType, parentTuple.Relation)
	}
	if parentTuple.SubjectType != "group" || parentTuple.SubjectID != "g-1" {
		t.Fatalf("unexpected parent subject: %s:%s", parentTuple.SubjectType, parentTuple.SubjectID)
	}
}

func TestCreate_NoAutoAccept_KnownUser_PendingWithResolvedSubject(t *testing.T) {
	h := newTestHarness(
		WithUserLookup(func(_ context.Context, email string) (string, string, error) {
			if email == "bob@example.com" {
				return "user", "user-bob", nil
			}
			return "", "", nil
		}),
	)

	result, err := h.useCase.Create(context.Background(), CreateInput{
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Relation:     "member",
		Identifier:   "bob@example.com",
		InvitedBy:    "inviter-1",
		AutoAccept:   false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.DirectlyAdded {
		t.Fatal("expected DirectlyAdded to be false — AutoAccept is false")
	}
	if result.Invitation == nil {
		t.Fatal("expected invitation to be created")
	}
	if result.Invitation.AutoAccept {
		t.Fatal("expected AutoAccept to be false on invitation")
	}

	// Key assertion: resolved_subject_id should be set even though AutoAccept is false.
	if result.Invitation.ResolvedSubjectID == nil {
		t.Fatal("expected resolved_subject_id to be set for known user")
	}
	if *result.Invitation.ResolvedSubjectID != "user-bob" {
		t.Fatalf("expected resolved_subject_id=user-bob, got %s", *result.Invitation.ResolvedSubjectID)
	}
}

func TestCreate_NoAutoAccept_UnknownUser_PendingNoSubject(t *testing.T) {
	h := newTestHarness(
		WithUserLookup(func(_ context.Context, _ string) (string, string, error) {
			return "", "", nil
		}),
	)

	result, err := h.useCase.Create(context.Background(), CreateInput{
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Relation:     "member",
		Identifier:   "stranger@example.com",
		InvitedBy:    "inviter-1",
		AutoAccept:   false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.DirectlyAdded {
		t.Fatal("expected DirectlyAdded to be false")
	}
	if result.Invitation == nil {
		t.Fatal("expected invitation to be created")
	}
	if result.Invitation.ResolvedSubjectID != nil {
		t.Fatal("expected resolved_subject_id to be nil for unknown user")
	}
}

func TestCreate_AutoAccept_KnownUser_AlreadyMember(t *testing.T) {
	h := newTestHarness(
		WithUserLookup(func(_ context.Context, _ string) (string, string, error) {
			return "user", "user-alice", nil
		}),
		WithMemberCheck(func(_ context.Context, _, _, _, _ string) (bool, error) {
			return true, nil // already a member
		}),
	)

	_, err := h.useCase.Create(context.Background(), CreateInput{
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Relation:     "member",
		Identifier:   "alice@example.com",
		InvitedBy:    "inviter-1",
		AutoAccept:   true,
	})

	if !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("expected ErrAlreadyMember, got %v", err)
	}
}

func TestCreate_NoLookup_AlwaysCreatesPending(t *testing.T) {
	// No WithUserLookup — all invitations create pending records.
	h := newTestHarness()

	result, err := h.useCase.Create(context.Background(), CreateInput{
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Relation:     "member",
		Identifier:   "anyone@example.com",
		InvitedBy:    "inviter-1",
		AutoAccept:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.DirectlyAdded {
		t.Fatal("expected DirectlyAdded to be false without lookup")
	}
	if result.Invitation == nil {
		t.Fatal("expected invitation to be created")
	}
}

// =============================================================================
// Accept Tests
// =============================================================================

func TestAccept_Success(t *testing.T) {
	h := newTestHarness()

	// Hash a known token so we can look it up.
	token := "test-accept-token-0123456789abcdef"
	tokenHash, _ := h.useCase.hasher.Hash(token)

	h.store.addInvitation(pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", tokenHash, false))

	result, err := h.useCase.Accept(context.Background(), AcceptInput{
		Token:       token,
		SubjectType: "user",
		SubjectID:   "user-alice",
		Identifier:  "alice@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ResourceType != "tenant" || result.ResourceID != "t-1" || result.Relation != "member" {
		t.Fatalf("unexpected result: %+v", result)
	}

	// Verify relationship was created.
	found := false
	for _, rel := range h.authz.created {
		if rel.ResourceType == "tenant" && rel.ResourceID == "t-1" && rel.SubjectID == "user-alice" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected relationship to be created on target resource")
	}

	// Verify invitation was updated to accepted.
	inv := h.store.invitations["inv-1"]
	if inv.InvitationStatus != "accepted" {
		t.Fatalf("expected status=accepted, got %s", inv.InvitationStatus)
	}
	if inv.AcceptedAt == nil {
		t.Fatal("expected accepted_at to be set")
	}
}

func TestAccept_WrongIdentifier(t *testing.T) {
	h := newTestHarness()
	token := "test-wrong-email-token-012345678"
	tokenHash, _ := h.useCase.hasher.Hash(token)

	h.store.addInvitation(pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", tokenHash, false))

	_, err := h.useCase.Accept(context.Background(), AcceptInput{
		Token:       token,
		SubjectType: "user",
		SubjectID:   "user-bob",
		Identifier:  "bob@example.com", // doesn't match
	})

	if !errors.Is(err, ErrIdentifierMismatch) {
		t.Fatalf("expected ErrIdentifierMismatch, got %v", err)
	}
}

func TestAccept_AlreadyAccepted(t *testing.T) {
	h := newTestHarness()
	token := "test-already-accepted-token-0123"
	tokenHash, _ := h.useCase.hasher.Hash(token)

	inv := pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", tokenHash, false)
	inv.InvitationStatus = "accepted"
	h.store.addInvitation(inv)

	_, err := h.useCase.Accept(context.Background(), AcceptInput{
		Token:       token,
		SubjectType: "user",
		SubjectID:   "user-alice",
		Identifier:  "alice@example.com",
	})

	if !errors.Is(err, ErrInvitationAlreadyUsed) {
		t.Fatalf("expected ErrInvitationAlreadyUsed, got %v", err)
	}
}

func TestAccept_Expired(t *testing.T) {
	h := newTestHarness()
	token := "test-expired-token-0123456789ab"
	tokenHash, _ := h.useCase.hasher.Hash(token)

	inv := pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", tokenHash, false)
	inv.ExpiresAt = time.Now().UTC().Add(-1 * time.Hour) // already expired
	h.store.addInvitation(inv)

	_, err := h.useCase.Accept(context.Background(), AcceptInput{
		Token:       token,
		SubjectType: "user",
		SubjectID:   "user-alice",
		Identifier:  "alice@example.com",
	})

	if !errors.Is(err, ErrInvitationExpired) {
		t.Fatalf("expected ErrInvitationExpired, got %v", err)
	}

	// Verify status was updated to expired.
	updated := h.store.invitations["inv-1"]
	if updated.InvitationStatus != "expired" {
		t.Fatalf("expected status to be updated to expired, got %s", updated.InvitationStatus)
	}
}

func TestAccept_Cancelled(t *testing.T) {
	h := newTestHarness()
	token := "test-cancelled-token-012345678"
	tokenHash, _ := h.useCase.hasher.Hash(token)

	inv := pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", tokenHash, false)
	inv.InvitationStatus = "cancelled"
	h.store.addInvitation(inv)

	_, err := h.useCase.Accept(context.Background(), AcceptInput{
		Token:       token,
		SubjectType: "user",
		SubjectID:   "user-alice",
		Identifier:  "alice@example.com",
	})

	if !errors.Is(err, ErrInvitationCancelled) {
		t.Fatalf("expected ErrInvitationCancelled, got %v", err)
	}
}

func TestAccept_InvalidToken(t *testing.T) {
	h := newTestHarness()

	_, err := h.useCase.Accept(context.Background(), AcceptInput{
		Token:       "nonexistent-token-value-here123",
		SubjectType: "user",
		SubjectID:   "user-alice",
		Identifier:  "alice@example.com",
	})

	if !errors.Is(err, ErrInvitationNotFound) {
		t.Fatalf("expected ErrInvitationNotFound, got %v", err)
	}
}

// =============================================================================
// Decline Tests
// =============================================================================

func TestDecline_Success(t *testing.T) {
	h := newTestHarness()
	h.store.addInvitation(pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "hash-1", false))

	err := h.useCase.Decline(context.Background(), "inv-1", "alice@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify invitation was deleted.
	if _, ok := h.store.invitations["inv-1"]; ok {
		t.Fatal("expected invitation to be deleted")
	}
}

func TestDecline_WrongEmail(t *testing.T) {
	h := newTestHarness()
	h.store.addInvitation(pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "hash-1", false))

	err := h.useCase.Decline(context.Background(), "inv-1", "bob@example.com")
	if !errors.Is(err, ErrIdentifierMismatch) {
		t.Fatalf("expected ErrIdentifierMismatch, got %v", err)
	}
}

func TestDecline_NotPending(t *testing.T) {
	h := newTestHarness()
	inv := pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "hash-1", false)
	inv.InvitationStatus = "accepted"
	h.store.addInvitation(inv)

	err := h.useCase.Decline(context.Background(), "inv-1", "alice@example.com")
	if !errors.Is(err, ErrInvitationInvalidStatus) {
		t.Fatalf("expected ErrInvitationInvalidStatus, got %v", err)
	}
}

func TestDecline_NotFound(t *testing.T) {
	h := newTestHarness()

	err := h.useCase.Decline(context.Background(), "nonexistent", "alice@example.com")
	if !errors.Is(err, ErrInvitationNotFound) {
		t.Fatalf("expected ErrInvitationNotFound, got %v", err)
	}
}

// =============================================================================
// Cancel Tests
// =============================================================================

func TestCancel_Success(t *testing.T) {
	h := newTestHarness()
	h.store.addInvitation(pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "hash-1", false))

	err := h.useCase.Cancel(context.Background(), "inv-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := h.store.invitations["inv-1"]; ok {
		t.Fatal("expected invitation to be deleted")
	}
}

func TestCancel_NotPending(t *testing.T) {
	h := newTestHarness()
	inv := pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "hash-1", false)
	inv.InvitationStatus = "accepted"
	h.store.addInvitation(inv)

	err := h.useCase.Cancel(context.Background(), "inv-1")
	if !errors.Is(err, ErrInvitationInvalidStatus) {
		t.Fatalf("expected ErrInvitationInvalidStatus, got %v", err)
	}
}

func TestCancel_NotFound(t *testing.T) {
	h := newTestHarness()

	err := h.useCase.Cancel(context.Background(), "nonexistent")
	if !errors.Is(err, ErrInvitationNotFound) {
		t.Fatalf("expected ErrInvitationNotFound, got %v", err)
	}
}

// =============================================================================
// Resend Tests
// =============================================================================

func TestResend_Success_Pending(t *testing.T) {
	h := newTestHarness()
	h.store.addInvitation(pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "old-hash", false))

	inv, err := h.useCase.Resend(context.Background(), "inv-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be the same invitation ID.
	if inv.InvitationID != "inv-1" {
		t.Fatalf("expected same invitation ID, got %s", inv.InvitationID)
	}

	// Token hash should have changed.
	if inv.TokenHash == "old-hash" {
		t.Fatal("expected token hash to change")
	}

	// Status should be pending.
	if inv.InvitationStatus != "pending" {
		t.Fatalf("expected pending status, got %s", inv.InvitationStatus)
	}

	// Expiry should be in the future.
	if inv.ExpiresAt.Before(time.Now().UTC()) {
		t.Fatal("expected expiry to be in the future")
	}

	// Verify InvitationSentEvent was emitted with new token.
	if len(h.bus.emitted) != 1 {
		t.Fatalf("expected 1 event, got %d", len(h.bus.emitted))
	}
	sentEvent, ok := h.bus.emitted[0].(InvitationSentEvent)
	if !ok {
		t.Fatalf("expected InvitationSentEvent, got %T", h.bus.emitted[0])
	}
	if sentEvent.Token == "" {
		t.Fatal("expected new plaintext token in event")
	}
}

func TestResend_Success_Expired(t *testing.T) {
	h := newTestHarness()

	inv := pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "old-hash", false)
	inv.InvitationStatus = "expired"
	inv.ExpiresAt = time.Now().UTC().Add(-24 * time.Hour)
	h.store.addInvitation(inv)

	updated, err := h.useCase.Resend(context.Background(), "inv-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Status should be reset to pending.
	if updated.InvitationStatus != "pending" {
		t.Fatalf("expected pending status after resend, got %s", updated.InvitationStatus)
	}

	// Expiry should be reset.
	if updated.ExpiresAt.Before(time.Now().UTC()) {
		t.Fatal("expected expiry to be in the future after resend")
	}
}

func TestResend_InvalidStatus(t *testing.T) {
	h := newTestHarness()

	inv := pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "hash-1", false)
	inv.InvitationStatus = "accepted"
	h.store.addInvitation(inv)

	_, err := h.useCase.Resend(context.Background(), "inv-1")
	if !errors.Is(err, ErrInvitationInvalidStatus) {
		t.Fatalf("expected ErrInvitationInvalidStatus, got %v", err)
	}
}

func TestResend_NotFound(t *testing.T) {
	h := newTestHarness()

	_, err := h.useCase.Resend(context.Background(), "nonexistent")
	if !errors.Is(err, ErrInvitationNotFound) {
		t.Fatalf("expected ErrInvitationNotFound, got %v", err)
	}
}

// =============================================================================
// ResolveOnRegistration Tests
// =============================================================================

func TestResolveOnRegistration_AutoAcceptOnly(t *testing.T) {
	h := newTestHarness()

	// Auto-accept invitation — should be resolved.
	h.store.addInvitation(pendingInvitation("inv-auto", "tenant", "t-1", "member", "alice@example.com", "hash-1", true))

	// Non-auto-accept invitation — should NOT be resolved.
	h.store.addInvitation(pendingInvitation("inv-manual", "tenant", "t-2", "member", "alice@example.com", "hash-2", false))

	resolved, err := h.useCase.ResolveOnRegistration(context.Background(), "alice@example.com", IdentifierTypeEmail, "user", "user-alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolved != 1 {
		t.Fatalf("expected 1 resolved, got %d", resolved)
	}

	// Auto-accept invitation should be accepted.
	autoInv := h.store.invitations["inv-auto"]
	if autoInv.InvitationStatus != "accepted" {
		t.Fatalf("expected auto-accept invitation to be accepted, got %s", autoInv.InvitationStatus)
	}

	// Manual invitation should still be pending.
	manualInv := h.store.invitations["inv-manual"]
	if manualInv.InvitationStatus != "pending" {
		t.Fatalf("expected manual invitation to still be pending, got %s", manualInv.InvitationStatus)
	}

	// Verify relationship was created for auto-accept.
	found := false
	for _, rel := range h.authz.created {
		if rel.ResourceType == "tenant" && rel.ResourceID == "t-1" && rel.SubjectID == "user-alice" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected relationship to be created for auto-accept invitation")
	}
}

func TestResolveOnRegistration_SkipsExpired(t *testing.T) {
	h := newTestHarness()

	inv := pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "hash-1", true)
	inv.ExpiresAt = time.Now().UTC().Add(-1 * time.Hour) // expired
	h.store.addInvitation(inv)

	resolved, err := h.useCase.ResolveOnRegistration(context.Background(), "alice@example.com", IdentifierTypeEmail, "user", "user-alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolved != 0 {
		t.Fatalf("expected 0 resolved (expired), got %d", resolved)
	}
}

func TestResolveOnRegistration_NoInvitations(t *testing.T) {
	h := newTestHarness()

	resolved, err := h.useCase.ResolveOnRegistration(context.Background(), "nobody@example.com", IdentifierTypeEmail, "user", "user-nobody")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolved != 0 {
		t.Fatalf("expected 0 resolved, got %d", resolved)
	}
}

func TestResolveOnRegistration_MultipleInvitations(t *testing.T) {
	h := newTestHarness()

	h.store.addInvitation(pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "hash-1", true))
	h.store.addInvitation(pendingInvitation("inv-2", "tenant", "t-2", "admin", "alice@example.com", "hash-2", true))

	resolved, err := h.useCase.ResolveOnRegistration(context.Background(), "alice@example.com", IdentifierTypeEmail, "user", "user-alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolved != 2 {
		t.Fatalf("expected 2 resolved, got %d", resolved)
	}

	for _, id := range []string{"inv-1", "inv-2"} {
		inv := h.store.invitations[id]
		if inv.InvitationStatus != "accepted" {
			t.Fatalf("expected %s to be accepted, got %s", id, inv.InvitationStatus)
		}
	}
}

// =============================================================================
// RedirectURL Tests
// =============================================================================

func TestCreate_PropagatesRedirectURL(t *testing.T) {
	h := newTestHarness(
		WithUserLookup(func(_ context.Context, _ string) (string, string, error) {
			return "", "", nil
		}),
	)

	redirectURL := "https://example.com/accept"
	result, err := h.useCase.Create(context.Background(), CreateInput{
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Relation:     "member",
		Identifier:   "alice@example.com",
		InvitedBy:    "inviter-1",
		AutoAccept:   false,
		RedirectURL:  redirectURL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Invitation == nil {
		t.Fatal("expected invitation to be created")
	}

	// Verify RedirectURL was persisted.
	if len(h.store.createdInputs) != 1 {
		t.Fatalf("expected 1 create, got %d", len(h.store.createdInputs))
	}
	if h.store.createdInputs[0].RedirectURL == nil || *h.store.createdInputs[0].RedirectURL != redirectURL {
		t.Fatalf("expected RedirectURL %q in repo input, got %v", redirectURL, h.store.createdInputs[0].RedirectURL)
	}

	// Verify event carries RedirectURL.
	var sentEvent InvitationSentEvent
	for _, e := range h.bus.emitted {
		if se, ok := e.(InvitationSentEvent); ok {
			sentEvent = se
			break
		}
	}
	if sentEvent.InvitationID == "" {
		t.Fatal("expected InvitationSentEvent to be emitted")
	}
	if sentEvent.RedirectURL != redirectURL {
		t.Fatalf("expected RedirectURL %q in event, got %q", redirectURL, sentEvent.RedirectURL)
	}
}

func TestCreate_EmptyRedirectURL(t *testing.T) {
	h := newTestHarness(
		WithUserLookup(func(_ context.Context, _ string) (string, string, error) {
			return "", "", nil
		}),
	)

	result, err := h.useCase.Create(context.Background(), CreateInput{
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Relation:     "member",
		Identifier:   "alice@example.com",
		InvitedBy:    "inviter-1",
		AutoAccept:   false,
		RedirectURL:  "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Invitation == nil {
		t.Fatal("expected invitation to be created")
	}

	// Verify RedirectURL was not set (nil pointer).
	if len(h.store.createdInputs) != 1 {
		t.Fatalf("expected 1 create, got %d", len(h.store.createdInputs))
	}
	if h.store.createdInputs[0].RedirectURL != nil {
		t.Fatalf("expected nil RedirectURL for empty input, got %v", h.store.createdInputs[0].RedirectURL)
	}

	// Verify event carries empty RedirectURL.
	var sentEvent InvitationSentEvent
	for _, e := range h.bus.emitted {
		if se, ok := e.(InvitationSentEvent); ok {
			sentEvent = se
			break
		}
	}
	if sentEvent.RedirectURL != "" {
		t.Fatalf("expected empty RedirectURL in event, got %q", sentEvent.RedirectURL)
	}
}

func TestResend_RestoresRedirectURL(t *testing.T) {
	h := newTestHarness()

	redirectURL := "https://example.com/accept"
	inv := pendingInvitation("inv-1", "tenant", "t-1", "member", "alice@example.com", "old-hash", false)
	inv.RedirectURL = &redirectURL
	h.store.addInvitation(inv)

	_, err := h.useCase.Resend(context.Background(), "inv-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify InvitationSentEvent carries the persisted RedirectURL.
	if len(h.bus.emitted) != 1 {
		t.Fatalf("expected 1 event, got %d", len(h.bus.emitted))
	}
	sentEvent, ok := h.bus.emitted[0].(InvitationSentEvent)
	if !ok {
		t.Fatalf("expected InvitationSentEvent, got %T", h.bus.emitted[0])
	}
	if sentEvent.RedirectURL != redirectURL {
		t.Fatalf("expected RedirectURL %q in resend event, got %q", redirectURL, sentEvent.RedirectURL)
	}
}

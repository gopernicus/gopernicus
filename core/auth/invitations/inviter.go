// Package invitations provides generic resource invitation business logic.
//
// Invitations allow users to invite others to resources via email. The flow:
//  1. Create invitation → token generated → InvitationSentEvent emitted
//  2. Invitee clicks link → token verified → ReBAC relationship created
//  3. Invitation marked as accepted
//
// If the invitee already exists in the system, the relationship is created
// directly without generating a pending invitation.
package invitations

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authorization"
	invitationsrepo "github.com/gopernicus/gopernicus/core/repositories/rebac/invitations"
	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// Token alphabet — URL-safe, no visually ambiguous characters.
const tokenAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
const tokenLength = 32

// InvitationExpiryDays is the default number of days before an invitation expires.
const InvitationExpiryDays = 7

// Identifier types for invitations.
const (
	IdentifierTypeEmail = "email"
	IdentifierTypePhone = "phone"
)

// Invitation status values.
const (
	StatusPending  = "pending"
	StatusAccepted = "accepted"
	StatusDeclined = "declined"
	StatusCancelled = "cancelled"
	StatusExpired  = "expired"
)

// =============================================================================
// Dependencies
// =============================================================================

// UserLookup resolves an email to a subject for the "direct add" path.
// Return ("", "") if the user doesn't exist. The use case will create
// a pending invitation in that case.
type UserLookup func(ctx context.Context, email string) (subjectType, subjectID string, err error)

// MemberCheck checks if a subject is already a member of a resource.
type MemberCheck func(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) (bool, error)

// =============================================================================
// Case
// =============================================================================

// Inviter provides invitation business logic.
type Inviter struct {
	invitations *invitationsrepo.Repository
	authorizer  *authorization.Authorizer
	hasher      *cryptids.SHA256Hasher
	bus         events.Bus
	lookupUser  UserLookup
	checkMember MemberCheck
}

// Option configures a Inviter.
type Option func(*Inviter)

// WithUserLookup enables the "direct add" path for existing users.
// Without this, all invitations create pending records regardless.
func WithUserLookup(fn UserLookup) Option {
	return func(c *Inviter) { c.lookupUser = fn }
}

// WithMemberCheck enables duplicate membership detection.
func WithMemberCheck(fn MemberCheck) Option {
	return func(c *Inviter) { c.checkMember = fn }
}

// NewInviter creates a new invitation Inviter.
func NewInviter(
	invitationRepo *invitationsrepo.Repository,
	authorizer *authorization.Authorizer,
	bus events.Bus,
	opts ...Option,
) *Inviter {
	c := &Inviter{
		invitations: invitationRepo,
		authorizer:  authorizer,
		hasher:      cryptids.NewSHA256Hasher(),
		bus:         bus,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// =============================================================================
// Create Invitation
// =============================================================================

// CreateInput is the input for creating an invitation.
type CreateInput struct {
	ResourceType   string
	ResourceID     string
	Relation       string // role to grant on acceptance (e.g., "member", "admin")
	Identifier     string // identifier value (e.g. email address, phone number)
	IdentifierType string // type of identifier: IdentifierTypeEmail, IdentifierTypePhone, etc.
	InvitedBy      string // principal ID of the inviter
	AutoAccept     bool   // when true: known user → direct add; unknown user → auto-accept on identifier verification
}

// CreateResult is the result of creating an invitation.
type CreateResult struct {
	// DirectlyAdded is true if the user already existed and was added directly.
	DirectlyAdded bool

	// Invitation is set when a pending invitation was created (DirectlyAdded == false).
	Invitation *invitationsrepo.Invitation
}

// Create creates an invitation to a resource.
//
// Behavior depends on AutoAccept and whether the user exists:
//
//   - AutoAccept + known user → direct add (relationship created immediately)
//   - AutoAccept + unknown user → pending invitation, auto-accepted on email verification
//   - !AutoAccept + known user → pending invitation (user must explicitly accept)
//   - !AutoAccept + unknown user → pending invitation (user must register then accept)
func (c *Inviter) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	// Always resolve the subject if possible — sets resolved_subject_id
	// so the invitation appears in /mine immediately.
	var resolvedSubjectType, resolvedSubjectID string
	if c.lookupUser != nil {
		st, sid, err := c.lookupUser(ctx, input.Identifier)
		if err != nil {
			return CreateResult{}, fmt.Errorf("lookup user: %w", err)
		}
		resolvedSubjectType, resolvedSubjectID = st, sid
	}

	// AutoAccept + known user → direct add (no invitation record).
	if input.AutoAccept && resolvedSubjectID != "" {
		return c.addExistingUser(ctx, input, resolvedSubjectType, resolvedSubjectID)
	}

	return c.createPendingInvitation(ctx, input, resolvedSubjectID)
}

func (c *Inviter) addExistingUser(ctx context.Context, input CreateInput, subjectType, subjectID string) (CreateResult, error) {
	// Check if already a member.
	if c.checkMember != nil {
		isMember, err := c.checkMember(ctx, input.ResourceType, input.ResourceID, subjectType, subjectID)
		if err != nil {
			return CreateResult{}, fmt.Errorf("check member: %w", err)
		}
		if isMember {
			return CreateResult{}, ErrAlreadyMember
		}
	}

	// Create the relationship directly.
	if err := c.authorizer.CreateRelationships(ctx, []authorization.CreateRelationship{{
		ResourceType: input.ResourceType,
		ResourceID:   input.ResourceID,
		Relation:     input.Relation,
		SubjectType:  subjectType,
		SubjectID:    subjectID,
	}}); err != nil {
		return CreateResult{}, fmt.Errorf("create relationship: %w", err)
	}

	if c.bus != nil {
		c.bus.Emit(ctx, MemberAddedEvent{
			BaseEvent:    events.NewBaseEvent("member.added"),
			ResourceType: input.ResourceType,
			ResourceID:   input.ResourceID,
			Relation:     input.Relation,
			SubjectType:  subjectType,
			SubjectID:    subjectID,
			AddedBy:      input.InvitedBy,
		})
	}

	return CreateResult{DirectlyAdded: true}, nil
}

func (c *Inviter) createPendingInvitation(ctx context.Context, input CreateInput, resolvedSubjectID string) (CreateResult, error) {
	// Generate token.
	token, err := cryptids.GenerateCustomID(tokenAlphabet, tokenLength)
	if err != nil {
		return CreateResult{}, fmt.Errorf("generate token: %w", err)
	}

	tokenHash, err := c.hasher.Hash(token)
	if err != nil {
		return CreateResult{}, fmt.Errorf("hash token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(time.Duration(InvitationExpiryDays) * 24 * time.Hour)

	createInput := invitationsrepo.CreateInvitation{
		ResourceType:     input.ResourceType,
		ResourceID:       input.ResourceID,
		Relation:         input.Relation,
		Identifier:       input.Identifier,
		IdentifierType:   identifierTypeOrDefault(input.IdentifierType),
		InvitedBy:        input.InvitedBy,
		TokenHash:        tokenHash,
		AutoAccept:       input.AutoAccept,
		InvitationStatus: StatusPending,
		ExpiresAt:        expiresAt,
		RecordState:      "active",
	}
	if resolvedSubjectID != "" {
		createInput.ResolvedSubjectID = &resolvedSubjectID
	}

	inv, err := c.invitations.Create(ctx, createInput)
	if err != nil {
		return CreateResult{}, fmt.Errorf("create invitation: %w", err)
	}

	// Create ReBAC tuples for the invitation itself.
	if err := c.authorizer.CreateRelationships(ctx, []authorization.CreateRelationship{
		{
			ResourceType: "invitation",
			ResourceID:   inv.InvitationID,
			Relation:     "owner",
			SubjectType:  "user",
			SubjectID:    input.InvitedBy,
		},
		{
			ResourceType: "invitation",
			ResourceID:   inv.InvitationID,
			Relation:     input.ResourceType,
			SubjectType:  input.ResourceType,
			SubjectID:    input.ResourceID,
		},
	}); err != nil {
		return CreateResult{}, fmt.Errorf("create invitation relationships: %w", err)
	}

	// Emit event with plaintext token (only time it's available).
	if c.bus != nil {
		c.bus.Emit(ctx, InvitationSentEvent{
			BaseEvent:    events.NewBaseEvent("invitation.sent"),
			InvitationID: inv.InvitationID,
			ResourceType: input.ResourceType,
			ResourceID:   input.ResourceID,
			Relation:     input.Relation,
			Identifier:   input.Identifier,
			Token:        token,
			InvitedBy:    input.InvitedBy,
			AutoAccept:   input.AutoAccept,
		})
	}

	return CreateResult{Invitation: &inv}, nil
}

// =============================================================================
// Accept Invitation
// =============================================================================

// AcceptInput is the input for accepting an invitation.
type AcceptInput struct {
	Token       string // plaintext token from the invitation link
	SubjectType string // type of the accepting subject (e.g., "user")
	SubjectID   string // ID of the accepting subject
	Identifier  string // identifier of the accepting subject (must match invitation identifier)
}

// AcceptResult is the result of accepting an invitation.
type AcceptResult struct {
	ResourceType string
	ResourceID   string
	Relation     string
}

// Accept verifies a token and creates the ReBAC relationship on the target resource.
func (c *Inviter) Accept(ctx context.Context, input AcceptInput) (AcceptResult, error) {
	tokenHash, err := c.hasher.Hash(input.Token)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("hash token: %w", err)
	}

	inv, err := c.invitations.GetByToken(ctx, tokenHash, time.Now().UTC())
	if err != nil {
		return AcceptResult{}, ErrInvitationNotFound
	}

	// Validate status.
	switch inv.InvitationStatus {
	case StatusPending:
		// OK, proceed.
	case StatusAccepted:
		return AcceptResult{}, ErrInvitationAlreadyUsed
	case StatusCancelled:
		return AcceptResult{}, ErrInvitationCancelled
	case StatusExpired:
		return AcceptResult{}, ErrInvitationExpired
	default:
		return AcceptResult{}, ErrInvitationInvalidStatus
	}

	// Check expiry.
	if time.Now().UTC().After(inv.ExpiresAt) {
		expired := StatusExpired
		c.invitations.Update(ctx, inv.InvitationID, invitationsrepo.UpdateInvitation{
			InvitationStatus: &expired,
		})
		return AcceptResult{}, ErrInvitationExpired
	}

	// Verify identifier matches.
	if inv.Identifier != input.Identifier {
		return AcceptResult{}, ErrIdentifierMismatch
	}

	// Create the relationship on the target resource.
	if err := c.authorizer.CreateRelationships(ctx, []authorization.CreateRelationship{{
		ResourceType: inv.ResourceType,
		ResourceID:   inv.ResourceID,
		Relation:     inv.Relation,
		SubjectType:  input.SubjectType,
		SubjectID:    input.SubjectID,
	}}); err != nil {
		return AcceptResult{}, fmt.Errorf("create relationship: %w", err)
	}

	// Update invitation status.
	now := time.Now().UTC()
	accepted := StatusAccepted
	if _, err := c.invitations.Update(ctx, inv.InvitationID, invitationsrepo.UpdateInvitation{
		InvitationStatus:  &accepted,
		AcceptedAt:        &now,
		ResolvedSubjectID: &input.SubjectID,
	}); err != nil {
		return AcceptResult{}, fmt.Errorf("update invitation: %w", err)
	}

	return AcceptResult{
		ResourceType: inv.ResourceType,
		ResourceID:   inv.ResourceID,
		Relation:     inv.Relation,
	}, nil
}

// =============================================================================
// Decline Invitation
// =============================================================================

// Decline deletes a pending invitation that the invitee does not want.
func (c *Inviter) Decline(ctx context.Context, invitationID, identifier string) error {
	inv, err := c.invitations.Get(ctx, invitationID)
	if err != nil {
		return ErrInvitationNotFound
	}

	if inv.InvitationStatus != StatusPending {
		return ErrInvitationInvalidStatus
	}

	if inv.Identifier != identifier {
		return ErrIdentifierMismatch
	}

	// Hard delete the invitation and its ReBAC relationships.
	if err := c.invitations.Delete(ctx, invitationID); err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}

	if err := c.authorizer.DeleteResourceRelationships(ctx, "invitation", invitationID); err != nil {
		return fmt.Errorf("delete invitation relationships: %w", err)
	}

	return nil
}

// =============================================================================
// Cancel Invitation
// =============================================================================

// Cancel hard-deletes a pending invitation and cleans up its ReBAC relationships.
func (c *Inviter) Cancel(ctx context.Context, invitationID string) error {
	inv, err := c.invitations.Get(ctx, invitationID)
	if err != nil {
		return ErrInvitationNotFound
	}

	if inv.InvitationStatus != StatusPending {
		return ErrInvitationInvalidStatus
	}

	// Hard delete the invitation and its ReBAC relationships.
	if err := c.invitations.Delete(ctx, invitationID); err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}

	if err := c.authorizer.DeleteResourceRelationships(ctx, "invitation", invitationID); err != nil {
		return fmt.Errorf("delete invitation relationships: %w", err)
	}

	return nil
}

// =============================================================================
// Resend Invitation
// =============================================================================

// Resend regenerates the token and resets the expiry on an existing invitation.
// The same invitation record is updated in-place — no new ID is created.
func (c *Inviter) Resend(ctx context.Context, invitationID string) (*invitationsrepo.Invitation, error) {
	inv, err := c.invitations.Get(ctx, invitationID)
	if err != nil {
		return nil, ErrInvitationNotFound
	}

	if inv.InvitationStatus != StatusPending && inv.InvitationStatus != StatusExpired {
		return nil, ErrInvitationInvalidStatus
	}

	// Generate a new token.
	token, err := cryptids.GenerateCustomID(tokenAlphabet, tokenLength)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	tokenHash, err := c.hasher.Hash(token)
	if err != nil {
		return nil, fmt.Errorf("hash token: %w", err)
	}

	// Reset expiry and ensure status is pending (in case it was expired).
	expiresAt := time.Now().UTC().Add(time.Duration(InvitationExpiryDays) * 24 * time.Hour)
	pending := StatusPending

	updated, err := c.invitations.Update(ctx, invitationID, invitationsrepo.UpdateInvitation{
		TokenHash:        &tokenHash,
		InvitationStatus: &pending,
		ExpiresAt:        &expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("update invitation: %w", err)
	}

	// Emit event with the new plaintext token.
	if c.bus != nil {
		c.bus.Emit(ctx, InvitationSentEvent{
			BaseEvent:    events.NewBaseEvent("invitation.sent"),
			InvitationID: inv.InvitationID,
			ResourceType: inv.ResourceType,
			ResourceID:   inv.ResourceID,
			Relation:     inv.Relation,
			Identifier:   inv.Identifier,
			Token:        token,
			InvitedBy:    inv.InvitedBy,
			AutoAccept:   inv.AutoAccept,
		})
	}

	return &updated, nil
}

// =============================================================================
// List Invitations
// =============================================================================

// ListByResource returns paginated invitations for a resource.
func (c *Inviter) ListByResource(
	ctx context.Context,
	resourceType, resourceID string,
	filter invitationsrepo.FilterListByResource,
	orderBy fop.Order,
	page fop.PageStringCursor,
) ([]invitationsrepo.Invitation, fop.Pagination, error) {
	return c.invitations.ListByResource(ctx, filter, resourceType, resourceID, orderBy, page)
}

// ListBySubject returns paginated invitations for an authenticated subject.
func (c *Inviter) ListBySubject(
	ctx context.Context,
	resolvedSubjectID string,
	filter invitationsrepo.FilterListBySubject,
	orderBy fop.Order,
	page fop.PageStringCursor,
) ([]invitationsrepo.Invitation, fop.Pagination, error) {
	return c.invitations.ListBySubject(ctx, filter, resolvedSubjectID, orderBy, page)
}

// ListByIdentifier returns paginated invitations for an identifier (e.g. email).
func (c *Inviter) ListByIdentifier(
	ctx context.Context,
	identifier, identifierType string,
	filter invitationsrepo.FilterListByIdentifier,
	orderBy fop.Order,
	page fop.PageStringCursor,
) ([]invitationsrepo.Invitation, fop.Pagination, error) {
	return c.invitations.ListByIdentifier(ctx, filter, identifier, identifierType, time.Now().UTC(), orderBy, page)
}

// =============================================================================
// Resolve on Registration
// =============================================================================

// ResolveOnRegistration accepts all pending auto-accept invitations for an
// identifier (e.g. email) when a user verifies their identifier. Only
// invitations created with AutoAccept=true are resolved; regular invitations
// require explicit acceptance.
func (c *Inviter) ResolveOnRegistration(ctx context.Context, identifier, identifierType, subjectType, subjectID string) (int, error) {
	pending := StatusPending
	autoAccept := true
	filter := invitationsrepo.FilterListByIdentifier{
		InvitationStatus: &pending,
		AutoAccept:       &autoAccept,
	}

	invs, _, err := c.invitations.ListByIdentifier(ctx, filter, identifier, identifierTypeOrDefault(identifierType), time.Now().UTC(), fop.Order{}, fop.PageStringCursor{Limit: 100})
	if err != nil {
		return 0, fmt.Errorf("list pending invitations: %w", err)
	}

	resolved := 0
	for _, inv := range invs {
		// Check not expired.
		if time.Now().UTC().After(inv.ExpiresAt) {
			continue
		}

		// Create the relationship.
		if err := c.authorizer.CreateRelationships(ctx, []authorization.CreateRelationship{{
			ResourceType: inv.ResourceType,
			ResourceID:   inv.ResourceID,
			Relation:     inv.Relation,
			SubjectType:  subjectType,
			SubjectID:    subjectID,
		}}); err != nil {
			continue // best effort — don't fail the registration
		}

		// Mark as accepted.
		now := time.Now().UTC()
		accepted := StatusAccepted
		c.invitations.Update(ctx, inv.InvitationID, invitationsrepo.UpdateInvitation{
			InvitationStatus:  &accepted,
			AcceptedAt:        &now,
			ResolvedSubjectID: &subjectID,
		})

		resolved++
	}

	return resolved, nil
}

func identifierTypeOrDefault(t string) string {
	if t == "" {
		return IdentifierTypeEmail
	}
	return t
}

package invitations

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/fop"
)

// ---------------------------------------------------------------------------
// OrderBy constants — user-facing sort field names for invitation listings.
// Field-identical to the generated invitations repository so adapters are
// plain struct conversions.
// ---------------------------------------------------------------------------

const (
	OrderByPK                = "invitation_id"
	OrderByResourceType      = "resource_type"
	OrderByResourceID        = "resource_id"
	OrderByRelation          = "relation"
	OrderByIdentifier        = "identifier"
	OrderByIdentifierType    = "identifier_type"
	OrderByResolvedSubjectID = "resolved_subject_id"
	OrderByInvitedBy         = "invited_by"
	OrderByTokenHash         = "token_hash"
	OrderByAutoAccept        = "auto_accept"
	OrderByInvitationStatus  = "invitation_status"
	OrderByExpiresAt         = "expires_at"
	OrderByAcceptedAt        = "accepted_at"
	OrderByRecordState       = "record_state"
	OrderByCreatedAt         = "created_at"
	OrderByUpdatedAt         = "updated_at"
	OrderByRedirectURL       = "redirect_url"
)

// Max page limits for the list operations the engine exposes.
const (
	MaxLimitListByResource   = 100
	MaxLimitListBySubject    = 100
	MaxLimitListByIdentifier = 100
)

// OrderByFields maps user-facing field names to OrderField definitions.
var OrderByFields = map[string]fop.OrderField{
	OrderByPK:                {Column: "invitation_id"},
	OrderByResourceType:      {Column: "resource_type", CastLower: true},
	OrderByResourceID:        {Column: "resource_id", CastLower: true},
	OrderByRelation:          {Column: "relation", CastLower: true},
	OrderByIdentifier:        {Column: "identifier", CastLower: true},
	OrderByIdentifierType:    {Column: "identifier_type", CastLower: true},
	OrderByResolvedSubjectID: {Column: "resolved_subject_id", CastLower: true},
	OrderByInvitedBy:         {Column: "invited_by", CastLower: true},
	OrderByTokenHash:         {Column: "token_hash", CastLower: true},
	OrderByAutoAccept:        {Column: "auto_accept"},
	OrderByInvitationStatus:  {Column: "invitation_status", CastLower: true},
	OrderByExpiresAt:         {Column: "expires_at"},
	OrderByAcceptedAt:        {Column: "accepted_at"},
	OrderByRecordState:       {Column: "record_state", CastLower: true},
	OrderByCreatedAt:         {Column: "created_at"},
	OrderByUpdatedAt:         {Column: "updated_at"},
	OrderByRedirectURL:       {Column: "redirect_url", CastLower: true},
}

// ---------------------------------------------------------------------------
// Repository interface — implemented by the user's generated invitations
// repository via a satisfier (see satisfiers/).
// ---------------------------------------------------------------------------

// InvitationRepository provides the invitation persistence the engine needs.
// Method signatures mirror the generated invitations repository so a
// satisfier can delegate with plain struct conversions.
type InvitationRepository interface {
	Create(ctx context.Context, input CreateInvitation) (Invitation, error)
	Get(ctx context.Context, invitationID string) (Invitation, error)
	GetByToken(ctx context.Context, tokenHash string, now time.Time) (Invitation, error)
	Update(ctx context.Context, invitationID string, input UpdateInvitation) (Invitation, error)
	Delete(ctx context.Context, invitationID string) error
	ListByResource(ctx context.Context, filter FilterListByResource, resourceType string, resourceID string, orderBy fop.Order, page fop.PageStringCursor) ([]Invitation, fop.Pagination, error)
	ListBySubject(ctx context.Context, filter FilterListBySubject, resolvedSubjectID string, orderBy fop.Order, page fop.PageStringCursor) ([]Invitation, fop.Pagination, error)
	ListByIdentifier(ctx context.Context, filter FilterListByIdentifier, identifier string, identifierType string, now time.Time, orderBy fop.Order, page fop.PageStringCursor) ([]Invitation, fop.Pagination, error)
}

// ---------------------------------------------------------------------------
// Core types — field-identical to the generated invitations repository so
// satisfiers can convert with a plain struct conversion.
// ---------------------------------------------------------------------------

// Invitation is the invitation record the engine operates on.
type Invitation struct {
	InvitationID      string
	ResourceType      string
	ResourceID        string
	Relation          string
	Identifier        string
	IdentifierType    string
	ResolvedSubjectID *string
	InvitedBy         string
	TokenHash         string
	AutoAccept        bool
	InvitationStatus  string
	ExpiresAt         time.Time
	AcceptedAt        *time.Time
	RecordState       string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	RedirectURL       *string
}

// CreateInvitation is the input for creating an invitation.
type CreateInvitation struct {
	InvitationID      string
	ResourceType      string
	ResourceID        string
	Relation          string
	Identifier        string
	IdentifierType    string
	ResolvedSubjectID *string
	InvitedBy         string
	TokenHash         string
	AutoAccept        bool
	InvitationStatus  string
	ExpiresAt         time.Time
	AcceptedAt        *time.Time
	RecordState       string
	RedirectURL       *string
}

// UpdateInvitation is the input for updating an invitation.
type UpdateInvitation struct {
	ResourceType      *string
	ResourceID        *string
	Relation          *string
	Identifier        *string
	IdentifierType    *string
	ResolvedSubjectID *string
	InvitedBy         *string
	TokenHash         *string
	AutoAccept        *bool
	InvitationStatus  *string
	ExpiresAt         *time.Time
	AcceptedAt        *time.Time
	UpdatedAt         *time.Time
	RedirectURL       *string
}

// FilterListByResource holds filter criteria for ListByResource.
type FilterListByResource struct {
	InvitationStatus *string
	Relation         *string
	AutoAccept       *bool
	SearchTerm       *string

	// AuthorizedIDs restricts results to the given primary key values.
	// Set by the bridge layer for prefilter authorization.
	// Nil means unrestricted (admin bypass). Empty non-nil means no access.
	AuthorizedIDs []string
}

// FilterListBySubject holds filter criteria for ListBySubject.
type FilterListBySubject struct {
	ResourceType     *string
	InvitationStatus *string
	Relation         *string
	AutoAccept       *bool
	SearchTerm       *string

	// AuthorizedIDs restricts results to the given primary key values.
	// Set by the bridge layer for prefilter authorization.
	// Nil means unrestricted (admin bypass). Empty non-nil means no access.
	AuthorizedIDs []string
}

// FilterListByIdentifier holds filter criteria for ListByIdentifier.
type FilterListByIdentifier struct {
	InvitationStatus *string
	AutoAccept       *bool
	SearchTerm       *string

	// AuthorizedIDs restricts results to the given primary key values.
	// Set by the bridge layer for prefilter authorization.
	// Nil means unrestricted (admin bypass). Empty non-nil means no access.
	AuthorizedIDs []string
}

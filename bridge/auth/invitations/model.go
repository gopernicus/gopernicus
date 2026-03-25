package invitations

import (
	"time"

	"github.com/gopernicus/gopernicus/sdk/validation"
)

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// CreateInvitationRequest is the JSON body for POST /{resource_type}/{resource_id}.
// Resource type and ID come from the URL path.
type CreateInvitationRequest struct {
	Relation       string `json:"relation"`
	Identifier     string `json:"identifier"`      // identifier value (email, phone, etc.)
	IdentifierType string `json:"identifier_type"` // "email" (default), "phone", etc.
	AutoAccept     bool   `json:"auto_accept"`     // true = direct add (known) or auto-accept on verification (unknown)
}

func (r *CreateInvitationRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("relation", r.Relation))
	errs.Add(validation.Required("identifier", r.Identifier))
	return errs.Err()
}

// AcceptInvitationRequest is the JSON body for POST /accept.
type AcceptInvitationRequest struct {
	Token string `json:"token"`
}

func (r *AcceptInvitationRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("token", r.Token))
	return errs.Err()
}

// DeclineInvitationRequest is the JSON body for POST /{invitation_id}/decline.
type DeclineInvitationRequest struct {
	Identifier string `json:"identifier"`
}

func (r *DeclineInvitationRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("identifier", r.Identifier))
	return errs.Err()
}

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

// InvitationResponse is the JSON response for a single invitation.
type InvitationResponse struct {
	InvitationID   string     `json:"invitation_id"`
	ResourceType   string     `json:"resource_type"`
	ResourceID     string     `json:"resource_id"`
	Relation       string     `json:"relation"`
	Identifier     string     `json:"identifier"`
	IdentifierType string     `json:"identifier_type"`
	InvitedBy      string     `json:"invited_by"`
	Status         string     `json:"status"`
	ExpiresAt      time.Time  `json:"expires_at"`
	AcceptedAt     *time.Time `json:"accepted_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// CreateInvitationResponse is the JSON response for creating an invitation.
type CreateInvitationResponse struct {
	DirectlyAdded bool                `json:"directly_added"`
	Invitation    *InvitationResponse `json:"invitation,omitempty"`
}

// AcceptInvitationResponse is the JSON response for accepting an invitation.
type AcceptInvitationResponse struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Relation     string `json:"relation"`
}

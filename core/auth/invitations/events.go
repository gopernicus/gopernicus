package invitations

import "github.com/gopernicus/gopernicus/infrastructure/events"

// InvitationSentEvent is emitted when an invitation email should be sent.
// Subscribe to this event to deliver the invitation email.
type InvitationSentEvent struct {
	events.BaseEvent

	InvitationID string `json:"invitation_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Relation     string `json:"relation"`
	Identifier   string `json:"identifier"` // email address
	Token        string `json:"token"`       // plaintext token (only available at creation time)
	InvitedBy    string `json:"invited_by"`
}

func (e InvitationSentEvent) Type() string { return "invitation.sent" }

// MemberAddedEvent is emitted when an existing user is added directly
// (no invitation needed because the user already exists in the system).
type MemberAddedEvent struct {
	events.BaseEvent

	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Relation     string `json:"relation"`
	SubjectType  string `json:"subject_type"`
	SubjectID    string `json:"subject_id"`
	AddedBy      string `json:"added_by"`
}

func (e MemberAddedEvent) Type() string { return "member.added" }

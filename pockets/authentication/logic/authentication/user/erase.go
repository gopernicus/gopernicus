package user

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErrEraseBlockedByClaim is returned by EraseRepository.Erase when an
// invitation addressed to or claimed by the user is in the accepting state. The
// claim id is the host's grant operation id and a claim never expires, so the
// operator settles or cancels it through the host's grant path first; nothing
// was written. It wraps sdk.ErrConflict (409). Checked with errors.Is.
var ErrEraseBlockedByClaim = fmt.Errorf("user: an accepting invitation claim is held for the user; settle or cancel it first: %w", sdk.ErrConflict)

// EraseRepository is the OPTIONAL user-erasure capability (SOC 2 S3). It must be
// backed by the same transaction-capable adapter as Users, Identifiers,
// Passwords, Passkeys, Sessions, Challenges, AuthenticationGrants, Invitations,
// ServiceAccounts, APIKeys and SecurityEvents: Erase is ONE transaction across
// all of them.
//
// Erase, in order: lock the user row (already erased → Erasure{Changed: false,
// ErasedAt: stored}, no statement, no row); capture the user's (kind, value)
// identifier pairs, active and retired, dropping any pair that is an ACTIVE
// identifier of another user; lock the invitations addressed to a captured pair
// with resolved_subject_id empty or the user, and those resolved to the user;
// refuse with ErrEraseBlockedByClaim when any locked row is accepting; mark the
// user erased (deactivated when active, display name blanked, auth_revision
// incremented); retire and blank every identifier row of the user; delete the
// password, passkeys, OAuth accounts, external subjects, contact changes, OAuth2
// authorization codes, the challenges keyed to the user or to SubjectKeys, the
// authentication grants and the sessions of both profiles; delete the locked
// pending invitations and blank identifier and metadata on the locked terminal
// ones (invited_by, resolved_subject_id and resolved_subject_type are kept);
// cancel the pending invitations the user issued; delete the user's act-as-user
// service accounts and all their API keys (accounts the user owns without
// act-as stay); and append Event with the counts merged into its Details.
//
// The security-event rail is never touched beyond that one row: earlier rows
// keep the user's pseudonymous id and age out under retention.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - an empty ID → sdk.ErrInvalidInput before any statement;
//   - an unknown ID → sdk.ErrNotFound;
//   - an accepting claim → ErrEraseBlockedByClaim with nothing written;
//   - a repeat → Changed=false with the first ErasedAt and zero counts.
type EraseRepository interface {
	Erase(ctx context.Context, in EraseInput) (Erasure, error)
}

// EraseInput is one erasure. Now stamps erased_at and every timestamp the
// transaction writes. SubjectKeys are the PII-free keys of the user's
// address-keyed challenges (an email magic link issued before the address
// mapped to the user carries no user id). Event is the user_erased row the
// service prepared — the actor's attribution and client info, no counts; the
// store merges the counts and appends it inside the transaction. A zero Event
// (no EventType) appends nothing: the host runs without the rail.
type EraseInput struct {
	ID          string
	Now         time.Time
	SubjectKeys []string
	Event       securityevent.SecurityEvent
}

// Erasure is the outcome of an Erase. Changed reports whether this call erased
// the user; ErasedAt is when the user was erased, by this call or an earlier
// one. Counts are zero when Changed is false.
type Erasure struct {
	Changed  bool
	ErasedAt time.Time
	Counts   EraseCounts
}

// EraseCounts is what one erasure removed, blanked or cancelled. The counts are
// the user_erased row's Details; they name rows, never their content.
type EraseCounts struct {
	Sessions             int
	Grants               int
	Passwords            int
	Passkeys             int
	OAuthAccounts        int
	ExternalSubjects     int
	ContactChanges       int
	Codes                int
	Challenges           int
	IdentifiersBlanked   int
	InvitationsDeleted   int
	InvitationsBlanked   int
	InvitationsCancelled int
	ServiceAccounts      int
	APIKeys              int
}

// Merge returns a copy of details with every count added under its stable
// snake_case key. It is the one spelling every store writes into the
// user_erased row.
func (c EraseCounts) Merge(details map[string]any) map[string]any {
	out := maps.Clone(details)
	if out == nil {
		out = map[string]any{}
	}
	out["sessions"] = c.Sessions
	out["grants"] = c.Grants
	out["passwords"] = c.Passwords
	out["passkeys"] = c.Passkeys
	out["oauth_accounts"] = c.OAuthAccounts
	out["external_subjects"] = c.ExternalSubjects
	out["contact_changes"] = c.ContactChanges
	out["codes"] = c.Codes
	out["challenges"] = c.Challenges
	out["identifiers_blanked"] = c.IdentifiersBlanked
	out["invitations_deleted"] = c.InvitationsDeleted
	out["invitations_blanked"] = c.InvitationsBlanked
	out["invitations_cancelled"] = c.InvitationsCancelled
	out["service_accounts"] = c.ServiceAccounts
	out["api_keys"] = c.APIKeys
	return out
}

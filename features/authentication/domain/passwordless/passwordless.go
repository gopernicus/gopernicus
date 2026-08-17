// Package passwordless is the atomic magic-link redemption domain
// (coordination-hub-auth-upstream CHAU-6.1).
//
// It exists for one reason: consuming a magic-link token and establishing the
// resulting identity cannot be two steps. challenge.Repository.ConsumeToken is
// atomic for the CHALLENGE ROW only — it cannot make token consumption atomic
// with creating a user, claiming an identifier, revoking a squatter's
// credentials, and inserting a session. Doing those as a sequence in the service
// leaves a half-provisioned account behind any failure and lets two concurrent
// redemptions of the same link both act.
//
// The precedent is domain/passwordreset: a store-owned multi-table transaction
// rather than a service-level check/write sequence.
//
// The package holds the input/output vocabulary and the port. The store owns the
// transaction; the service owns credential generation and policy.
package passwordless

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// BindingVersion is the version stamped into a magic-link binding at issue.
// Redemption REJECTS a version it does not know rather than guessing, because the
// binding is what carries the provisioning INTENT — misreading it could provision
// an account nobody asked to create.
const BindingVersion = 1

// Binding is the stored, versioned magic-link binding (CHAU-6.1). It is written
// into the challenge row's Context at issue and read back verbatim at redemption.
//
// The intent is captured AT ISSUE and never re-derived from current
// configuration: a link mailed while provisioning was disabled must not provision
// just because the flag was flipped on before the user clicked it, and a link
// mailed while it was enabled keeps its meaning if the flag is flipped off.
type Binding struct {
	// Version is BindingVersion at issue. An unknown version is a generic
	// redemption failure, never a silent fallback.
	Version int `json:"version"`
	// Kind is the identifier kind the link was issued for. v1 provisioning is
	// EMAIL-LINK ONLY; a phone or OTP binding never carries ProvisionIfAbsent.
	Kind string `json:"kind"`
	// NormalizedValue is the canonical address the link proves possession of. It
	// is retained here deliberately: token redemption carries no identifier input,
	// so without it the redeemer cannot know WHAT ownership was proven. This is the
	// same retention the pre-existing known-user binding already had.
	NormalizedValue string `json:"normalized_value"`
	// IdentifierID and UserID are the rows that existed AT ISSUE, empty when the
	// address matched no account. They are an expectation, not an authority: the
	// redemption re-reads the CURRENT claim and decides from that.
	IdentifierID string `json:"identifier_id,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	// ProvisionIfAbsent records that this link was issued under an enabled
	// provisioning policy. Captured at issue; never inferred at consume.
	ProvisionIfAbsent bool `json:"provision_if_absent,omitempty"`
}

// Outcome is what an atomic redemption decided. It is authoritative: the port
// never smuggles a decision through the error return, which is reserved for
// infrastructure failure and the generic credential rejection.
type Outcome string

const (
	// OutcomeLoginExistingVerified is the ordinary case: the bound address is
	// claimed by a verified, login-enabled identifier and the link logs that user
	// in.
	OutcomeLoginExistingVerified Outcome = "login_existing_verified"
	// OutcomeVerifyAndAdoptExistingUnverified means the address was claimed by an
	// UNVERIFIED identifier. Clicking the link proves possession, so the identifier
	// is verified and adopted — and every credential that predates the proof (the
	// password, sessions, recent-auth grants, outstanding password-reset
	// challenges) is revoked first, because they may belong to a squatter who
	// registered the address without proving it. This is the OAuth pending-link
	// lesson applied to the same threat.
	OutcomeVerifyAndAdoptExistingUnverified Outcome = "verify_and_adopt_existing_unverified"
	// OutcomeProvisionNew means no account claimed the address, so one was created
	// in this transaction with the address already verified.
	OutcomeProvisionNew Outcome = "provision_new"
)

// ErrRedemption is the single generic credential failure a redemption returns for
// every stable bad outcome: an unknown, expired, or replayed token; a malformed or
// unknown-version binding; a replaced, removed, or login-disabled identifier; a
// deactivated user; and a would-be provision on a link that never carried the
// intent. It wraps sdk.ErrUnauthorized.
//
// One error for all of them is the point. Branch-specific errors would let a
// caller distinguish "this address has no account" from "this token is stale",
// which is exactly the enumeration the passwordless design forbids.
var ErrRedemption = fmt.Errorf("passwordless redemption rejected: %w", sdk.ErrUnauthorized)

// RedeemInput is everything the store needs to decide and commit. It carries only
// PROTECTED values: a token DIGEST, never the token; and a fully-formed session
// row whose plaintext credentials the service holds and discards if the
// transaction does not commit.
type RedeemInput struct {
	// Purpose is the challenge purpose to match (login_magic_link).
	Purpose string
	// TokenDigest is the presented token's digest. The plaintext never reaches the
	// store.
	TokenDigest string
	// Session is the proposed session row, already carrying its hashed refresh
	// token. It is inserted ONLY on a committed outcome, and its UserID is filled
	// in by the store because a provisioning redemption does not know the id until
	// the user row exists.
	Session session.Session
	// NewUser is the user row template a provisioning outcome creates: the domain
	// already stamped its status and timestamps. Its ID may be empty under the
	// DB-generated convention.
	NewUser user.User
	// NewIdentifier is the identifier row template a provisioning outcome creates
	// for the bound address: verified, primary, and login/recovery/notification
	// enabled. Its UserID is filled in by the store.
	NewIdentifier identifier.Identifier
	// AdoptedIdentifierUses are the uses an ADOPTED (previously unverified)
	// identifier is set to when the link proves its address.
	AdoptedIdentifierUses identifier.Uses
	// RevokeChallengePurposes are the challenge purposes an adoption revokes for
	// the adopted user — the outstanding secrets a squatter could still redeem.
	RevokeChallengePurposes []string
	// Now is the transaction clock: the verification time, the session's
	// authentication time, and any created row's timestamps derive from it.
	Now time.Time
}

// RedeemResult is what a committed redemption produced.
type RedeemResult struct {
	// Outcome is the branch that committed.
	Outcome Outcome
	// User is the resulting subject — created, adopted, or logged in.
	User user.User
	// Identifier is the resulting active identifier for the bound address.
	Identifier identifier.Identifier
	// Session is the committed session row.
	Session session.Session
	// Provisioned reports whether this redemption CREATED the user. It drives the
	// best-effort invitation resolution, which must run for a new account and must
	// NOT re-run for a login or an adoption.
	Provisioned bool
}

// Repository is the atomic magic-link redemption port. ONE method, because the
// whole contract is that it is one transaction.
//
// Redeem MUST, inside a single transaction:
//
//  1. select and consume the live matching login_magic_link challenge row;
//  2. decode and validate the stored versioned Binding against the CURRENT active
//     identifier claim for its (kind, value);
//  3. choose exactly one outcome — login, verify-and-adopt, provision, or a
//     generic rejection;
//  4. require the resulting user to be ACTIVE;
//  5. for an adoption, verify/claim the identifier and revoke the pre-proof
//     password, sessions, recent-auth grants, and the listed challenge purposes
//     BEFORE the session is inserted;
//  6. for a provision, create one active user with an empty display name and one
//     VERIFIED primary identifier with login/recovery/notification enabled; and
//  7. insert the proposed session and commit all of it together.
//
// Sentinel contract (the storetest conformance suite executes these):
//
//   - every stable bad outcome — unknown/expired/replayed token, malformed or
//     unknown-version binding, replaced/removed/login-disabled identifier,
//     deactivated user, provisioning without the captured intent — is
//     ErrRedemption, and NOTHING is written;
//   - an infrastructure error rolls the WHOLE transaction back INCLUDING the token
//     consumption, so a transient failure leaves the link retryable; and
//   - two concurrent redemptions of one token produce exactly ONE committed
//     session and one outcome; the loser gets ErrRedemption.
//
// It must be backed by the same transaction-capable adapter as Users,
// Identifiers, Passwords, Sessions, Challenges, and AuthenticationGrants.
type Repository interface {
	Redeem(ctx context.Context, in RedeemInput) (RedeemResult, error)
}

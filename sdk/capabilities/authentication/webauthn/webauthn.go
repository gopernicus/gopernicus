package webauthn

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// Required: the authenticator must verify the user (a PIN or biometric).
	Required Requirement = "required"
	// Preferred: verification is requested; a ceremony without it still
	// succeeds and reports UserVerified false.
	Preferred Requirement = "preferred"
	// Discouraged: verification is not requested; an authenticator may still
	// perform it, and the ceremony reports what it did.
	Discouraged Requirement = "discouraged"
)

// MaxUserHandleBytes bounds User.Handle (the WebAuthn user.id limit).
const MaxUserHandleBytes = 64

var (
	// ErrCeremonyRejected rejects a registration or assertion response or its
	// State (see the package documentation for the classes). It matches
	// sdk.ErrUnauthorized.
	ErrCeremonyRejected = fmt.Errorf("webauthn: ceremony rejected: %w", sdk.ErrUnauthorized)

	// ErrUserVerificationRequired rejects an otherwise valid response whose
	// authenticator data lacks user verification under Required. It matches
	// sdk.ErrUnauthorized, not ErrCeremonyRejected.
	ErrUserVerificationRequired = fmt.Errorf("webauthn: user verification required: %w", sdk.ErrUnauthorized)

	// ErrOriginOutsideRPID refuses a Config whose origin policy admits a host
	// outside the RP ID; the returned error names the host. It matches
	// sdk.ErrInvalidInput.
	ErrOriginOutsideRPID = fmt.Errorf("webauthn: origin outside the RP ID: %w", sdk.ErrInvalidInput)
)

// RelyingParty runs WebAuthn ceremonies for one RP ID. Implementations are
// safe for concurrent use; every ceremony's state travels in Ceremony.State.
type RelyingParty interface {
	// Config returns the validated configuration, Timeout resolved. Consumers
	// read RPID and Origins for their own posture checks.
	Config() Config

	// BeginRegistration starts a registration ceremony for in.User.
	BeginRegistration(ctx context.Context, in RegistrationStart) (Ceremony, error)

	// FinishRegistration verifies a registration response against its State
	// and returns the new credential. Every failure returns a zero Credential.
	FinishRegistration(ctx context.Context, in RegistrationFinish) (Credential, error)

	// BeginAssertion starts a discoverable assertion ceremony: the options
	// carry an empty allowCredentials.
	BeginAssertion(ctx context.Context, in AssertionStart) (Ceremony, error)

	// ReadAssertion parses an assertion response and returns its unverified
	// lookup keys. A malformed response returns ErrCeremonyRejected.
	ReadAssertion(response []byte) (AssertionHandle, error)

	// FinishAssertion verifies an assertion response against its State, the
	// stored credential record and the record owner's user handle. Every
	// failure returns a zero Assertion.
	FinishAssertion(ctx context.Context, in AssertionFinish) (Assertion, error)
}

// Requirement is a WebAuthn user-verification requirement; its values are the
// wire strings.
type Requirement string

// User is the account a credential is registered for.
type User struct {
	// Handle is the WebAuthn user.id: 1 to MaxUserHandleBytes opaque bytes.
	// Callers mint 32 random bytes per user, never the user id, because
	// authenticators disclose it to anyone holding the device.
	Handle []byte
	// Name and DisplayName are shown by the authenticator's account chooser.
	Name        string
	DisplayName string
}

// RegistrationStart is the input to BeginRegistration.
type RegistrationStart struct {
	User User
	// Exclude lists the credential ids the user already holds, so an
	// authenticator holding one refuses to register a second.
	Exclude [][]byte
	// UserVerification defaults to Preferred.
	UserVerification Requirement
}

// AssertionStart is the input to BeginAssertion.
type AssertionStart struct {
	// UserVerification defaults to Preferred.
	UserVerification Requirement
}

// Ceremony is one started ceremony.
type Ceremony struct {
	// Options is the inner publicKey options JSON for the client.
	Options json.RawMessage
	// State is kept server-side and presented to Finish at most once.
	State []byte
	// ExpiresAt is when Finish stops accepting State.
	ExpiresAt time.Time
}

// RegistrationFinish is the input to FinishRegistration.
type RegistrationFinish struct {
	State []byte
	// Response is the RegistrationResponseJSON (PublicKeyCredential.toJSON()
	// of a create() result).
	Response []byte
}

// AssertionHandle carries an assertion response's unverified lookup keys.
type AssertionHandle struct {
	CredentialID []byte
	UserHandle   []byte
}

// AssertionFinish is the input to FinishAssertion.
type AssertionFinish struct {
	State []byte
	// Response is the AuthenticationResponseJSON (PublicKeyCredential.toJSON()
	// of a get() result).
	Response []byte
	// Credential is the stored record the response's credential id names.
	Credential Credential
	// UserHandle is the stored handle of the record's owner; the response's
	// user handle must equal it.
	UserHandle []byte
}

// Credential is a registered credential as the caller stores it.
type Credential struct {
	// ID is the raw credential id.
	ID []byte
	// PublicKey is the credential public key as a COSE_Key.
	PublicKey []byte
	// AAGUID is the authenticator model id as reported (16 bytes, all zero
	// when the client does not report one). Attestation is "none", so it is
	// unverified.
	AAGUID []byte
	// SignCount is the counter this ceremony reported.
	SignCount uint32
	// Transports are the response's transport hints, as reported.
	Transports []string
	// UserVerified, BackupEligible and BackupState are this ceremony's
	// authenticator-data flags.
	UserVerified   bool
	BackupEligible bool
	BackupState    bool
}

// Assertion is the result of a verified assertion. Every field comes from this
// ceremony's authenticator data.
type Assertion struct {
	SignCount      uint32
	UserVerified   bool
	BackupEligible bool
	BackupState    bool
	// CounterRegressed: either count is non-zero and the presented count is
	// not above the stored one (0/0 never). The caller decides.
	CounterRegressed bool
}

package totp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

var (
	// ErrAlreadyEnrolled refuses Begin or Confirm for a user whose credential is
	// already confirmed. It matches sdk.ErrConflict.
	ErrAlreadyEnrolled = fmt.Errorf("totp: the account already has a confirmed authenticator app: %w", sdk.ErrConflict)
	// ErrBootstrapClosed refuses a bootstrap-tier Confirm for an owner who, by the
	// time it is stored, holds an AAL2-capable passkey: enrolling now needs AAL2
	// proof. It matches sdk.ErrConflict.
	ErrBootstrapClosed = fmt.Errorf("totp: the account now holds an AAL2-capable passkey; enrolling needs AAL2: %w", sdk.ErrConflict)
	// ErrEnvelope refuses a sealed secret that does not open, or that opens to
	// another user's envelope. It matches sdk.ErrConflict.
	ErrEnvelope = fmt.Errorf("totp: the sealed secret does not belong to this user: %w", sdk.ErrConflict)
)

// Credential is one user's authenticator-app credential. Secret is the sealed
// envelope (see Seal) — plaintext never rests, pending or confirmed. SessionID,
// Tier, AuthRevision and ExpiresAt describe a pending enrollment (the session
// that began it, the gate's tier, the owner's revision at Begin and when it
// lapses); Confirm clears them. ConfirmedAt is nil while pending. LastUsedStep is
// the newest accepted time step, so no code is accepted twice. FailedAttempts
// counts consecutive refusals and LockedUntil is the lockout they set.
type Credential struct {
	UserID         string
	Secret         string
	SessionID      string
	Tier           string
	AuthRevision   int64
	ExpiresAt      *time.Time
	ConfirmedAt    *time.Time
	LastUsedStep   int64
	FailedAttempts int
	LockedUntil    *time.Time
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

// Confirmed reports whether the credential has been confirmed.
func (c Credential) Confirmed() bool { return c.ConfirmedAt != nil }

// Locked reports whether a lockout is in force at now.
func (c Credential) Locked(now time.Time) bool {
	return c.LockedUntil != nil && now.Before(*c.LockedUntil)
}

// RecoveryCode is one stored recovery code: its digest under the challenge
// protector's key KeyID, never the code.
type RecoveryCode struct {
	ID          string
	UserID      string
	KeyID       string
	Digest      string
	GeneratedAt time.Time
}

// Confirmation is one Confirm: the pending credential of UserID becomes
// confirmed at ConfirmedAt with LastUsedStep Step, and Codes replace the user's
// recovery codes. ExpectedAuthRevision is the revision the pending row carries;
// Bootstrap marks an enrollment begun at the AAL1 bootstrap tier.
type Confirmation struct {
	UserID               string
	ExpectedAuthRevision int64
	Bootstrap            bool
	Step                 int64
	ConfirmedAt          time.Time
	Codes                []RecoveryCode
}

// Failure is what RecordFailure counted: the consecutive failures this one
// reached and, when it reached the lock threshold, the lockout it set
// (LockedUntil nil otherwise).
type Failure struct {
	Attempts    int
	LockedUntil *time.Time
}

// Removed is what Remove deleted: whether a credential (pending or confirmed)
// existed and how many recovery codes went with it.
type Removed struct {
	Credential    bool
	RecoveryCodes int
}

// envelope is the sealed plaintext: the owner and the base32 secret. Binding the
// owner into the ciphertext means a row copied onto another user never opens.
type envelope struct {
	U string `json:"u"`
	S string `json:"s"`
}

// Seal encrypts secret for userID under enc.
func Seal(enc cryptids.Encrypter, userID string, secret []byte) (string, error) {
	blob, err := json.Marshal(envelope{U: userID, S: EncodeSecret(secret)})
	if err != nil {
		return "", err
	}
	return enc.Encrypt(string(blob))
}

// Open decrypts a sealed secret and refuses one that is not userID's with
// ErrEnvelope. A ciphertext the encrypter cannot open is ErrEnvelope too, wrapping
// the encrypter's error.
func Open(enc cryptids.Encrypter, userID, sealed string) ([]byte, error) {
	plain, err := enc.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEnvelope, err)
	}
	var e envelope
	if json.Unmarshal([]byte(plain), &e) != nil || e.U == "" || e.U != userID {
		return nil, ErrEnvelope
	}
	secret, err := DecodeSecret(e.S)
	if err != nil {
		return nil, ErrEnvelope
	}
	return secret, nil
}

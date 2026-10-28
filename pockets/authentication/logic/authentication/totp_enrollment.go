package authentication

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// totpEnrollmentTTL is how long a pending enrollment waits for its first code.
	totpEnrollmentTTL = 10 * time.Minute
	// totpWindow is the accepted drift either side of now, in time steps: three
	// live codes.
	totpWindow = 1
	// totpLockAttempts and totpLockFor are the RFC 6238 §5.2 lockout: the fifth
	// consecutive refused code locks the second factor for five minutes.
	totpLockAttempts = 5
	totpLockFor      = 5 * time.Minute
)

// The trigger values a recovery_codes_generated row carries.
const (
	recoveryTriggerEnrollment = "enrollment"
	recoveryTriggerRegenerate = "regenerate"
)

var (
	// ErrTOTPUnavailable is returned by every TOTP and recovery-code method when
	// WithTOTP wired no encrypter. It wraps sdk.ErrNotFound.
	ErrTOTPUnavailable = fmt.Errorf("authentication: TOTP is not enabled: %w", sdk.ErrNotFound)
	// ErrTOTPReposRequired is returned by New when WithTOTP wires an encrypter
	// without Repositories.TOTP, CredentialMutations, AuthenticationGrants or a
	// ChallengeProtector.
	ErrTOTPReposRequired = fmt.Errorf("authentication: TOTP requires Repositories.TOTP, CredentialMutations, AuthenticationGrants and a ChallengeProtector: %w", sdk.ErrInvalidInput)
	// ErrTOTPSchemaMissing is returned by New when WithTOTP is wired and
	// Repositories.TOTP (a totp.SchemaProber) reports its tables missing: apply
	// migration 0026_totp before boot. It wraps the store's error. A host that
	// does not wire TOTP is never probed.
	ErrTOTPSchemaMissing = errors.New("authentication: TOTP is wired but its schema is missing; apply migration 0026_totp")
	// ErrTOTPExists refuses an enrollment for a user whose authenticator app is
	// already confirmed. It wraps sdk.ErrConflict.
	ErrTOTPExists = fmt.Errorf("authentication: an authenticator app is already set up: %w", sdk.ErrConflict)
	// ErrTOTPEnrollmentExpired refuses a confirm with no live pending enrollment
	// for the caller's session: never begun, lapsed, or begun from another
	// session. It wraps sdk.ErrExpired.
	ErrTOTPEnrollmentExpired = fmt.Errorf("authentication: the authenticator-app enrollment expired or was not started: %w", sdk.ErrExpired)
	// ErrTOTPRejected is the one refusal a TOTP code answers: wrong, reused, no
	// confirmed app, a secret that does not open, or a use that lost a concurrent
	// change. The audit row names the class; the error never does. It wraps
	// sdk.ErrUnauthorized.
	ErrTOTPRejected = fmt.Errorf("authentication: authenticator code rejected: %w", sdk.ErrUnauthorized)
	// ErrRecoveryCodeRejected is the one refusal a recovery code answers. It
	// wraps sdk.ErrUnauthorized.
	ErrRecoveryCodeRejected = fmt.Errorf("authentication: recovery code rejected: %w", sdk.ErrUnauthorized)
	// ErrRecoveryCodesStale refuses a regeneration whose expected set is no longer
	// the current one. It wraps sdk.ErrConflict.
	ErrRecoveryCodesStale = fmt.Errorf("authentication: the recovery codes changed since they were read: %w", sdk.ErrConflict)
)

// SecondFactorLockedError is the lockout refusal of a TOTP or recovery-code
// proof: too many consecutive refused codes. It matches ErrTooManyAttempts;
// Until is when the lockout lifts and Wait how long that is from the refusal,
// which a transport turns into Retry-After.
type SecondFactorLockedError struct {
	Until time.Time
	Wait  time.Duration
}

func (e *SecondFactorLockedError) Error() string {
	return "authentication: too many second-factor attempts; try again later"
}

func (e *SecondFactorLockedError) Unwrap() error { return ErrTooManyAttempts }

// TOTPEnrollment is a pending enrollment as the caller receives it: Secret is the
// unpadded base32 shared secret — shown here and nowhere else — and KeyURI the
// otpauth:// URI an authenticator app scans. Account is the login identifier the
// app lists the entry under.
type TOTPEnrollment struct {
	Secret    string
	KeyURI    string
	Issuer    string
	Account   string
	ExpiresAt time.Time
}

// TOTPConfirmInput confirms the caller's pending enrollment with a first code.
type TOTPConfirmInput struct {
	SessionID string
	UserID    string
	Code      string
}

// RecoveryCodeSet is a freshly minted recovery-code set: the codes in their
// display form (xxxxx-xxxxx), returned once and never again, and when they were
// generated.
type RecoveryCodeSet struct {
	Codes       []string
	GeneratedAt time.Time
}

// TOTPConfirmation is a completed enrollment and its first recovery codes.
type TOTPConfirmation struct {
	EnrolledAt    time.Time
	RecoveryCodes RecoveryCodeSet
}

// TOTPEnabled reports whether the authenticator-app second factor is wired. The
// transport mounts the TOTP routes only when it is.
func (s *Service) TOTPEnabled() bool { return s.totpEncrypter != nil && s.totp != nil }

func (s *Service) requireTOTP() error {
	if !s.TOTPEnabled() {
		return ErrTOTPUnavailable
	}
	return nil
}

// BeginTOTPEnrollment starts an authenticator-app enrollment from the caller's
// live session. A user whose app is already confirmed is ErrTOTPExists, checked
// first. It then consumes the second-factor gate (register_totp): a holder of
// an AAL2 factor must prove AAL2 within the last five minutes, anyone else may
// bootstrap from a primary login or a password or code step-up. The pending row
// — one per user; a re-Begin replaces it — carries the sealed secret, the
// session, the tier and the owner's revision, re-checked when it is confirmed.
func (s *Service) BeginTOTPEnrollment(ctx context.Context, sessionID, userID string) (TOTPEnrollment, error) {
	if err := s.requireTOTP(); err != nil {
		return TOTPEnrollment{}, err
	}
	owner, err := s.users.Get(ctx, userID)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if existing, err := s.totp.Get(ctx, userID); err == nil && existing.Confirmed() {
		return TOTPEnrollment{}, ErrTOTPExists
	} else if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return TOTPEnrollment{}, err
	}
	policy, tier, err := s.secondFactorPolicy(ctx, userID)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if _, err := s.RequireRecentAuthentication(ctx, sessionID, userID, authgrant.PurposeRegisterTOTP, "", policy); err != nil {
		return TOTPEnrollment{}, err
	}
	secret, err := totp.NewSecret()
	if err != nil {
		return TOTPEnrollment{}, err
	}
	sealed, err := totp.Seal(s.totpEncrypter, userID, secret)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	now := s.now().UTC()
	expires := now.Add(totpEnrollmentTTL)
	if err := s.totp.Begin(ctx, totp.Credential{
		UserID: userID, Secret: sealed, SessionID: sessionID, Tier: tier, AuthRevision: owner.AuthRevision,
		ExpiresAt: &expires, CreatedAt: now,
	}); err != nil {
		if errors.Is(err, totp.ErrAlreadyEnrolled) {
			return TOTPEnrollment{}, ErrTOTPExists
		}
		return TOTPEnrollment{}, err
	}
	return s.totpEnrollment(ctx, userID, secret, expires)
}

// PendingTOTPEnrollment re-reads the caller's pending enrollment, so a page can
// show the same secret again after a redirect or a wrong code. No live pending
// row for this session is ErrTOTPEnrollmentExpired; a confirmed app is
// ErrTOTPExists.
func (s *Service) PendingTOTPEnrollment(ctx context.Context, sessionID, userID string) (TOTPEnrollment, error) {
	if err := s.requireTOTP(); err != nil {
		return TOTPEnrollment{}, err
	}
	c, err := s.pendingTOTP(ctx, sessionID, userID)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	secret, err := totp.Open(s.totpEncrypter, userID, c.Secret)
	if err != nil {
		s.recordTOTPFailure(ctx, userID, string(totpMethodTOTP), securityevent.ReasonEnvelope)
		return TOTPEnrollment{}, ErrTOTPEnrollmentExpired
	}
	return s.totpEnrollment(ctx, userID, secret, *c.ExpiresAt)
}

// ConfirmTOTPEnrollment confirms the caller's pending enrollment with a code
// from the app, in a fixed order: the second-factor budget, the pending row of
// this live session, its lockout, the secret, the code. A wrong code is
// ErrTOTPRejected (totp_failed reason invalid) and leaves the pending row, so
// the same secret can be tried again; the fifth consecutive one locks. The
// confirmed code's step is recorded, so it never steps up afterwards. Confirm
// stores the credential and ten recovery codes atomically under the user lock,
// refusing a moved revision or a bootstrap-tier enrollment for an owner who
// meanwhile gained an AAL2-capable passkey (ErrStepUpRequired: restart). Success
// records totp_enrolled and recovery_codes_generated, sends the
// mfa_change_notice, and returns the recovery codes — the only time they are
// shown.
func (s *Service) ConfirmTOTPEnrollment(ctx context.Context, in TOTPConfirmInput) (TOTPConfirmation, error) {
	if err := s.requireTOTP(); err != nil {
		return TOTPConfirmation{}, err
	}
	if err := s.secondFactorBudget(ctx, in.UserID); err != nil {
		return TOTPConfirmation{}, err
	}
	method := string(totpMethodTOTP)
	c, err := s.pendingTOTP(ctx, in.SessionID, in.UserID)
	if err != nil {
		if errors.Is(err, ErrTOTPEnrollmentExpired) {
			s.recordTOTPFailure(ctx, in.UserID, method, securityevent.ReasonExpired)
		}
		return TOTPConfirmation{}, err
	}
	if _, err := s.liveStepUpSession(ctx, in.SessionID, in.UserID, s.now()); err != nil {
		return TOTPConfirmation{}, err
	}
	now := s.now().UTC()
	if c.Locked(now) {
		s.recordTOTPFailure(ctx, in.UserID, method, securityevent.ReasonLocked)
		return TOTPConfirmation{}, &SecondFactorLockedError{Until: *c.LockedUntil, Wait: c.LockedUntil.Sub(now)}
	}
	secret, err := totp.Open(s.totpEncrypter, in.UserID, c.Secret)
	if err != nil {
		s.recordTOTPFailure(ctx, in.UserID, method, securityevent.ReasonEnvelope)
		return TOTPConfirmation{}, ErrTOTPRejected
	}
	step, ok := totp.Validate(secret, in.Code, now, totpWindow)
	if !ok {
		return TOTPConfirmation{}, s.secondFactorFailure(ctx, in.UserID, method, securityevent.ReasonInvalid, ErrTOTPRejected)
	}
	set, rows, err := s.mintRecoveryCodes(in.UserID, now)
	if err != nil {
		return TOTPConfirmation{}, err
	}
	err = s.totp.Confirm(ctx, totp.Confirmation{
		UserID: in.UserID, ExpectedSecret: c.Secret, ExpectedSessionID: c.SessionID,
		ExpectedAuthRevision: c.AuthRevision, Bootstrap: c.Tier != passkeyTierAAL2 && s.PasskeysEnabled(),
		Step: step, ConfirmedAt: now, Codes: rows,
	})
	if err != nil {
		switch {
		case errors.Is(err, totp.ErrAlreadyEnrolled):
			return TOTPConfirmation{}, ErrTOTPExists
		case errors.Is(err, sdk.ErrNotFound):
			return TOTPConfirmation{}, ErrTOTPEnrollmentExpired
		case errors.Is(err, totp.ErrBootstrapClosed), errors.Is(err, sdk.ErrConflict):
			s.recordTOTPFailure(ctx, in.UserID, method, securityevent.ReasonConflict)
			return TOTPConfirmation{}, ErrStepUpRequired
		}
		return TOTPConfirmation{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: in.UserID, Type: securityevent.TypeTOTPEnrolled, Status: securityevent.StatusSuccess,
		Details: map[string]any{"tier": boundedEnum(c.Tier, passkeyTierBootstrap, passkeyTierAAL2)},
	})
	s.recordRecoveryCodesGenerated(ctx, in.UserID, len(set.Codes), recoveryTriggerEnrollment)
	s.enqueueMFAChangeNotices(ctx, in.UserID, mfaChangeTOTPEnrolled)
	return TOTPConfirmation{EnrolledAt: now, RecoveryCodes: set}, nil
}

// pendingTOTP returns the caller's live pending enrollment: a row that is not
// confirmed (else ErrTOTPExists), unexpired and begun from sessionID (else
// ErrTOTPEnrollmentExpired).
func (s *Service) pendingTOTP(ctx context.Context, sessionID, userID string) (totp.Credential, error) {
	c, err := s.totp.Get(ctx, userID)
	if errors.Is(err, sdk.ErrNotFound) {
		return totp.Credential{}, ErrTOTPEnrollmentExpired
	}
	if err != nil {
		return totp.Credential{}, err
	}
	if c.Confirmed() {
		return totp.Credential{}, ErrTOTPExists
	}
	if sessionID == "" || c.SessionID != sessionID || c.ExpiresAt == nil || !s.now().Before(*c.ExpiresAt) {
		return totp.Credential{}, ErrTOTPEnrollmentExpired
	}
	return c, nil
}

// totpEnrollment renders a pending enrollment for the caller.
func (s *Service) totpEnrollment(ctx context.Context, userID string, secret []byte, expires time.Time) (TOTPEnrollment, error) {
	view, err := s.CurrentUserView(ctx, userID)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	account, _ := passkeyAccountNames(view)
	uri, err := totp.KeyURI(s.totpIssuer, account, secret)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	return TOTPEnrollment{Secret: totp.EncodeSecret(secret), KeyURI: uri, Issuer: s.totpIssuer, Account: account, ExpiresAt: expires}, nil
}

// mintRecoveryCodes mints a recovery-code set for userID: the display codes and
// the rows that store their digests under the protector's active key.
func (s *Service) mintRecoveryCodes(userID string, now time.Time) (RecoveryCodeSet, []totp.RecoveryCode, error) {
	codes, err := totp.NewRecoveryCodes(totp.RecoveryCodeCount)
	if err != nil {
		return RecoveryCodeSet{}, nil, err
	}
	generated := now.UTC().Truncate(time.Second)
	keyID := s.protector.ActiveKeyID()
	rows := make([]totp.RecoveryCode, 0, len(codes))
	for _, code := range codes {
		normalized, _ := totp.NormalizeRecoveryCode(code)
		digest, err := s.protector.DigestCode(keyID, userID, challenge.PurposeRecoveryCode, normalized)
		if err != nil {
			return RecoveryCodeSet{}, nil, err
		}
		rows = append(rows, totp.RecoveryCode{ID: s.recordIDs.MustGenerate(), UserID: userID, KeyID: keyID, Digest: digest, GeneratedAt: generated})
	}
	return RecoveryCodeSet{Codes: codes, GeneratedAt: generated}, rows, nil
}

// secondFactorFailure counts a refused code against the lockout and records it,
// returning rejected, or the lockout when this failure set one (totp_locked).
func (s *Service) secondFactorFailure(ctx context.Context, userID, method, reason string, rejected error) error {
	s.recordTOTPFailure(ctx, userID, method, reason)
	f, err := s.totp.RecordFailure(ctx, userID, totpLockAttempts, totpLockFor, s.now().UTC())
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return rejected
		}
		return err
	}
	if f.LockedUntil == nil {
		return rejected
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID, Type: securityevent.TypeTOTPLocked, Status: securityevent.StatusBlocked,
		Details:  map[string]any{"attempts": f.Attempts, "locked_for_seconds": int(totpLockFor / time.Second)},
		Budgeted: true,
	})
	return &SecondFactorLockedError{Until: *f.LockedUntil, Wait: totpLockFor}
}

// recordTOTPFailure appends a budgeted totp_failed row.
func (s *Service) recordTOTPFailure(ctx context.Context, userID, method, reason string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID, Type: securityevent.TypeTOTPFailed, Status: securityevent.StatusFailure,
		Details: map[string]any{
			"method": boundedEnum(method, string(totpMethodTOTP), string(totpMethodRecoveryCode)),
			"reason": boundedEnum(reason, totpFailureReasons...),
		},
		Budgeted: true,
	})
}

// recordRecoveryCodesGenerated appends a recovery_codes_generated row.
func (s *Service) recordRecoveryCodesGenerated(ctx context.Context, userID string, count int, trigger string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID, Type: securityevent.TypeRecoveryCodesGenerated, Status: securityevent.StatusSuccess,
		Details: map[string]any{"count": count, "trigger": trigger},
	})
}

// totpFailureReasons is the totp_failed reason vocabulary.
var totpFailureReasons = []string{
	securityevent.ReasonInvalid, securityevent.ReasonReused, securityevent.ReasonLocked, securityevent.ReasonNotEnrolled,
	securityevent.ReasonExpired, securityevent.ReasonEnvelope, securityevent.ReasonInactive, securityevent.ReasonConflict,
}

// warnOrphanedRecoveryCodes logs one WARN at New naming the key ids recovery
// codes are stored under that the challenge protector no longer accepts: those
// codes can never verify, so their users regenerate at their next AAL2.
func (s *Service) warnOrphanedRecoveryCodes(ctx context.Context) {
	if !s.TOTPEnabled() || s.protector == nil {
		return
	}
	stored, err := s.totp.RecoveryCodeKeyIDs(ctx)
	if err != nil {
		s.logger.Warn("auth: recovery-code key ids could not be read", "error_kind", ErrorKind(err))
		return
	}
	candidates, err := s.protector.CandidateCodeDigests("", challenge.PurposeRecoveryCode, "key-probe")
	if err != nil {
		return
	}
	var orphaned []string
	for _, id := range stored {
		if !slices.ContainsFunc(candidates, func(c challenge.DigestCandidate) bool { return c.KeyID == id }) {
			orphaned = append(orphaned, id)
		}
	}
	if len(orphaned) > 0 {
		slices.Sort(orphaned)
		s.logger.Warn("auth: recovery codes sealed under retired pepper keys", "key_ids", orphaned)
	}
}

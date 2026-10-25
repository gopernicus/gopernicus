package authentication

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
)

const (
	// passkeyCeremonyTTL is the life of a ceremony handle on the challenge rail:
	// the WebAuthn timeout ceiling, the one TTL both the rail and the relying
	// party's state honour.
	passkeyCeremonyTTL = webauthn.MaxTimeout
	// defaultPasskeysPerUser and maxPasskeysPerUser bound PasskeyConfig.MaxPerUser.
	defaultPasskeysPerUser = 10
	maxPasskeysPerUser     = 25
	// passkeyHandleBytes is the width of a minted WebAuthn user handle.
	passkeyHandleBytes = 32
)

// The authorization tier a registration ceremony was begun at, recorded on its
// row and re-applied when the passkey is stored.
const (
	passkeyTierBootstrap = "bootstrap"
	passkeyTierAAL2      = "aal2"
)

// The ceremony values passkey_login and passkey_failed rows carry.
const (
	passkeyCeremonySignIn       = "sign_in"
	passkeyCeremonyStepUp       = "step_up"
	passkeyCeremonyRegistration = "registration"
)

var (
	// ErrPasskeysUnavailable is returned by every passkey method when no relying
	// party is wired. It wraps sdk.ErrNotFound: an unwired capability is absent.
	ErrPasskeysUnavailable = fmt.Errorf("authentication: passkeys are not enabled: %w", sdk.ErrNotFound)
	// ErrPasskeyReposRequired is returned by New when WithPasskeys wires a relying
	// party without Repositories.Passkeys, CredentialMutations,
	// AuthenticationGrants, or the challenge rail and its protector.
	ErrPasskeyReposRequired = fmt.Errorf("authentication: passkeys require Repositories.Passkeys, CredentialMutations, AuthenticationGrants, Challenges and a ChallengeProtector: %w", sdk.ErrInvalidInput)
	// ErrPasskeyRejected is the one refusal a passkey ceremony answers: an
	// unknown, consumed or foreign ceremony handle, a response the relying party
	// refused, an unknown or another user's credential, an inactive account, a
	// regressed signature counter, or a use that lost a concurrent change. The
	// audit row names the class; the error never does. It wraps
	// sdk.ErrUnauthorized.
	ErrPasskeyRejected = fmt.Errorf("authentication: passkey rejected: %w", sdk.ErrUnauthorized)
	// ErrPasskeyExists refuses registering a credential that is already stored.
	// It wraps sdk.ErrAlreadyExists.
	ErrPasskeyExists = fmt.Errorf("authentication: this passkey is already registered: %w", sdk.ErrAlreadyExists)
	// ErrPasskeyLimit refuses a registration past PasskeyConfig.MaxPerUser,
	// checked when the ceremony begins and again when it is stored. It wraps
	// sdk.ErrConflict.
	ErrPasskeyLimit = passkey.ErrPasskeyLimit
)

// PasskeyCeremony is a started ceremony as the client receives it. Ceremony is
// the single-use handle the client returns with the authenticator's response;
// PublicKey is the inner publicKey options JSON for
// PublicKeyCredential.parseCreationOptionsFromJSON or
// parseRequestOptionsFromJSON; ExpiresAt is when the handle stops being
// accepted. The relying-party state stays server-side on the challenge rail.
type PasskeyCeremony struct {
	Ceremony  string
	PublicKey json.RawMessage
	ExpiresAt time.Time
}

// PasskeyRegistrationInput finishes a registration. SessionID and UserID are the
// caller's proven live session; Response is the RegistrationResponseJSON. An
// empty or invalid Name never fails the registration: the passkey is named
// "Passkey N" instead.
type PasskeyRegistrationInput struct {
	SessionID string
	UserID    string
	Ceremony  string
	Response  []byte
	Name      string
}

// PasskeyRegistrationOptions are a registration's per-ceremony requests.
// UserVerification webauthn.Required raises the ceremony to required user
// verification, so the authenticator must verify the user (a PIN or biometric)
// and a response without it is refused; empty or webauthn.Preferred keeps
// PasskeyConfig.UserVerification. A request never lowers the host's
// requirement; any other value is sdk.ErrInvalidInput.
type PasskeyRegistrationOptions struct {
	UserVerification webauthn.Requirement
}

// PasskeySignInInput finishes a usernameless sign-in. Response is the
// AuthenticationResponseJSON.
type PasskeySignInInput struct {
	Ceremony string
	Response []byte
}

// PasskeyStepUpInput finishes a step-up from the caller's proven live session.
type PasskeyStepUpInput struct {
	SessionID string
	UserID    string
	Ceremony  string
	Response  []byte
}

// StepUpResult is a completed passkey step-up: the re-minted session's token
// pair (the replaced session is gone), its user, and the assurance the
// assertion earned — AAL2 for a user-verified passkey, AAL1 for presence only.
type StepUpResult struct {
	Tokens    TokenPair
	User      user.User
	Assurance session.AssuranceLevel
}

// passkeyBinding is a ceremony row's stored context: the relying party's
// ceremony state and the session it is bound to (empty for sign-in). A
// registration also records the user handle the creation options carried, the
// tier its gate authorized and the owner's auth revision at Begin, which Create
// re-checks under the user lock.
type passkeyBinding struct {
	State        []byte `json:"state"`
	SessionID    string `json:"session_id,omitempty"`
	UserHandle   []byte `json:"user_handle,omitempty"`
	Tier         string `json:"tier,omitempty"`
	AuthRevision int64  `json:"auth_revision,omitempty"`
}

// PasskeysEnabled reports whether a relying party is wired. The transport
// mounts the passkey routes only when it is.
func (s *Service) PasskeysEnabled() bool { return s.relyingParty != nil }

func (s *Service) requirePasskeys() error {
	if s.relyingParty == nil || s.passkeys == nil {
		return ErrPasskeysUnavailable
	}
	return nil
}

// BeginPasskeyRegistration starts a registration from the caller's live
// session. It refuses a user already at PasskeyConfig.MaxPerUser before any
// authenticator is involved, then consumes the enrollment gate: a user holding
// a user-verified passkey must prove AAL2 within the last five minutes (a
// passkey sign-in or step-up); anyone else — the first passkey included — may
// bootstrap from a primary login or a password or code step-up grant in that
// window. Missing proof is ErrStepUpRequired. The single-use ceremony row then
// carries the authorization to FinishPasskeyRegistration, with the tier it was
// granted at and the owner's auth revision, both re-checked when the passkey is
// stored. The options exclude the user's existing credentials and reuse their
// user handle, or carry a fresh random one. A ceremony that requires user
// verification (the host's PasskeyConfig.UserVerification or opts) leaves the
// user's presence-only passkeys out of the exclude list, so the same
// authenticator can replace one with a verified passkey.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, sessionID, userID string, opts ...PasskeyRegistrationOptions) (PasskeyCeremony, error) {
	if err := s.requirePasskeys(); err != nil {
		return PasskeyCeremony{}, err
	}
	uv, err := s.registrationUserVerification(opts)
	if err != nil {
		return PasskeyCeremony{}, err
	}
	owner, err := s.users.Get(ctx, userID)
	if err != nil {
		return PasskeyCeremony{}, err
	}
	existing, err := s.passkeys.ListByUser(ctx, userID)
	if err != nil {
		return PasskeyCeremony{}, err
	}
	if len(existing) >= s.passkeyMaxPerUser {
		return PasskeyCeremony{}, ErrPasskeyLimit
	}
	policy, tier := passkeyEnrollmentPolicy(existing), passkeyTierBootstrap
	if policy.MinAssurance == session.AssuranceAAL2 {
		tier = passkeyTierAAL2
	}
	if _, err := s.RequireRecentAuthentication(ctx, sessionID, userID, authgrant.PurposeRegisterPasskey, "", policy); err != nil {
		return PasskeyCeremony{}, err
	}
	view, err := s.CurrentUserView(ctx, userID)
	if err != nil {
		return PasskeyCeremony{}, err
	}
	handle, exclude := passkeyHandle(existing), make([][]byte, 0, len(existing))
	if handle == nil {
		if handle, err = newPasskeyHandle(); err != nil {
			return PasskeyCeremony{}, err
		}
	}
	for _, p := range existing {
		if uv == webauthn.Required && !p.AAL2Capable() {
			continue
		}
		exclude = append(exclude, p.CredentialID)
	}
	name, display := passkeyAccountNames(view)
	started, err := s.relyingParty.BeginRegistration(ctx, webauthn.RegistrationStart{
		User:             webauthn.User{Handle: handle, Name: name, DisplayName: display},
		Exclude:          exclude,
		UserVerification: uv,
	})
	if err != nil {
		return PasskeyCeremony{}, err
	}
	return s.issuePasskeyCeremony(ctx, userID, challenge.PurposePasskeyRegistration, started,
		passkeyBinding{State: started.State, SessionID: sessionID, UserHandle: handle, Tier: tier, AuthRevision: owner.AuthRevision},
		withSubjectKey(passkeySessionSubject(sessionID)))
}

// registrationUserVerification is a registration ceremony's requirement: the
// host's, raised to Required when a request asks for it and never lowered.
func (s *Service) registrationUserVerification(opts []PasskeyRegistrationOptions) (webauthn.Requirement, error) {
	uv := s.passkeyUV
	for _, o := range opts {
		switch o.UserVerification {
		case "", webauthn.Preferred:
		case webauthn.Required:
			uv = webauthn.Required
		default:
			return "", fmt.Errorf("authentication: passkey registration user verification %q is not required or preferred: %w", o.UserVerification, sdk.ErrInvalidInput)
		}
	}
	return uv, nil
}

// FinishPasskeyRegistration consumes the ceremony, checks that it belongs to the
// caller's live session, verifies the response through the relying party and
// stores the passkey under the Begin-time guard: a moved auth revision, or a
// bootstrap-tier ceremony for an owner who meanwhile gained an AAL2-capable
// passkey, is ErrStepUpRequired (restart; Begin then applies the current
// gate). A duplicate credential is ErrPasskeyExists and a full account
// ErrPasskeyLimit. Success records passkey_registered and sends the
// passkey_change_notice to the account's verified contact channels.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, in PasskeyRegistrationInput) (passkey.Passkey, error) {
	if err := s.requirePasskeys(); err != nil {
		return passkey.Passkey{}, err
	}
	binding, err := s.consumePasskeyCeremony(ctx, challenge.PurposePasskeyRegistration, in.Ceremony, in.UserID, in.SessionID)
	if err != nil {
		s.recordPasskeyFailure(ctx, passkeyCeremonyRegistration, in.UserID, ceremonyFailureReason(err))
		return passkey.Passkey{}, err
	}
	if _, err := s.liveStepUpSession(ctx, in.SessionID, in.UserID, s.now()); err != nil {
		return passkey.Passkey{}, err
	}
	cred, err := s.relyingParty.FinishRegistration(ctx, webauthn.RegistrationFinish{State: binding.State, Response: in.Response})
	if err != nil {
		if errors.Is(err, sdk.ErrUnauthorized) {
			s.recordPasskeyFailure(ctx, passkeyCeremonyRegistration, in.UserID, relyingPartyFailureReason(err))
			return passkey.Passkey{}, ErrPasskeyRejected
		}
		return passkey.Passkey{}, err
	}
	existing, err := s.passkeys.ListByUser(ctx, in.UserID)
	if err != nil {
		return passkey.Passkey{}, err
	}
	name, ok := passkey.NormalizeName(in.Name)
	if !ok {
		name = passkey.DefaultName(len(existing))
	}
	created, err := s.passkeys.Create(ctx, passkey.CreateInput{Passkey: passkey.Passkey{
		ID:             s.ids.MustGenerate(),
		UserID:         in.UserID,
		Name:           name,
		CredentialID:   cred.ID,
		PublicKey:      cred.PublicKey,
		AAGUID:         cred.AAGUID,
		UserHandle:     binding.UserHandle,
		SignCount:      cred.SignCount,
		Transports:     cred.Transports,
		UserVerified:   cred.UserVerified,
		BackupEligible: cred.BackupEligible,
		BackupState:    cred.BackupState,
		CreatedAt:      s.now().UTC(),
	}, MaxPerUser: s.passkeyMaxPerUser, ExpectedAuthRevision: binding.AuthRevision, Bootstrap: binding.Tier != passkeyTierAAL2})
	if err != nil {
		switch {
		case errors.Is(err, sdk.ErrAlreadyExists):
			return passkey.Passkey{}, ErrPasskeyExists
		case errors.Is(err, passkey.ErrPasskeyLimit):
			return passkey.Passkey{}, ErrPasskeyLimit
		case errors.Is(err, sdk.ErrConflict):
			s.recordPasskeyFailure(ctx, passkeyCeremonyRegistration, in.UserID, securityevent.ReasonConflict)
			return passkey.Passkey{}, ErrStepUpRequired
		}
		return passkey.Passkey{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: in.UserID,
		Type:   securityevent.TypePasskeyRegistered,
		Status: securityevent.StatusSuccess,
		Details: map[string]any{
			"passkey_id":      created.ID,
			"user_verified":   created.UserVerified,
			"backup_eligible": created.BackupEligible,
			"aaguid":          passkeyAAGUID(created.AAGUID),
		},
	})
	s.enqueuePasskeyChangeNotices(ctx, in.UserID, passkeyChangeAdded, created.ID)
	return created, nil
}

// BeginPasskeySignIn starts a usernameless sign-in: a discoverable assertion
// whose options name no credential, so nothing about any account leaves the
// server. The ceremony row is keyed by its own handle; the route's per-IP
// budget bounds how many a client can open.
func (s *Service) BeginPasskeySignIn(ctx context.Context) (PasskeyCeremony, error) {
	if err := s.requirePasskeys(); err != nil {
		return PasskeyCeremony{}, err
	}
	started, err := s.relyingParty.BeginAssertion(ctx, webauthn.AssertionStart{UserVerification: s.passkeyUV})
	if err != nil {
		return PasskeyCeremony{}, err
	}
	return s.issuePasskeyCeremony(ctx, "", challenge.PurposePasskeyAssertion, started, passkeyBinding{State: started.State}, withDigestSubjectKey())
}

// FinishPasskeySignIn completes a usernameless sign-in in a fixed order:
// consume the ceremony, read the response's credential id, load the stored
// passkey and then its owner (active; revision R), verify the assertion against
// that record, refuse a regressed counter, record the use with a
// compare-and-swap (a passkey removed meanwhile is a conflict), and mint a
// session fenced on R, so a passkey removed or an account changed between
// lookup and mint is denied. A user-verified assertion signs in at AAL2,
// presence only at AAL1. Every refusal is ErrPasskeyRejected (an expired handle
// ErrChallengeExpired) with a passkey_failed row; success records passkey_login.
func (s *Service) FinishPasskeySignIn(ctx context.Context, in PasskeySignInInput) (TokenPair, user.User, error) {
	if err := s.requirePasskeys(); err != nil {
		return TokenPair{}, user.User{}, err
	}
	binding, err := s.consumePasskeyCeremony(ctx, challenge.PurposePasskeyAssertion, in.Ceremony, "", "")
	if err != nil {
		s.recordPasskeyFailure(ctx, passkeyCeremonySignIn, "", ceremonyFailureReason(err))
		return TokenPair{}, user.User{}, err
	}
	pk, u, assertion, err := s.verifyPasskeyAssertion(ctx, passkeyCeremonySignIn, binding, in.Response, nil)
	if err != nil {
		return TokenPair{}, user.User{}, err
	}
	method := passkeyMethod(pk, assertion)
	pair, err := s.mintSession(ctx, u.ID, u.AuthRevision, s.primaryAuthentication(method))
	if err != nil {
		if reason, ok := mintFailureReason(err); ok {
			s.recordPasskeyFailure(ctx, passkeyCeremonySignIn, "", reason)
			return TokenPair{}, user.User{}, ErrPasskeyRejected
		}
		return TokenPair{}, user.User{}, err
	}
	s.recordPasskeyLogin(ctx, u.ID, passkeyCeremonySignIn, method, assertion)
	return pair, u, nil
}

// BeginPasskeyStepUp starts a step-up from the caller's live session: a
// discoverable assertion like sign-in, its ceremony row bound to the session.
func (s *Service) BeginPasskeyStepUp(ctx context.Context, sessionID, userID string) (PasskeyCeremony, error) {
	if err := s.requirePasskeys(); err != nil {
		return PasskeyCeremony{}, err
	}
	if _, err := s.liveStepUpSession(ctx, sessionID, userID, s.now()); err != nil {
		return PasskeyCeremony{}, err
	}
	started, err := s.relyingParty.BeginAssertion(ctx, webauthn.AssertionStart{UserVerification: s.passkeyUV})
	if err != nil {
		return PasskeyCeremony{}, err
	}
	return s.issuePasskeyCeremony(ctx, userID, challenge.PurposePasskeyStepUp, started,
		passkeyBinding{State: started.State, SessionID: sessionID}, withSubjectKey(passkeySessionSubject(sessionID)))
}

// FinishPasskeyStepUp completes a step-up: the owner's revision R is read first,
// then the live session check, then sign-in's assertion order with an owner
// check (another user's passkey is refused, reason user_mismatch). Success re-mints the session — AuthenticatedAt now, the
// passkey added to its methods, the assertion's assurance, the replaced
// session's ExpiresAt — fenced on the owner's revision, then deletes the
// replaced session and its grants. A user-verified passkey yields AAL2, which
// RequireMFA gates and the AAL2 enrollment gate accept for the next five
// minutes; presence only yields AAL1.
func (s *Service) FinishPasskeyStepUp(ctx context.Context, in PasskeyStepUpInput) (StepUpResult, error) {
	if err := s.requirePasskeys(); err != nil {
		return StepUpResult{}, err
	}
	binding, err := s.consumePasskeyCeremony(ctx, challenge.PurposePasskeyStepUp, in.Ceremony, in.UserID, in.SessionID)
	if err != nil {
		s.recordPasskeyFailure(ctx, passkeyCeremonyStepUp, in.UserID, ceremonyFailureReason(err))
		return StepUpResult{}, err
	}
	owner, err := s.users.Get(ctx, in.UserID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return StepUpResult{}, ErrStepUpRequired
		}
		return StepUpResult{}, err
	}
	replaced, err := s.liveStepUpSession(ctx, in.SessionID, in.UserID, s.now())
	if err != nil {
		return StepUpResult{}, err
	}
	pk, u, assertion, err := s.verifyPasskeyAssertion(ctx, passkeyCeremonyStepUp, binding, in.Response, &owner)
	if err != nil {
		return StepUpResult{}, err
	}
	method := passkeyMethod(pk, assertion)
	auth := s.primaryAuthentication(method)
	auth.Methods = mergeMethods(replaced.Authentication.Methods, auth)
	pair, created, err := s.mintSessionUntil(ctx, u.ID, u.AuthRevision, auth, replaced.ExpiresAt)
	if err != nil {
		if reason, ok := mintFailureReason(err); ok {
			s.recordPasskeyFailure(ctx, passkeyCeremonyStepUp, in.UserID, reason)
			return StepUpResult{}, ErrPasskeyRejected
		}
		return StepUpResult{}, err
	}
	if err := s.sessions.Delete(ctx, replaced.ID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return StepUpResult{}, err
	}
	if s.authGrants != nil {
		if err := s.authGrants.DeleteBySession(ctx, replaced.ID); err != nil {
			s.logger.Warn("delete session grants failed", "error_kind", ErrorKind(err))
		}
	}
	s.recordPasskeyLogin(ctx, u.ID, passkeyCeremonyStepUp, method, assertion)
	return StepUpResult{Tokens: pair, User: u, Assurance: created.Authentication.Assurance}, nil
}

// verifyPasskeyAssertion runs the shared assertion steps after the ceremony is
// consumed: credential lookup, the owner — for step-up the already-read caller
// (owner), whose passkey it must be, for sign-in read here (revision R) —
// relying-party verification, the counter check and the use record. It records
// the refusal it returns; the passkey_failed row's UserID is the caller for
// step-up and empty for sign-in.
func (s *Service) verifyPasskeyAssertion(ctx context.Context, ceremony string, binding passkeyBinding, response []byte, owner *user.User) (passkey.Passkey, user.User, webauthn.Assertion, error) {
	ownerID := ""
	if owner != nil {
		ownerID = owner.ID
	}
	refuse := func(reason string) (passkey.Passkey, user.User, webauthn.Assertion, error) {
		s.recordPasskeyFailure(ctx, ceremony, ownerID, reason)
		return passkey.Passkey{}, user.User{}, webauthn.Assertion{}, ErrPasskeyRejected
	}
	handle, err := s.relyingParty.ReadAssertion(response)
	if err != nil {
		if errors.Is(err, sdk.ErrUnauthorized) {
			return refuse(securityevent.ReasonRejected)
		}
		return passkey.Passkey{}, user.User{}, webauthn.Assertion{}, err
	}
	pk, err := s.passkeys.GetByCredentialID(ctx, handle.CredentialID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return refuse(securityevent.ReasonUnknownCredential)
		}
		return passkey.Passkey{}, user.User{}, webauthn.Assertion{}, err
	}
	if owner != nil && pk.UserID != owner.ID {
		return refuse(securityevent.ReasonUserMismatch)
	}
	var u user.User
	if owner != nil {
		u = *owner
	} else if u, err = s.users.Get(ctx, pk.UserID); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return refuse(securityevent.ReasonUnknownCredential)
		}
		return passkey.Passkey{}, user.User{}, webauthn.Assertion{}, err
	}
	if !u.Active() {
		return refuse(securityevent.ReasonInactive)
	}
	assertion, err := s.relyingParty.FinishAssertion(ctx, webauthn.AssertionFinish{
		State:      binding.State,
		Response:   response,
		Credential: passkeyCredential(pk),
		UserHandle: pk.UserHandle,
	})
	if err != nil {
		if errors.Is(err, sdk.ErrUnauthorized) {
			return refuse(relyingPartyFailureReason(err))
		}
		return passkey.Passkey{}, user.User{}, webauthn.Assertion{}, err
	}
	if assertion.CounterRegressed {
		s.recordSecurityEvent(ctx, securityEventInput{
			UserID:   pk.UserID,
			Type:     securityevent.TypePasskeySignCountRegressed,
			Status:   securityevent.StatusBlocked,
			Details:  map[string]any{"passkey_id": pk.ID, "stored": pk.SignCount, "presented": assertion.SignCount},
			Budgeted: true,
		})
		return passkey.Passkey{}, user.User{}, webauthn.Assertion{}, ErrPasskeyRejected
	}
	if err := s.passkeys.RecordUse(ctx, pk.ID, pk.SignCount, assertion.SignCount, assertion.BackupState, s.now().UTC()); err != nil {
		if errors.Is(err, sdk.ErrConflict) {
			return refuse(securityevent.ReasonConflict)
		}
		return passkey.Passkey{}, user.User{}, webauthn.Assertion{}, err
	}
	return pk, u, assertion, nil
}

// issuePasskeyCeremony stores a started ceremony's state on the challenge rail
// and returns the handle the client echoes.
func (s *Service) issuePasskeyCeremony(ctx context.Context, userID, purpose string, started webauthn.Ceremony, binding passkeyBinding, key challengeOption) (PasskeyCeremony, error) {
	handle, issued, err := s.issueChallenge(ctx, userID, purpose, key, withStoredContext(binding))
	if err != nil {
		return PasskeyCeremony{}, err
	}
	return PasskeyCeremony{Ceremony: handle, PublicKey: started.Options, ExpiresAt: issued.ExpiresAt}, nil
}

// consumePasskeyCeremony spends a ceremony handle and returns its binding. A
// handle that is unknown, already spent, of another purpose, or bound to
// another user or session is ErrPasskeyRejected; an expired one
// ErrChallengeExpired. Either way the handle is spent.
func (s *Service) consumePasskeyCeremony(ctx context.Context, purpose, handle, userID, sessionID string) (passkeyBinding, error) {
	consumed, err := s.redeemToken(ctx, purpose, handle)
	if err != nil {
		switch {
		case errors.Is(err, ErrChallengeExpired):
			return passkeyBinding{}, ErrChallengeExpired
		case errors.Is(err, ErrChallengeInvalid):
			return passkeyBinding{}, ErrPasskeyRejected
		}
		return passkeyBinding{}, err
	}
	var binding passkeyBinding
	if json.Unmarshal(consumed.Context, &binding) != nil || len(binding.State) == 0 ||
		consumed.UserID != userID || binding.SessionID != sessionID {
		return passkeyBinding{}, ErrPasskeyRejected
	}
	return binding, nil
}

// recordPasskeyFailure appends a budgeted passkey_failed row.
func (s *Service) recordPasskeyFailure(ctx context.Context, ceremony, userID, reason string) {
	if reason == "" {
		return
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:   userID,
		Type:     securityevent.TypePasskeyFailed,
		Status:   securityevent.StatusFailure,
		Details:  map[string]any{"ceremony": ceremony, "reason": reason},
		Budgeted: true,
	})
}

// recordPasskeyLogin appends the passkey_login row of a minted session.
func (s *Service) recordPasskeyLogin(ctx context.Context, userID, ceremony string, method session.MethodKind, assertion webauthn.Assertion) {
	d, _ := session.DescribeMethod(method)
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Type:   securityevent.TypePasskeyLogin,
		Status: securityevent.StatusSuccess,
		Details: map[string]any{
			"ceremony":     ceremony,
			"assurance":    string(d.Assurance),
			"backup_state": assertion.BackupState,
		},
	})
}

// ceremonyFailureReason classifies a consumePasskeyCeremony refusal; an
// infrastructure error records nothing.
func ceremonyFailureReason(err error) string {
	switch {
	case errors.Is(err, ErrChallengeExpired):
		return securityevent.ReasonExpired
	case errors.Is(err, ErrPasskeyRejected):
		return securityevent.ReasonRejected
	}
	return ""
}

// relyingPartyFailureReason classifies a relying-party refusal.
func relyingPartyFailureReason(err error) string {
	if errors.Is(err, webauthn.ErrUserVerificationRequired) {
		return securityevent.ReasonUVRequired
	}
	return securityevent.ReasonRejected
}

// mintFailureReason classifies a fenced mint that refused a passkey proof: the
// owner deactivated or erased (inactive), or its revision advanced — a passkey
// removed, a credential changed — since the proof was read (conflict).
func mintFailureReason(err error) (string, bool) {
	switch {
	case errors.Is(err, session.ErrUserNotActive), errors.Is(err, sdk.ErrNotFound):
		return securityevent.ReasonInactive, true
	case errors.Is(err, sdk.ErrConflict):
		return securityevent.ReasonConflict, true
	}
	return "", false
}

// passkeyEnrollmentPolicy is the gate on adding or removing a passkey: AAL2
// once the user holds an AAL2-capable passkey, the bootstrap default otherwise.
func passkeyEnrollmentPolicy(existing []passkey.Passkey) RecentAuthPolicy {
	for _, p := range existing {
		if p.AAL2Capable() {
			return RecentAuthPolicy{MinAssurance: session.AssuranceAAL2}
		}
	}
	return RecentAuthPolicy{}
}

// passkeyMethod maps an assertion to its method kind: AAL2 only when the
// assertion is user-verified AND its passkey is AAL2-capable, so a passkey the
// enrollment gate counts as presence-only never mints AAL2.
func passkeyMethod(pk passkey.Passkey, assertion webauthn.Assertion) session.MethodKind {
	if assertion.UserVerified && pk.AAL2Capable() {
		return session.MethodPasskey
	}
	return session.MethodPasskeyUnverified
}

// mergeMethods keeps the replaced session's descriptors and appends auth's
// whose kind is new. Below AAL2 it drops a carried AAL2 passkey descriptor, so
// a presence-only step-up never keeps one.
func mergeMethods(current []session.AuthenticationMethod, auth session.AuthenticationMetadata) []session.AuthenticationMethod {
	var out []session.AuthenticationMethod
	for _, c := range current {
		if c.Kind == session.MethodPasskey && !auth.Assurance.AtLeast(session.AssuranceAAL2) {
			continue
		}
		out = append(out, c)
	}
	for _, m := range auth.Methods {
		seen := false
		for _, c := range out {
			seen = seen || c.Kind == m.Kind
		}
		if !seen {
			out = append(out, m)
		}
	}
	return out
}

// passkeyCredential is the stored record FinishAssertion verifies against.
func passkeyCredential(p passkey.Passkey) webauthn.Credential {
	return webauthn.Credential{
		ID:             p.CredentialID,
		PublicKey:      p.PublicKey,
		AAGUID:         p.AAGUID,
		SignCount:      p.SignCount,
		Transports:     p.Transports,
		UserVerified:   p.UserVerified,
		BackupEligible: p.BackupEligible,
		BackupState:    p.BackupState,
	}
}

// passkeyHandle returns the user handle the user's passkeys share, or nil.
func passkeyHandle(existing []passkey.Passkey) []byte {
	for _, p := range existing {
		if len(p.UserHandle) > 0 {
			return p.UserHandle
		}
	}
	return nil
}

func newPasskeyHandle() ([]byte, error) {
	handle := make([]byte, passkeyHandleBytes)
	if _, err := rand.Read(handle); err != nil {
		return nil, fmt.Errorf("mint passkey user handle: %w", err)
	}
	return handle, nil
}

// passkeyAccountNames are the names an authenticator's account chooser shows:
// the account's email, else its display name, else a generic label.
func passkeyAccountNames(view CurrentUserView) (name, display string) {
	name, display = view.Email, view.User.DisplayName
	if name == "" {
		name = display
	}
	if name == "" {
		name = "Account"
	}
	if display == "" {
		display = name
	}
	return name, display
}

// passkeySessionSubject keys a session-bound ceremony row.
func passkeySessionSubject(sessionID string) string {
	return "session:" + sessionID
}

// passkeyAAGUID renders an AAGUID as the 32 hex digits a passkey_registered row
// carries, or "" when the client reported none of the expected width.
func passkeyAAGUID(aaguid []byte) string {
	if len(aaguid) != 16 {
		return ""
	}
	return hex.EncodeToString(aaguid)
}

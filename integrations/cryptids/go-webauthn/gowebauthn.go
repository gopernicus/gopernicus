package gowebauthn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	gowa "github.com/go-webauthn/webauthn/webauthn"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
)

const (
	kindRegistration = "registration"
	kindAssertion    = "assertion"
)

var (
	_ webauthn.RelyingParty = (*RelyingParty)(nil)

	credentialParameters = []protocol.CredentialParameter{
		{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgES256},
		{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgEdDSA},
		{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgRS256},
	}

	errRecordMismatch = errors.New("the response names another credential or user handle than the stored record")
)

// RelyingParty implements webauthn.RelyingParty over go-webauthn. It is safe
// for concurrent use; every ceremony's state travels in Ceremony.State.
type RelyingParty struct {
	cfg   webauthn.Config
	begin *gowa.WebAuthn
}

// state is Ceremony.State: the go-webauthn session data and the ceremony kind
// it was begun for.
type state struct {
	Kind    string           `json:"kind"`
	Session gowa.SessionData `json:"session"`
}

// user is the go-webauthn view of one account.
type user struct {
	handle      []byte
	name        string
	displayName string
	credentials []gowa.Credential
}

// New validates cfg and returns a RelyingParty for it. Every error matches
// sdk.ErrInvalidInput: cfg.Validate's, a positive Timeout under a millisecond
// (WebAuthn timeouts are whole milliseconds), or a configuration go-webauthn
// refuses. A zero Timeout selects webauthn.MaxTimeout.
func New(cfg webauthn.Config) (*RelyingParty, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = webauthn.MaxTimeout
	}
	if cfg.Timeout < time.Millisecond {
		return nil, fmt.Errorf("gowebauthn: Timeout %v is under one millisecond: %w", cfg.Timeout, sdk.ErrInvalidInput)
	}
	// Begin never reads the origins; go-webauthn requires one.
	begin, err := library(cfg, "https://"+cfg.RPID, protocol.UnsolicitedOutputPolicyReject)
	if err != nil {
		return nil, fmt.Errorf("gowebauthn: %v: %w", err, sdk.ErrInvalidInput)
	}
	return &RelyingParty{cfg: cfg, begin: begin}, nil
}

// Config returns the validated configuration, Timeout resolved.
func (rp *RelyingParty) Config() webauthn.Config { return rp.cfg }

// BeginRegistration starts a registration ceremony for in.User.
func (rp *RelyingParty) BeginRegistration(_ context.Context, in webauthn.RegistrationStart) (webauthn.Ceremony, error) {
	if len(in.User.Handle) == 0 || len(in.User.Handle) > webauthn.MaxUserHandleBytes {
		return webauthn.Ceremony{}, fmt.Errorf("gowebauthn: User.Handle must be 1 to %d bytes: %w", webauthn.MaxUserHandleBytes, sdk.ErrInvalidInput)
	}
	uv, err := userVerification(in.UserVerification)
	if err != nil {
		return webauthn.Ceremony{}, err
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(in.Exclude))
	for _, id := range in.Exclude {
		exclude = append(exclude, protocol.CredentialDescriptor{Type: protocol.PublicKeyCredentialType, CredentialID: id})
	}
	creation, session, err := rp.begin.BeginRegistration(
		&user{handle: in.User.Handle, name: in.User.Name, displayName: in.User.DisplayName},
		gowa.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   uv,
		}),
		gowa.WithConveyancePreference(protocol.PreferNoAttestation),
		gowa.WithExclusions(exclude),
		gowa.WithCredentialParameters(credentialParameters),
	)
	if err != nil {
		return webauthn.Ceremony{}, fmt.Errorf("gowebauthn: begin registration: %v", err)
	}
	return newCeremony(kindRegistration, creation.Response, session)
}

// FinishRegistration verifies a registration response against its State and
// returns the new credential, its flags and count from this ceremony's
// authenticator data. Unsolicited extension outputs are rejected.
func (rp *RelyingParty) FinishRegistration(_ context.Context, in webauthn.RegistrationFinish) (webauthn.Credential, error) {
	session, err := readState(in.State, kindRegistration)
	if err != nil {
		return webauthn.Credential{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Response)
	if err != nil {
		return webauthn.Credential{}, rejected("parse the registration response", err)
	}
	origin := parsed.Response.CollectedClientData.Origin
	if !rp.cfg.Origins.Allows(origin) {
		return webauthn.Credential{}, rejected("admit the origin", fmt.Errorf("%q is outside the origin policy", origin))
	}
	authData := parsed.Response.AttestationObject.AuthData
	if authData.Flags.HasExtensions() {
		return webauthn.Credential{}, rejected("verify the registration", errors.New("unsolicited authenticator extension outputs"))
	}
	verifier, err := library(rp.cfg, origin, protocol.UnsolicitedOutputPolicyReject)
	if err != nil {
		return webauthn.Credential{}, rejected("configure the verifier", err)
	}
	requireUV := deferVerification(&session)
	created, err := verifier.CreateCredential(&user{handle: session.UserID}, session, parsed)
	if err != nil {
		return webauthn.Credential{}, rejected("verify the registration", err)
	}
	if requireUV && !authData.Flags.HasUserVerified() {
		return webauthn.Credential{}, webauthn.ErrUserVerificationRequired
	}
	var transports []string
	for _, t := range parsed.Response.Transports {
		transports = append(transports, string(t))
	}
	return webauthn.Credential{
		ID:             created.ID,
		PublicKey:      created.PublicKey,
		AAGUID:         authData.AttData.AAGUID,
		SignCount:      authData.Counter,
		Transports:     transports,
		UserVerified:   authData.Flags.HasUserVerified(),
		BackupEligible: authData.Flags.HasBackupEligible(),
		BackupState:    authData.Flags.HasBackupState(),
	}, nil
}

// BeginAssertion starts a discoverable assertion ceremony.
func (rp *RelyingParty) BeginAssertion(_ context.Context, in webauthn.AssertionStart) (webauthn.Ceremony, error) {
	uv, err := userVerification(in.UserVerification)
	if err != nil {
		return webauthn.Ceremony{}, err
	}
	assertion, session, err := rp.begin.BeginDiscoverableLogin(gowa.WithUserVerification(uv))
	if err != nil {
		return webauthn.Ceremony{}, fmt.Errorf("gowebauthn: begin assertion: %v", err)
	}
	return newCeremony(kindAssertion, assertion.Response, session)
}

// ReadAssertion returns an assertion response's unverified credential id and
// user handle.
func (rp *RelyingParty) ReadAssertion(response []byte) (webauthn.AssertionHandle, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return webauthn.AssertionHandle{}, rejected("parse the assertion response", err)
	}
	return webauthn.AssertionHandle{CredentialID: parsed.RawID, UserHandle: parsed.Response.UserHandle}, nil
}

// FinishAssertion verifies an assertion response against its State, the
// stored record and its owner's handle. The result comes from this ceremony's
// authenticator data. Unsolicited extension outputs are ignored (see the
// package documentation for why).
func (rp *RelyingParty) FinishAssertion(_ context.Context, in webauthn.AssertionFinish) (webauthn.Assertion, error) {
	session, err := readState(in.State, kindAssertion)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Response)
	if err != nil {
		return webauthn.Assertion{}, rejected("parse the assertion response", err)
	}
	origin := parsed.Response.CollectedClientData.Origin
	if !rp.cfg.Origins.Allows(origin) {
		return webauthn.Assertion{}, rejected("admit the origin", fmt.Errorf("%q is outside the origin policy", origin))
	}
	verifier, err := library(rp.cfg, origin, protocol.UnsolicitedOutputPolicyIgnore)
	if err != nil {
		return webauthn.Assertion{}, rejected("configure the verifier", err)
	}
	record := gowa.Credential{
		ID:            in.Credential.ID,
		PublicKey:     in.Credential.PublicKey,
		Flags:         gowa.CredentialFlags{BackupEligible: in.Credential.BackupEligible},
		Authenticator: gowa.Authenticator{SignCount: in.Credential.SignCount},
	}
	owner := func(rawID, userHandle []byte) (gowa.User, error) {
		if !bytes.Equal(rawID, in.Credential.ID) || !bytes.Equal(userHandle, in.UserHandle) {
			return nil, errRecordMismatch
		}
		return &user{handle: in.UserHandle, credentials: []gowa.Credential{record}}, nil
	}
	requireUV := deferVerification(&session)
	if _, _, err := verifier.ValidatePasskeyLogin(owner, session, parsed); err != nil {
		return webauthn.Assertion{}, rejected("verify the assertion", err)
	}
	authData := parsed.Response.AuthenticatorData
	if requireUV && !authData.Flags.HasUserVerified() {
		return webauthn.Assertion{}, webauthn.ErrUserVerificationRequired
	}
	presented, stored := authData.Counter, in.Credential.SignCount
	return webauthn.Assertion{
		SignCount:        presented,
		UserVerified:     authData.Flags.HasUserVerified(),
		BackupEligible:   authData.Flags.HasBackupEligible(),
		BackupState:      authData.Flags.HasBackupState(),
		CounterRegressed: (stored != 0 || presented != 0) && presented <= stored,
	}, nil
}

func (u *user) WebAuthnID() []byte                     { return u.handle }
func (u *user) WebAuthnName() string                   { return u.name }
func (u *user) WebAuthnDisplayName() string            { return u.displayName }
func (u *user) WebAuthnCredentials() []gowa.Credential { return u.credentials }

// library builds the go-webauthn instance for one origin. Timeouts are
// enforced, so Begin stamps the session's expiry and Finish refuses it after.
func library(cfg webauthn.Config, origin string, extensions protocol.UnsolicitedOutputPolicy) (*gowa.WebAuthn, error) {
	timeout := gowa.TimeoutConfig{Enforce: true, Timeout: cfg.Timeout, TimeoutUVD: cfg.Timeout}
	return gowa.New(&gowa.Config{
		RPID:                              cfg.RPID,
		RPDisplayName:                     cfg.RPName,
		RPOrigins:                         []string{origin},
		Timeouts:                          gowa.TimeoutsConfig{Login: timeout, Registration: timeout},
		ExtensionsUnsolicitedOutputPolicy: extensions,
	})
}

func newCeremony(kind string, options any, session *gowa.SessionData) (webauthn.Ceremony, error) {
	encodedOptions, err := json.Marshal(options)
	if err != nil {
		return webauthn.Ceremony{}, fmt.Errorf("gowebauthn: encode the %s options: %w", kind, err)
	}
	encodedState, err := json.Marshal(state{Kind: kind, Session: *session})
	if err != nil {
		return webauthn.Ceremony{}, fmt.Errorf("gowebauthn: encode the %s state: %w", kind, err)
	}
	return webauthn.Ceremony{Options: encodedOptions, State: encodedState, ExpiresAt: session.Expires}, nil
}

func readState(raw []byte, kind string) (gowa.SessionData, error) {
	var s state
	if err := json.Unmarshal(raw, &s); err != nil {
		return gowa.SessionData{}, rejected("read the state", err)
	}
	if s.Kind != kind {
		return gowa.SessionData{}, rejected("read the state", fmt.Errorf("a %q state cannot finish a %s", s.Kind, kind))
	}
	return s.Session, nil
}

// deferVerification lifts a Required user-verification check out of
// go-webauthn so it runs after every other check, and reports whether it was
// required.
func deferVerification(session *gowa.SessionData) bool {
	required := session.UserVerification == protocol.VerificationRequired
	session.UserVerification = protocol.VerificationPreferred
	return required
}

func userVerification(uv webauthn.Requirement) (protocol.UserVerificationRequirement, error) {
	switch uv {
	case "":
		return protocol.VerificationPreferred, nil
	case webauthn.Required:
		return protocol.VerificationRequired, nil
	case webauthn.Preferred:
		return protocol.VerificationPreferred, nil
	case webauthn.Discouraged:
		return protocol.VerificationDiscouraged, nil
	}
	return "", fmt.Errorf("gowebauthn: unknown UserVerification %q: %w", uv, sdk.ErrInvalidInput)
}

// rejected wraps a go-webauthn or adapter failure as ErrCeremonyRejected. The
// cause is formatted, not wrapped, so no library type crosses the port.
func rejected(step string, cause error) error {
	return fmt.Errorf("gowebauthn: %s: %v: %w", step, cause, webauthn.ErrCeremonyRejected)
}

// Package webauthnstub is a test-support WebAuthn relying party for this
// pocket's tests. It implements webauthn.RelyingParty over the responses the
// sdk's webauthntest.Authenticator produces — "none" attestation, ES256,
// discoverable credentials — and checks what the pocket depends on: the
// ceremony state, the clientDataJSON type, challenge and origin, the rpIdHash,
// user presence and verification, the stored credential and user handle, the
// backup flags, the ES256 signature and the counter — enough to pass
// webauthntest.Run. It reads only webauthntest's canonical ES256 COSE_Key, so it
// is not a general verifier: the implementation of record is the go-webauthn
// integration.
package webauthnstub

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
)

const (
	flagUserPresent    byte = 0x01
	flagUserVerified   byte = 0x04
	flagBackupEligible byte = 0x08
	flagBackupState    byte = 0x10
	flagAttestedData   byte = 0x40
	flagExtensionData  byte = 0x80

	registrationCeremony = "registration"
	assertionCeremony    = "assertion"
	// coseES256Len is the length of webauthntest's canonical EC2 P-256 COSE_Key.
	coseES256Len = 77
)

var b64 = base64.RawURLEncoding

// RelyingParty is the stub. Now is the clock state expiry is checked against;
// nil selects time.Now.
type RelyingParty struct {
	cfg webauthn.Config
	Now func() time.Time
}

type state struct {
	Ceremony         string               `json:"ceremony"`
	Challenge        []byte               `json:"challenge"`
	UserVerification webauthn.Requirement `json:"user_verification"`
	ExpiresAt        time.Time            `json:"expires_at"`
}

type clientData struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Origin    string `json:"origin"`
}

type response struct {
	ID       string `json:"id"`
	RawID    string `json:"rawId"`
	Type     string `json:"type"`
	Response struct {
		ClientDataJSON    string   `json:"clientDataJSON"`
		AuthenticatorData string   `json:"authenticatorData"`
		Signature         string   `json:"signature"`
		UserHandle        string   `json:"userHandle"`
		Transports        []string `json:"transports"`
	} `json:"response"`
}

// New validates cfg and returns the stub.
func New(cfg webauthn.Config) (*RelyingParty, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = webauthn.MaxTimeout
	}
	return &RelyingParty{cfg: cfg}, nil
}

// Config returns the validated configuration.
func (r *RelyingParty) Config() webauthn.Config { return r.cfg }

// BeginRegistration returns creation options and their state.
func (r *RelyingParty) BeginRegistration(_ context.Context, in webauthn.RegistrationStart) (webauthn.Ceremony, error) {
	if len(in.User.Handle) == 0 || len(in.User.Handle) > webauthn.MaxUserHandleBytes {
		return webauthn.Ceremony{}, fmt.Errorf("webauthnstub: user handle must be 1-%d bytes: %w", webauthn.MaxUserHandleBytes, sdk.ErrInvalidInput)
	}
	uv, err := requirement(in.UserVerification)
	if err != nil {
		return webauthn.Ceremony{}, err
	}
	exclude := make([]map[string]string, 0, len(in.Exclude))
	for _, id := range in.Exclude {
		exclude = append(exclude, map[string]string{"type": "public-key", "id": b64.EncodeToString(id)})
	}
	return r.begin(registrationCeremony, uv, func(challenge string) any {
		return map[string]any{
			"rp":                     map[string]string{"id": r.cfg.RPID, "name": r.cfg.RPName},
			"user":                   map[string]string{"id": b64.EncodeToString(in.User.Handle), "name": in.User.Name, "displayName": in.User.DisplayName},
			"challenge":              challenge,
			"pubKeyCredParams":       []map[string]any{{"type": "public-key", "alg": -7}, {"type": "public-key", "alg": -8}, {"type": "public-key", "alg": -257}},
			"timeout":                r.cfg.Timeout.Milliseconds(),
			"excludeCredentials":     exclude,
			"authenticatorSelection": map[string]any{"residentKey": "required", "requireResidentKey": true, "userVerification": uv},
			"attestation":            "none",
		}
	})
}

// FinishRegistration verifies a registration response.
func (r *RelyingParty) FinishRegistration(_ context.Context, in webauthn.RegistrationFinish) (webauthn.Credential, error) {
	st, err := r.state(in.State, registrationCeremony)
	if err != nil {
		return webauthn.Credential{}, err
	}
	resp, _, err := r.verifyClientData(in.Response, "webauthn.create", st)
	if err != nil {
		return webauthn.Credential{}, err
	}
	authData, err := b64.DecodeString(resp.Response.AuthenticatorData)
	if err != nil || len(authData) < 37+16+2 {
		return webauthn.Credential{}, rejected("authenticator data is malformed")
	}
	flags, count, err := r.checkAuthData(authData, st)
	if err != nil {
		return webauthn.Credential{}, err
	}
	if flags&flagAttestedData == 0 || flags&flagExtensionData != 0 {
		return webauthn.Credential{}, rejected("registration needs attested data and no extension outputs")
	}
	if flags&flagBackupState != 0 && flags&flagBackupEligible == 0 {
		return webauthn.Credential{}, rejected("backup state without backup eligibility")
	}
	aaguid := authData[37:53]
	idLen := int(binary.BigEndian.Uint16(authData[53:55]))
	if len(authData) != 55+idLen+coseES256Len {
		return webauthn.Credential{}, rejected("attested credential data is malformed")
	}
	id := authData[55 : 55+idLen]
	if rawID, err := b64.DecodeString(resp.RawID); err != nil || !bytes.Equal(rawID, id) {
		return webauthn.Credential{}, rejected("rawId differs from the attested credential id")
	}
	publicKey := authData[55+idLen:]
	if _, err := coseES256(publicKey); err != nil {
		return webauthn.Credential{}, err
	}
	return webauthn.Credential{
		ID:             slices.Clone(id),
		PublicKey:      slices.Clone(publicKey),
		AAGUID:         slices.Clone(aaguid),
		SignCount:      count,
		Transports:     slices.Clone(resp.Response.Transports),
		UserVerified:   flags&flagUserVerified != 0,
		BackupEligible: flags&flagBackupEligible != 0,
		BackupState:    flags&flagBackupState != 0,
	}, nil
}

// BeginAssertion returns discoverable request options and their state.
func (r *RelyingParty) BeginAssertion(_ context.Context, in webauthn.AssertionStart) (webauthn.Ceremony, error) {
	uv, err := requirement(in.UserVerification)
	if err != nil {
		return webauthn.Ceremony{}, err
	}
	return r.begin(assertionCeremony, uv, func(challenge string) any {
		return map[string]any{
			"challenge":        challenge,
			"timeout":          r.cfg.Timeout.Milliseconds(),
			"rpId":             r.cfg.RPID,
			"allowCredentials": []any{},
			"userVerification": uv,
		}
	})
}

// ReadAssertion returns the response's unverified credential id and handle.
func (r *RelyingParty) ReadAssertion(raw []byte) (webauthn.AssertionHandle, error) {
	var resp response
	if json.Unmarshal(raw, &resp) != nil {
		return webauthn.AssertionHandle{}, rejected("assertion is not JSON")
	}
	id, err := b64.DecodeString(resp.RawID)
	if err != nil || len(id) == 0 || resp.ID != resp.RawID {
		return webauthn.AssertionHandle{}, rejected("rawId is malformed or differs from id")
	}
	handle, err := b64.DecodeString(resp.Response.UserHandle)
	if err != nil {
		return webauthn.AssertionHandle{}, rejected("userHandle is malformed")
	}
	return webauthn.AssertionHandle{CredentialID: id, UserHandle: handle}, nil
}

// FinishAssertion verifies an assertion against the stored record.
func (r *RelyingParty) FinishAssertion(_ context.Context, in webauthn.AssertionFinish) (webauthn.Assertion, error) {
	st, err := r.state(in.State, assertionCeremony)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	resp, clientJSON, err := r.verifyClientData(in.Response, "webauthn.get", st)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	handle, err := r.ReadAssertion(in.Response)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	if !bytes.Equal(handle.CredentialID, in.Credential.ID) || !bytes.Equal(handle.UserHandle, in.UserHandle) {
		return webauthn.Assertion{}, rejected("credential id or user handle differs from the stored record")
	}
	authData, err := b64.DecodeString(resp.Response.AuthenticatorData)
	if err != nil || len(authData) < 37 {
		return webauthn.Assertion{}, rejected("authenticator data is malformed")
	}
	flags, count, err := r.checkAuthData(authData, st)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	eligible, backedUp := flags&flagBackupEligible != 0, flags&flagBackupState != 0
	if eligible != in.Credential.BackupEligible || (backedUp && !eligible) {
		return webauthn.Assertion{}, rejected("backup flags are inconsistent")
	}
	key, err := coseES256(in.Credential.PublicKey)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	signature, err := b64.DecodeString(resp.Response.Signature)
	if err != nil {
		return webauthn.Assertion{}, rejected("signature is malformed")
	}
	clientHash := sha256.Sum256(clientJSON)
	digest := sha256.Sum256(slices.Concat(authData, clientHash[:]))
	if !ecdsa.VerifyASN1(key, digest[:], signature) {
		return webauthn.Assertion{}, rejected("signature does not verify")
	}
	stored := in.Credential.SignCount
	return webauthn.Assertion{
		SignCount:        count,
		UserVerified:     flags&flagUserVerified != 0,
		BackupEligible:   eligible,
		BackupState:      backedUp,
		CounterRegressed: (stored != 0 || count != 0) && count <= stored,
	}, nil
}

func (r *RelyingParty) begin(ceremony string, uv webauthn.Requirement, options func(challenge string) any) (webauthn.Ceremony, error) {
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		return webauthn.Ceremony{}, err
	}
	opts, err := json.Marshal(options(b64.EncodeToString(challenge)))
	if err != nil {
		return webauthn.Ceremony{}, err
	}
	expires := r.now().Add(r.cfg.Timeout)
	st, err := json.Marshal(state{Ceremony: ceremony, Challenge: challenge, UserVerification: uv, ExpiresAt: expires})
	if err != nil {
		return webauthn.Ceremony{}, err
	}
	return webauthn.Ceremony{Options: opts, State: st, ExpiresAt: expires}, nil
}

func (r *RelyingParty) state(raw []byte, ceremony string) (state, error) {
	var st state
	if json.Unmarshal(raw, &st) != nil || st.Ceremony != ceremony || len(st.Challenge) == 0 {
		return state{}, rejected("ceremony state is foreign or malformed")
	}
	if !r.now().Before(st.ExpiresAt) {
		return state{}, rejected("ceremony state expired")
	}
	return st, nil
}

// verifyClientData parses a response and checks its clientDataJSON, returning
// the response and the raw clientDataJSON bytes.
func (r *RelyingParty) verifyClientData(raw []byte, ceremonyType string, st state) (response, []byte, error) {
	var resp response
	if json.Unmarshal(raw, &resp) != nil || resp.Type != "public-key" {
		return response{}, nil, rejected("response is malformed")
	}
	clientJSON, err := b64.DecodeString(resp.Response.ClientDataJSON)
	if err != nil {
		return response{}, nil, rejected("clientDataJSON is malformed")
	}
	var cd clientData
	if json.Unmarshal(clientJSON, &cd) != nil || cd.Type != ceremonyType {
		return response{}, nil, rejected("clientDataJSON type is wrong")
	}
	if challenge, err := b64.DecodeString(cd.Challenge); err != nil || !bytes.Equal(challenge, st.Challenge) {
		return response{}, nil, rejected("challenge does not match")
	}
	if !r.cfg.Origins.Allows(cd.Origin) {
		return response{}, nil, rejected("origin is not admitted")
	}
	return resp, clientJSON, nil
}

// checkAuthData checks the rpIdHash, presence and verification, returning the
// flags and the counter.
func (r *RelyingParty) checkAuthData(authData []byte, st state) (byte, uint32, error) {
	want := sha256.Sum256([]byte(r.cfg.RPID))
	if !bytes.Equal(authData[:32], want[:]) {
		return 0, 0, rejected("rpIdHash does not match")
	}
	flags := authData[32]
	if flags&flagUserPresent == 0 {
		return 0, 0, rejected("user presence missing")
	}
	if st.UserVerification == webauthn.Required && flags&flagUserVerified == 0 {
		return 0, 0, fmt.Errorf("webauthnstub: %w", webauthn.ErrUserVerificationRequired)
	}
	return flags, binary.BigEndian.Uint32(authData[33:37]), nil
}

func (r *RelyingParty) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// coseES256 reads webauthntest's canonical EC2 P-256 ES256 COSE_Key.
func coseES256(raw []byte) (*ecdsa.PublicKey, error) {
	head := []byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20}
	if len(raw) != coseES256Len || !bytes.Equal(raw[:10], head) || !bytes.Equal(raw[42:45], []byte{0x22, 0x58, 0x20}) {
		return nil, rejected("public key is not a canonical ES256 COSE_Key")
	}
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), slices.Concat([]byte{4}, raw[10:42], raw[45:77]))
	if err != nil {
		return nil, rejected("public key is not a P-256 point")
	}
	return key, nil
}

func requirement(uv webauthn.Requirement) (webauthn.Requirement, error) {
	switch uv {
	case "":
		return webauthn.Preferred, nil
	case webauthn.Required, webauthn.Preferred, webauthn.Discouraged:
		return uv, nil
	}
	return "", fmt.Errorf("webauthnstub: unknown user verification requirement %q: %w", uv, sdk.ErrInvalidInput)
}

func rejected(why string) error {
	return fmt.Errorf("webauthnstub: %s: %w", why, webauthn.ErrCeremonyRejected)
}

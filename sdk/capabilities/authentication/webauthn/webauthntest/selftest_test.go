package webauthntest_test

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
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn/webauthntest"
)

const (
	registrationCeremony = "registration"
	assertionCeremony    = "assertion"

	flagUserPresent    byte = 0x01
	flagUserVerified   byte = 0x04
	flagBackupEligible byte = 0x08
	flagBackupState    byte = 0x10
	flagAttestedData   byte = 0x40
	flagExtensionData  byte = 0x80

	maxCBORDepth = 8
)

var b64 = base64.RawURLEncoding

// reference is a test-only relying party: stdlib crypto, ES256 only, and a
// strict CBOR decoder written independently of the Authenticator's encoder. It
// follows the WebAuthn Level 3 verification procedures (§7.1 registration,
// §7.2 assertion) for "none" attestation, so Run over it proves both that the
// Authenticator's bytes verify and that the suite is satisfiable. It is not a
// default implementation: the port has none.
type reference struct {
	cfg webauthn.Config
}

// referenceState is held server-side by the caller, so it needs no MAC.
type referenceState struct {
	Ceremony         string               `json:"ceremony"`
	Challenge        []byte               `json:"challenge"`
	UserVerification webauthn.Requirement `json:"user_verification"`
	ExpiresAt        time.Time            `json:"expires_at"`
}

type authData struct {
	flags byte
	count uint32
	rest  []byte // attested credential data, then extensions
}

type assertionResponse struct {
	rawID, clientData, authData, signature, userHandle []byte
}

func TestRunReference(t *testing.T) {
	webauthntest.Run(t, newReference)
}

func newReference(cfg webauthn.Config) (webauthn.RelyingParty, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = webauthn.MaxTimeout
	}
	return &reference{cfg: cfg}, nil
}

func (r *reference) Config() webauthn.Config { return r.cfg }

func (r *reference) BeginRegistration(_ context.Context, in webauthn.RegistrationStart) (webauthn.Ceremony, error) {
	if len(in.User.Handle) == 0 || len(in.User.Handle) > webauthn.MaxUserHandleBytes {
		return webauthn.Ceremony{}, fmt.Errorf("reference: user handle must be 1-%d bytes: %w", webauthn.MaxUserHandleBytes, sdk.ErrInvalidInput)
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
			"rp":        map[string]string{"id": r.cfg.RPID, "name": r.cfg.RPName},
			"user":      map[string]string{"id": b64.EncodeToString(in.User.Handle), "name": in.User.Name, "displayName": in.User.DisplayName},
			"challenge": challenge,
			"pubKeyCredParams": []map[string]any{
				{"type": "public-key", "alg": -7}, {"type": "public-key", "alg": -8}, {"type": "public-key", "alg": -257},
			},
			"timeout":                r.cfg.Timeout.Milliseconds(),
			"excludeCredentials":     exclude,
			"authenticatorSelection": map[string]any{"residentKey": "required", "requireResidentKey": true, "userVerification": uv},
			"attestation":            "none",
		}
	})
}

func (r *reference) FinishRegistration(_ context.Context, in webauthn.RegistrationFinish) (webauthn.Credential, error) {
	state, err := r.state(in.State, registrationCeremony)
	if err != nil {
		return webauthn.Credential{}, err
	}
	var resp struct {
		ID       string `json:"id"`
		RawID    string `json:"rawId"`
		Type     string `json:"type"`
		Response struct {
			ClientDataJSON    string   `json:"clientDataJSON"`
			AttestationObject string   `json:"attestationObject"`
			Transports        []string `json:"transports"`
		} `json:"response"`
	}
	if err := json.Unmarshal(in.Response, &resp); err != nil {
		return webauthn.Credential{}, rejected("response is not JSON")
	}
	rawID, err := credentialID(resp.ID, resp.RawID, resp.Type)
	if err != nil {
		return webauthn.Credential{}, err
	}
	clientData, err := b64.DecodeString(resp.Response.ClientDataJSON)
	if err != nil {
		return webauthn.Credential{}, rejected("clientDataJSON is not base64url")
	}
	if err := r.checkClientData(clientData, "webauthn.create", state.Challenge); err != nil {
		return webauthn.Credential{}, err
	}
	attestationBytes, err := b64.DecodeString(resp.Response.AttestationObject)
	if err != nil {
		return webauthn.Credential{}, rejected("attestationObject is not base64url")
	}
	item, n, err := decodeCBOR(attestationBytes, 0)
	attestation, isMap := item.(map[any]any)
	if err != nil || n != len(attestationBytes) || !isMap || len(attestation) != 3 {
		return webauthn.Credential{}, rejected("attestationObject is not one {fmt, attStmt, authData} map")
	}
	if format, _ := attestation["fmt"].(string); format != "none" {
		return webauthn.Credential{}, rejected("attestation format is not none")
	}
	if stmt, ok := attestation["attStmt"].(map[any]any); !ok || len(stmt) != 0 {
		return webauthn.Credential{}, rejected("a none attestation statement is not empty")
	}
	raw, _ := attestation["authData"].([]byte)
	ad, err := r.parseAuthData(raw)
	if err != nil {
		return webauthn.Credential{}, err
	}
	if ad.flags&flagAttestedData == 0 {
		return webauthn.Credential{}, rejected("no attested credential data")
	}
	if len(ad.rest) < 18 {
		return webauthn.Credential{}, rejected("attested credential data is truncated")
	}
	aaguid, idLength := ad.rest[:16], int(binary.BigEndian.Uint16(ad.rest[16:18]))
	if len(ad.rest) < 18+idLength {
		return webauthn.Credential{}, rejected("credential id is truncated")
	}
	id, afterID := ad.rest[18:18+idLength], ad.rest[18+idLength:]
	_, keyLength, err := decodeCBOR(afterID, 0)
	if err != nil {
		return webauthn.Credential{}, rejected("credential public key is not CBOR")
	}
	key, extensions := afterID[:keyLength], afterID[keyLength:]
	if ad.flags&flagExtensionData != 0 {
		if _, n, err := decodeCBOR(extensions, 0); err != nil || n != len(extensions) {
			return webauthn.Credential{}, rejected("extension outputs are not one CBOR map")
		}
		return webauthn.Credential{}, rejected("unsolicited authenticator extension outputs")
	} else if len(extensions) != 0 {
		return webauthn.Credential{}, rejected("leftover authenticator data")
	}
	if !bytes.Equal(id, rawID) {
		return webauthn.Credential{}, rejected("attested credential id differs from rawId")
	}
	if _, err := coseES256(key); err != nil {
		return webauthn.Credential{}, err
	}
	if state.UserVerification == webauthn.Required && ad.flags&flagUserVerified == 0 {
		return webauthn.Credential{}, fmt.Errorf("reference: registration: %w", webauthn.ErrUserVerificationRequired)
	}
	return webauthn.Credential{
		ID:             bytes.Clone(rawID),
		PublicKey:      bytes.Clone(key),
		AAGUID:         bytes.Clone(aaguid),
		SignCount:      ad.count,
		Transports:     slices.Clone(resp.Response.Transports),
		UserVerified:   ad.flags&flagUserVerified != 0,
		BackupEligible: ad.flags&flagBackupEligible != 0,
		BackupState:    ad.flags&flagBackupState != 0,
	}, nil
}

func (r *reference) BeginAssertion(_ context.Context, in webauthn.AssertionStart) (webauthn.Ceremony, error) {
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

func (r *reference) ReadAssertion(response []byte) (webauthn.AssertionHandle, error) {
	parsed, err := parseAssertion(response)
	if err != nil {
		return webauthn.AssertionHandle{}, err
	}
	return webauthn.AssertionHandle{CredentialID: parsed.rawID, UserHandle: parsed.userHandle}, nil
}

func (r *reference) FinishAssertion(_ context.Context, in webauthn.AssertionFinish) (webauthn.Assertion, error) {
	state, err := r.state(in.State, assertionCeremony)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	parsed, err := parseAssertion(in.Response)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	if !bytes.Equal(parsed.rawID, in.Credential.ID) {
		return webauthn.Assertion{}, rejected("credential id differs from the stored record")
	}
	if len(parsed.userHandle) == 0 || !bytes.Equal(parsed.userHandle, in.UserHandle) {
		return webauthn.Assertion{}, rejected("user handle differs from the record owner's")
	}
	if err := r.checkClientData(parsed.clientData, "webauthn.get", state.Challenge); err != nil {
		return webauthn.Assertion{}, err
	}
	ad, err := r.parseAuthData(parsed.authData)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	if ad.flags&flagAttestedData != 0 {
		return webauthn.Assertion{}, rejected("an assertion carries attested credential data")
	}
	if ad.flags&flagExtensionData != 0 {
		if _, n, err := decodeCBOR(ad.rest, 0); err != nil || n != len(ad.rest) {
			return webauthn.Assertion{}, rejected("extension outputs are not one CBOR map")
		}
	} else if len(ad.rest) != 0 {
		return webauthn.Assertion{}, rejected("leftover authenticator data")
	}
	if eligible := ad.flags&flagBackupEligible != 0; eligible != in.Credential.BackupEligible {
		return webauthn.Assertion{}, rejected("backup eligibility changed")
	}
	key, err := coseES256(in.Credential.PublicKey)
	if err != nil {
		return webauthn.Assertion{}, err
	}
	clientHash := sha256.Sum256(parsed.clientData)
	digest := sha256.Sum256(slices.Concat(parsed.authData, clientHash[:]))
	if !ecdsa.VerifyASN1(key, digest[:], parsed.signature) {
		return webauthn.Assertion{}, rejected("signature")
	}
	if state.UserVerification == webauthn.Required && ad.flags&flagUserVerified == 0 {
		return webauthn.Assertion{}, fmt.Errorf("reference: assertion: %w", webauthn.ErrUserVerificationRequired)
	}
	stored := in.Credential.SignCount
	return webauthn.Assertion{
		SignCount:        ad.count,
		UserVerified:     ad.flags&flagUserVerified != 0,
		BackupEligible:   ad.flags&flagBackupEligible != 0,
		BackupState:      ad.flags&flagBackupState != 0,
		CounterRegressed: (stored != 0 || ad.count != 0) && ad.count <= stored,
	}, nil
}

func (r *reference) begin(ceremony string, uv webauthn.Requirement, options func(challenge string) any) (webauthn.Ceremony, error) {
	challenge := make([]byte, 32)
	_, _ = rand.Read(challenge)
	expires := time.Now().Add(r.cfg.Timeout)
	opts, err := json.Marshal(options(b64.EncodeToString(challenge)))
	if err != nil {
		return webauthn.Ceremony{}, err
	}
	state, err := json.Marshal(referenceState{Ceremony: ceremony, Challenge: challenge, UserVerification: uv, ExpiresAt: expires})
	if err != nil {
		return webauthn.Ceremony{}, err
	}
	return webauthn.Ceremony{Options: opts, State: state, ExpiresAt: expires}, nil
}

func (r *reference) state(raw []byte, ceremony string) (referenceState, error) {
	var s referenceState
	if err := json.Unmarshal(raw, &s); err != nil || s.Ceremony != ceremony || len(s.Challenge) < 16 {
		return referenceState{}, rejected("state is not a " + ceremony + " state")
	}
	if !time.Now().Before(s.ExpiresAt) {
		return referenceState{}, rejected("state expired")
	}
	return s, nil
}

func (r *reference) checkClientData(raw []byte, ceremonyType string, challenge []byte) error {
	var cd struct {
		Type        string `json:"type"`
		Challenge   string `json:"challenge"`
		Origin      string `json:"origin"`
		CrossOrigin bool   `json:"crossOrigin"`
	}
	if err := json.Unmarshal(raw, &cd); err != nil {
		return rejected("clientDataJSON is not JSON")
	}
	if cd.Type != ceremonyType {
		return rejected("clientDataJSON type " + cd.Type)
	}
	if cd.Challenge != b64.EncodeToString(challenge) {
		return rejected("challenge")
	}
	if !r.cfg.Origins.Allows(cd.Origin) || cd.CrossOrigin {
		return rejected("origin")
	}
	return nil
}

func (r *reference) parseAuthData(raw []byte) (authData, error) {
	if len(raw) < 37 {
		return authData{}, rejected("authenticator data is shorter than 37 bytes")
	}
	if hash := sha256.Sum256([]byte(r.cfg.RPID)); !bytes.Equal(raw[:32], hash[:]) {
		return authData{}, rejected("rpIdHash")
	}
	ad := authData{flags: raw[32], count: binary.BigEndian.Uint32(raw[33:37]), rest: raw[37:]}
	if ad.flags&flagUserPresent == 0 {
		return authData{}, rejected("user presence")
	}
	if ad.flags&flagBackupState != 0 && ad.flags&flagBackupEligible == 0 {
		return authData{}, rejected("backup state without backup eligibility")
	}
	return ad, nil
}

func parseAssertion(response []byte) (assertionResponse, error) {
	var resp struct {
		ID       string `json:"id"`
		RawID    string `json:"rawId"`
		Type     string `json:"type"`
		Response struct {
			ClientDataJSON    string `json:"clientDataJSON"`
			AuthenticatorData string `json:"authenticatorData"`
			Signature         string `json:"signature"`
			UserHandle        string `json:"userHandle"`
		} `json:"response"`
	}
	if err := json.Unmarshal(response, &resp); err != nil {
		return assertionResponse{}, rejected("response is not JSON")
	}
	rawID, err := credentialID(resp.ID, resp.RawID, resp.Type)
	if err != nil {
		return assertionResponse{}, err
	}
	var parsed assertionResponse
	parsed.rawID = rawID
	for _, field := range []struct {
		in       string
		out      *[]byte
		optional bool
	}{
		{resp.Response.ClientDataJSON, &parsed.clientData, false},
		{resp.Response.AuthenticatorData, &parsed.authData, false},
		{resp.Response.Signature, &parsed.signature, false},
		{resp.Response.UserHandle, &parsed.userHandle, true},
	} {
		v, err := b64.DecodeString(field.in)
		if err != nil || (len(v) == 0 && !field.optional) {
			return assertionResponse{}, rejected("assertion response member is missing or not base64url")
		}
		*field.out = v
	}
	if len(parsed.authData) < 37 {
		return assertionResponse{}, rejected("authenticator data is shorter than 37 bytes")
	}
	return parsed, nil
}

func credentialID(id, rawID, credentialType string) ([]byte, error) {
	if credentialType != "public-key" {
		return nil, rejected("credential type is not public-key")
	}
	raw, err := b64.DecodeString(rawID)
	if err != nil || len(raw) == 0 {
		return nil, rejected("rawId is not base64url")
	}
	fromID, err := b64.DecodeString(id)
	if err != nil || !bytes.Equal(fromID, raw) {
		return nil, rejected("id differs from rawId")
	}
	return raw, nil
}

// coseES256 parses an EC2 P-256 ES256 COSE_Key and nothing else.
func coseES256(raw []byte) (*ecdsa.PublicKey, error) {
	item, n, err := decodeCBOR(raw, 0)
	key, isMap := item.(map[any]any)
	if err != nil || n != len(raw) || !isMap || len(key) != 5 {
		return nil, rejected("public key is not a five-member COSE_Key")
	}
	x, _ := key[int64(-2)].([]byte)
	y, _ := key[int64(-3)].([]byte)
	if key[int64(1)] != int64(2) || key[int64(3)] != int64(-7) || key[int64(-1)] != int64(1) || len(x) != 32 || len(y) != 32 {
		return nil, rejected("public key is not an ES256 P-256 COSE_Key")
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), slices.Concat([]byte{4}, x, y))
	if err != nil {
		return nil, rejected("public key is not a P-256 point")
	}
	return pub, nil
}

func requirement(uv webauthn.Requirement) (webauthn.Requirement, error) {
	switch uv {
	case "":
		return webauthn.Preferred, nil
	case webauthn.Required, webauthn.Preferred, webauthn.Discouraged:
		return uv, nil
	}
	return "", fmt.Errorf("reference: unknown user verification requirement %q: %w", uv, sdk.ErrInvalidInput)
}

func rejected(why string) error {
	return fmt.Errorf("reference: %s: %w", why, webauthn.ErrCeremonyRejected)
}

// decodeCBOR decodes one CTAP2-canonical CBOR item and returns it with the
// bytes it consumed. Integers decode to int64, byte strings to []byte, text to
// string, arrays to []any and maps to map[any]any. It refuses non-shortest
// heads, indefinite lengths, tags, floats, and duplicate or unsorted map keys,
// so the Authenticator's encoder is checked against the canonical rules rather
// than against itself.
func decodeCBOR(b []byte, depth int) (any, int, error) {
	if depth > maxCBORDepth {
		return nil, 0, errors.New("cbor: nested too deeply")
	}
	if len(b) == 0 {
		return nil, 0, errors.New("cbor: truncated")
	}
	major, info := b[0]>>5, b[0]&0x1f
	arg, off, err := cborArgument(b, info)
	if err != nil {
		return nil, 0, err
	}
	switch major {
	case 0, 1:
		if arg > math.MaxInt64 {
			return nil, 0, errors.New("cbor: integer overflows int64")
		}
		if major == 1 {
			return -1 - int64(arg), off, nil
		}
		return int64(arg), off, nil
	case 2, 3:
		if arg > uint64(len(b)-off) {
			return nil, 0, errors.New("cbor: string truncated")
		}
		v := b[off : off+int(arg)]
		if major == 3 {
			if !utf8.Valid(v) {
				return nil, 0, errors.New("cbor: text is not UTF-8")
			}
			return string(v), off + int(arg), nil
		}
		return bytes.Clone(v), off + int(arg), nil
	case 4:
		if arg > uint64(len(b)) {
			return nil, 0, errors.New("cbor: array truncated")
		}
		items := make([]any, 0, arg)
		for range arg {
			item, n, err := decodeCBOR(b[off:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			items = append(items, item)
			off += n
		}
		return items, off, nil
	case 5:
		if arg > uint64(len(b)) {
			return nil, 0, errors.New("cbor: map truncated")
		}
		m := make(map[any]any, arg)
		var previous []byte
		for range arg {
			key, n, err := decodeCBOR(b[off:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			encodedKey := b[off : off+n]
			switch key.(type) {
			case int64, string:
			default:
				return nil, 0, errors.New("cbor: map key is not an integer or text")
			}
			if previous != nil && !canonicallyBefore(previous, encodedKey) {
				return nil, 0, errors.New("cbor: map keys are duplicated or not in canonical order")
			}
			previous = encodedKey
			off += n
			value, n, err := decodeCBOR(b[off:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			m[key] = value
			off += n
		}
		return m, off, nil
	case 7:
		switch arg {
		case 20:
			return false, off, nil
		case 21:
			return true, off, nil
		case 22:
			return nil, off, nil
		}
		return nil, 0, errors.New("cbor: unsupported simple value or float")
	}
	return nil, 0, errors.New("cbor: tags are not allowed")
}

// cborArgument reads a head's argument, refusing non-shortest forms and
// indefinite lengths.
func cborArgument(b []byte, info byte) (uint64, int, error) {
	if info < 24 {
		return uint64(info), 1, nil
	}
	if info > 27 {
		return 0, 0, errors.New("cbor: indefinite or reserved length")
	}
	size := 1 << (info - 24)
	if len(b) < 1+size {
		return 0, 0, errors.New("cbor: head truncated")
	}
	var v uint64
	for _, c := range b[1 : 1+size] {
		v = v<<8 | uint64(c)
	}
	floor := map[byte]uint64{24: 24, 25: 0x100, 26: 0x10000, 27: 0x100000000}[info]
	if v < floor {
		return 0, 0, errors.New("cbor: argument not in its shortest form")
	}
	return v, 1 + size, nil
}

// canonicallyBefore orders encoded keys by major type, then length, then bytes
// (CTAP2 canonical CBOR).
func canonicallyBefore(a, b []byte) bool {
	if a[0]>>5 != b[0]>>5 {
		return a[0]>>5 < b[0]>>5
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return bytes.Compare(a, b) < 0
}

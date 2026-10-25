package webauthntest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Authenticator-data flags (WebAuthn §6.1).
const (
	flagUserPresent    byte = 0x01
	flagUserVerified   byte = 0x04
	flagBackupEligible byte = 0x08
	flagBackupState    byte = 0x10
	flagAttestedData   byte = 0x40
	flagExtensionData  byte = 0x80
)

const (
	coseAlgES256      int64 = -7
	publicKeyType           = "public-key"
	transportInternal       = "internal"
	createType              = "webauthn.create"
	getType                 = "webauthn.get"
)

var (
	// ErrNoCredential: the authenticator holds no credential the options
	// admit (a browser reports NotAllowedError).
	ErrNoCredential = errors.New("webauthntest: no credential for this relying party")

	// ErrCredentialExcluded: the options exclude a credential this
	// authenticator holds (a browser reports InvalidStateError).
	ErrCredentialExcluded = errors.New("webauthntest: the authenticator holds an excluded credential")

	// testAAGUID is the 16-byte AAGUID every credential reports.
	testAAGUID = []byte("webauthntest-aag")

	b64 = base64.RawURLEncoding
)

// AuthenticatorConfig configures NewAuthenticator.
type AuthenticatorConfig struct {
	// RPID is the relying party ID the authenticator is bound to: it hashes it
	// into its authenticator data and refuses options naming another.
	RPID string
	// Origin is the origin written into clientDataJSON; SetOrigin changes it.
	Origin string
	// UserVerifying sets the user-verified flag on every ceremony until
	// SetUserVerified changes it.
	UserVerifying bool
}

// Authenticator is a software WebAuthn authenticator and client in one: ES256
// keys, "none" attestation, discoverable (resident) credentials, CTAP2
// canonical CBOR. It reports exactly what it is told to (it never refuses a
// ceremony for want of user verification), so the relying party's own checks
// are what a test exercises. Safe for concurrent use.
//
// The signature counter is per authenticator: each ceremony reports it, then
// advances it by one; SetSignCount sets the value the next ceremony reports. A
// new authenticator starts at 0.
//
// Registering a second credential for the same user handle replaces the first,
// as CTAP2 resident credentials do. Assert signs with the newest credential the
// options admit.
type Authenticator struct {
	mu             sync.Mutex
	rpID           string
	origin         string
	userVerified   bool
	backupEligible bool
	backupState    bool
	signCount      uint32
	extensions     []byte
	credentials    []*heldCredential
}

type heldCredential struct {
	id         []byte
	userHandle []byte
	key        *ecdsa.PrivateKey
	publicKey  []byte // COSE_Key
}

// faults bends one ceremony so the suite can prove a relying party's checks.
// The zero value is a faithful ceremony.
type faults struct {
	clientDataType  string // replaces the clientDataJSON type
	rpID            string // replaces the RP ID hashed into the authenticator data
	withoutPresence bool   // clears the user-present flag
}

type credentialParameterJSON struct {
	Type string `json:"type"`
	Alg  int64  `json:"alg"`
}

type credentialDescriptorJSON struct {
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	Transports []string `json:"transports,omitempty"`
}

// creationOptionsJSON is PublicKeyCredentialCreationOptionsJSON, the members
// this package reads.
type creationOptionsJSON struct {
	RP struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"rp"`
	User struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	} `json:"user"`
	Challenge              string                     `json:"challenge"`
	PubKeyCredParams       []credentialParameterJSON  `json:"pubKeyCredParams"`
	Timeout                *int64                     `json:"timeout"`
	ExcludeCredentials     []credentialDescriptorJSON `json:"excludeCredentials"`
	AuthenticatorSelection struct {
		ResidentKey        string `json:"residentKey"`
		RequireResidentKey *bool  `json:"requireResidentKey"`
		UserVerification   string `json:"userVerification"`
	} `json:"authenticatorSelection"`
	Attestation string `json:"attestation"`
}

// requestOptionsJSON is PublicKeyCredentialRequestOptionsJSON, the members
// this package reads.
type requestOptionsJSON struct {
	Challenge        string                     `json:"challenge"`
	Timeout          *int64                     `json:"timeout"`
	RPID             string                     `json:"rpId"`
	AllowCredentials []credentialDescriptorJSON `json:"allowCredentials"`
	UserVerification string                     `json:"userVerification"`
}

type clientDataJSON struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// registrationResponseJSON is RegistrationResponseJSON, as
// PublicKeyCredential.toJSON() writes it.
type registrationResponseJSON struct {
	ID                      string                  `json:"id"`
	RawID                   string                  `json:"rawId"`
	Type                    string                  `json:"type"`
	Response                attestationResponseJSON `json:"response"`
	AuthenticatorAttachment string                  `json:"authenticatorAttachment"`
	ClientExtensionResults  map[string]any          `json:"clientExtensionResults"`
}

type attestationResponseJSON struct {
	ClientDataJSON     string   `json:"clientDataJSON"`
	AttestationObject  string   `json:"attestationObject"`
	AuthenticatorData  string   `json:"authenticatorData"`
	Transports         []string `json:"transports"`
	PublicKey          string   `json:"publicKey"`
	PublicKeyAlgorithm int64    `json:"publicKeyAlgorithm"`
}

// authenticationResponseJSON is AuthenticationResponseJSON, as
// PublicKeyCredential.toJSON() writes it.
type authenticationResponseJSON struct {
	ID                      string                `json:"id"`
	RawID                   string                `json:"rawId"`
	Type                    string                `json:"type"`
	Response                assertionResponseJSON `json:"response"`
	AuthenticatorAttachment string                `json:"authenticatorAttachment"`
	ClientExtensionResults  map[string]any        `json:"clientExtensionResults"`
}

type assertionResponseJSON struct {
	ClientDataJSON    string `json:"clientDataJSON"`
	AuthenticatorData string `json:"authenticatorData"`
	Signature         string `json:"signature"`
	UserHandle        string `json:"userHandle,omitempty"`
}

// NewAuthenticator returns an authenticator bound to cfg.RPID. It panics on an
// empty RPID or Origin.
func NewAuthenticator(cfg AuthenticatorConfig) *Authenticator {
	if cfg.RPID == "" || cfg.Origin == "" {
		panic("webauthntest: NewAuthenticator needs an RPID and an Origin")
	}
	return &Authenticator{rpID: cfg.RPID, origin: cfg.Origin, userVerified: cfg.UserVerifying}
}

// Register runs navigator.credentials.create() over the creation options JSON
// (Ceremony.Options) and returns the RegistrationResponseJSON.
func (a *Authenticator) Register(options json.RawMessage) ([]byte, error) {
	return a.register(options, faults{})
}

// Assert runs navigator.credentials.get() over the request options JSON
// (Ceremony.Options) and returns the AuthenticationResponseJSON.
func (a *Authenticator) Assert(options json.RawMessage) ([]byte, error) {
	return a.assert(options, faults{})
}

// SetUserVerified sets whether later ceremonies report user verification.
func (a *Authenticator) SetUserVerified(verified bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.userVerified = verified
}

// SetSignCount sets the counter the next ceremony reports.
func (a *Authenticator) SetSignCount(count uint32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.signCount = count
}

// SetOrigin sets the origin later ceremonies write into clientDataJSON.
func (a *Authenticator) SetOrigin(origin string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.origin = origin
}

// SetExtensionOutputs makes later ceremonies carry outputs as authenticator
// extension outputs (the extension-data flag and a CBOR map). Values may be
// bool, int, int64, uint, uint32, uint64, string, []byte or a nested
// map[string]any; another kind panics. Nil or empty clears them.
func (a *Authenticator) SetExtensionOutputs(outputs map[string]any) {
	var encoded []byte
	if len(outputs) > 0 {
		var err error
		if encoded, err = cborValue(outputs); err != nil {
			panic(err.Error())
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extensions = encoded
}

func (a *Authenticator) register(options json.RawMessage, f faults) ([]byte, error) {
	var opts creationOptionsJSON
	if err := json.Unmarshal(options, &opts); err != nil {
		return nil, fmt.Errorf("webauthntest: creation options: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if opts.RP.ID != a.rpID {
		return nil, fmt.Errorf("webauthntest: options rp.id %q, but the authenticator is bound to %q", opts.RP.ID, a.rpID)
	}
	challenge, err := decodeChallenge(opts.Challenge)
	if err != nil {
		return nil, err
	}
	handle, err := b64.DecodeString(opts.User.ID)
	if err != nil || len(handle) == 0 {
		return nil, fmt.Errorf("webauthntest: options user.id is not non-empty base64url")
	}
	if !slices.Contains(opts.PubKeyCredParams, credentialParameterJSON{Type: publicKeyType, Alg: coseAlgES256}) {
		return nil, fmt.Errorf("webauthntest: options do not offer ES256 (alg -7)")
	}
	for _, excluded := range opts.ExcludeCredentials {
		id, err := b64.DecodeString(excluded.ID)
		if err != nil {
			return nil, fmt.Errorf("webauthntest: options excludeCredentials id is not base64url")
		}
		if a.holds(id) {
			return nil, ErrCredentialExcluded
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	point, err := key.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	cred := &heldCredential{id: make([]byte, 32), userHandle: handle, key: key}
	_, _ = rand.Read(cred.id)
	cred.publicKey = cborMap(
		cborPair{cborInt(1), cborInt(2)},              // kty: EC2
		cborPair{cborInt(3), cborInt(coseAlgES256)},   // alg: ES256
		cborPair{cborInt(-1), cborInt(1)},             // crv: P-256
		cborPair{cborInt(-2), cborBytes(point[1:33])}, // x
		cborPair{cborInt(-3), cborBytes(point[33:])},  // y
	)
	a.credentials = slices.DeleteFunc(a.credentials, func(c *heldCredential) bool { return bytes.Equal(c.userHandle, handle) })
	a.credentials = append(a.credentials, cred)

	attested := slices.Concat(testAAGUID, binary.BigEndian.AppendUint16(nil, uint16(len(cred.id))), cred.id, cred.publicKey)
	authData := a.authenticatorData(f, flagAttestedData, attested)
	clientData, err := a.clientData(createType, challenge, f)
	if err != nil {
		return nil, err
	}
	attestation := cborMap(
		cborPair{cborText("fmt"), cborText("none")},
		cborPair{cborText("attStmt"), cborMap()},
		cborPair{cborText("authData"), cborBytes(authData)},
	)
	return json.Marshal(registrationResponseJSON{
		ID:    b64.EncodeToString(cred.id),
		RawID: b64.EncodeToString(cred.id),
		Type:  publicKeyType,
		Response: attestationResponseJSON{
			ClientDataJSON:     b64.EncodeToString(clientData),
			AttestationObject:  b64.EncodeToString(attestation),
			AuthenticatorData:  b64.EncodeToString(authData),
			Transports:         []string{transportInternal},
			PublicKey:          b64.EncodeToString(spki),
			PublicKeyAlgorithm: coseAlgES256,
		},
		AuthenticatorAttachment: "platform",
		ClientExtensionResults:  map[string]any{},
	})
}

func (a *Authenticator) assert(options json.RawMessage, f faults) ([]byte, error) {
	var opts requestOptionsJSON
	if err := json.Unmarshal(options, &opts); err != nil {
		return nil, fmt.Errorf("webauthntest: request options: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if opts.RPID != "" && opts.RPID != a.rpID {
		return nil, fmt.Errorf("webauthntest: options rpId %q, but the authenticator is bound to %q", opts.RPID, a.rpID)
	}
	challenge, err := decodeChallenge(opts.Challenge)
	if err != nil {
		return nil, err
	}
	cred, err := a.pick(opts.AllowCredentials)
	if err != nil {
		return nil, err
	}
	authData := a.authenticatorData(f, 0, nil)
	clientData, err := a.clientData(getType, challenge, f)
	if err != nil {
		return nil, err
	}
	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(slices.Concat(authData, clientHash[:]))
	signature, err := ecdsa.SignASN1(rand.Reader, cred.key, digest[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(authenticationResponseJSON{
		ID:    b64.EncodeToString(cred.id),
		RawID: b64.EncodeToString(cred.id),
		Type:  publicKeyType,
		Response: assertionResponseJSON{
			ClientDataJSON:    b64.EncodeToString(clientData),
			AuthenticatorData: b64.EncodeToString(authData),
			Signature:         b64.EncodeToString(signature),
			UserHandle:        b64.EncodeToString(cred.userHandle),
		},
		AuthenticatorAttachment: "platform",
		ClientExtensionResults:  map[string]any{},
	})
}

// authenticatorData builds rpIdHash, flags, the counter, then attested and the
// extension outputs, and advances the counter. The caller holds a.mu.
func (a *Authenticator) authenticatorData(f faults, flags byte, attested []byte) []byte {
	rpID := a.rpID
	if f.rpID != "" {
		rpID = f.rpID
	}
	if !f.withoutPresence {
		flags |= flagUserPresent
	}
	if a.userVerified {
		flags |= flagUserVerified
	}
	if a.backupEligible {
		flags |= flagBackupEligible
	}
	if a.backupState {
		flags |= flagBackupState
	}
	if len(a.extensions) > 0 {
		flags |= flagExtensionData
	}
	hash := sha256.Sum256([]byte(rpID))
	count := a.signCount
	a.signCount++
	return slices.Concat(hash[:], []byte{flags}, binary.BigEndian.AppendUint32(nil, count), attested, a.extensions)
}

// clientData serializes CollectedClientData in the browser's member order.
// The caller holds a.mu.
func (a *Authenticator) clientData(ceremonyType string, challenge []byte, f faults) ([]byte, error) {
	if f.clientDataType != "" {
		ceremonyType = f.clientDataType
	}
	return json.Marshal(clientDataJSON{Type: ceremonyType, Challenge: b64.EncodeToString(challenge), Origin: a.origin})
}

// holds reports whether the authenticator holds credential id. The caller
// holds a.mu.
func (a *Authenticator) holds(id []byte) bool {
	return slices.ContainsFunc(a.credentials, func(c *heldCredential) bool { return bytes.Equal(c.id, id) })
}

// pick returns the newest credential allow admits (any, when allow is empty).
// The caller holds a.mu.
func (a *Authenticator) pick(allow []credentialDescriptorJSON) (*heldCredential, error) {
	for _, cred := range slices.Backward(a.credentials) {
		if len(allow) == 0 {
			return cred, nil
		}
		for _, d := range allow {
			if id, err := b64.DecodeString(d.ID); err == nil && bytes.Equal(id, cred.id) {
				return cred, nil
			}
		}
	}
	return nil, ErrNoCredential
}

// setBackup sets the backup flags later ceremonies report.
func (a *Authenticator) setBackup(eligible, state bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.backupEligible, a.backupState = eligible, state
}

// publicKeyOf returns the COSE_Key of credential id, or nil.
func (a *Authenticator) publicKeyOf(id []byte) []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, cred := range a.credentials {
		if bytes.Equal(cred.id, id) {
			return cred.publicKey
		}
	}
	return nil
}

func decodeChallenge(s string) ([]byte, error) {
	challenge, err := b64.DecodeString(s)
	if err != nil || len(challenge) == 0 {
		return nil, fmt.Errorf("webauthntest: options challenge is not non-empty base64url")
	}
	return challenge, nil
}

package authentication

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// The six key names. Each value is the environment variable KeysInput reads
// the key from, so an error naming a KeyName names the variable to set.
const (
	// KeyJWT signs access JWTs: at least 32 bytes.
	KeyJWT KeyName = "AUTH_JWT_SECRET"
	// KeyChallengePepper keys the OTP/link challenge HMAC ring: at least 32 bytes.
	KeyChallengePepper KeyName = "AUTH_CHALLENGE_PEPPER"
	// KeyIdentifier keys the PII-free rate-limit and idempotency digests: at
	// least 32 bytes.
	KeyIdentifier KeyName = "AUTH_IDENTIFIER_KEY"
	// KeyDeliveryEncrypter seals the delivery command envelope with AES-256-GCM:
	// exactly 32 bytes.
	KeyDeliveryEncrypter KeyName = "AUTH_DELIVERY_ENCRYPTER_KEY"
	// KeyTokenEncrypter seals stored OAuth provider tokens with AES-256-GCM:
	// exactly 32 bytes. It is optional in every mode.
	KeyTokenEncrypter KeyName = "AUTH_TOKEN_ENCRYPTER_KEY"
	// KeyTOTPRing seals TOTP secrets with an AES-256-GCM key ring: AUTH_TOTP_KEYS
	// holds "id:hex,id:hex" (each exactly 32 bytes) and AUTH_TOTP_ACTIVE_KEY names
	// the id that seals. Keep a retired id in the ring until nothing is sealed
	// under it.
	KeyTOTPRing KeyName = "AUTH_TOTP_KEYS"
	// keyTOTPActive is the TOTP ring's active-id variable.
	keyTOTPActive = "AUTH_TOTP_ACTIVE_KEY"
	// ephemeralRingKeyID is the id of a development ring minted for one boot.
	ephemeralRingKeyID = "ephemeral"
)

// LegacyKeyID is the reserved ring id NewKeys gives the single-key variable
// (AUTH_JWT_SECRET, AUTH_DELIVERY_ENCRYPTER_KEY or AUTH_TOKEN_ENCRYPTER_KEY)
// when it is set beside that key's ring variables: the migration window in
// which the ring still reads what the single key wrote. A ring entry named
// legacy beside the single variable is refused.
const LegacyKeyID = "legacy"

// The ring-form variables. Each pair mirrors AUTH_TOTP_KEYS /
// AUTH_TOTP_ACTIVE_KEY: "id:hex,id:hex" and the id that signs or seals.
const (
	keyJWTKeys                    = "AUTH_JWT_KEYS"
	keyJWTActive                  = "AUTH_JWT_ACTIVE_KEY"
	keyChallengePeppers           = "AUTH_CHALLENGE_PEPPERS"
	keyChallengeActivePepper      = "AUTH_CHALLENGE_ACTIVE_PEPPER"
	keyDeliveryEncrypterKeys      = "AUTH_DELIVERY_ENCRYPTER_KEYS"
	keyDeliveryEncrypterActiveKey = "AUTH_DELIVERY_ENCRYPTER_ACTIVE_KEY"
	keyTokenEncrypterKeys         = "AUTH_TOKEN_ENCRYPTER_KEYS"
	keyTokenEncrypterActiveKey    = "AUTH_TOKEN_ENCRYPTER_ACTIVE_KEY"
)

const (
	// keyMinBytes is the floor for the HMAC and JWT keys.
	keyMinBytes = 32
	// encrypterKeyBytes is the exact AES-256-GCM key length.
	encrypterKeyBytes = 32
	// redactedKey and unsetKey are what Keys and KeysInput print instead of key
	// material.
	redactedKey = "[redacted]"
	unsetKey    = "[unset]"
)

// ringKeyIDPattern is the cryptids key-id syntax. The ring forms parse here,
// not with cryptids.ParseKeyRing, because the JWT and pepper rings take keys of
// at least 32 bytes and the legacy key joins before the active id is checked.
// ringSpec.parse mirrors ParseKeyRing and KeyRing.validate in
// sdk/pkg/cryptids/keyring.go at the pinned sdk (v0.21.0); for exact-32 rings
// TestRingParserMatchesCryptidsParseKeyRing pins that they agree.
var ringKeyIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ErrKeyRequired is returned by NewKeys for each needed key that is unset in
// production and for every key whose value is malformed, joined into one error.
// Each wrapped error names the environment variable and never its value; a
// malformed value also wraps environment.ErrSecretEncoding or
// environment.ErrSecretUnderMinimum.
var ErrKeyRequired = errors.New("authentication: a needed key is unset or malformed")

// KeyName names one of the pocket's six keys by its environment variable.
type KeyName string

// KeysInput is the hex-encoded key material NewKeys decodes. A host fills it
// with the environment package's ParseEnvTags (the tags name the variables) or
// from any other source; the pocket never reads the process environment. An
// empty field is unset. It prints redacted.
type KeysInput struct {
	JWTSecret            string `env:"AUTH_JWT_SECRET"`
	ChallengePepper      string `env:"AUTH_CHALLENGE_PEPPER"`
	IdentifierKey        string `env:"AUTH_IDENTIFIER_KEY"`
	DeliveryEncrypterKey string `env:"AUTH_DELIVERY_ENCRYPTER_KEY"`
	TokenEncrypterKey    string `env:"AUTH_TOKEN_ENCRYPTER_KEY"`
	TOTPKeys             string `env:"AUTH_TOTP_KEYS"`
	TOTPActiveKey        string `env:"AUTH_TOTP_ACTIVE_KEY"`
	// The ring forms: "id:hex,id:hex" and the active id. Each replaces its
	// single variable above; see NewKeys for how the two forms combine.
	JWTKeys                    string `env:"AUTH_JWT_KEYS"`
	JWTActiveKey               string `env:"AUTH_JWT_ACTIVE_KEY"`
	ChallengePeppers           string `env:"AUTH_CHALLENGE_PEPPERS"`
	ChallengeActivePepper      string `env:"AUTH_CHALLENGE_ACTIVE_PEPPER"`
	DeliveryEncrypterKeys      string `env:"AUTH_DELIVERY_ENCRYPTER_KEYS"`
	DeliveryEncrypterActiveKey string `env:"AUTH_DELIVERY_ENCRYPTER_ACTIVE_KEY"`
	TokenEncrypterKeys         string `env:"AUTH_TOKEN_ENCRYPTER_KEYS"`
	TokenEncrypterActiveKey    string `env:"AUTH_TOKEN_ENCRYPTER_ACTIVE_KEY"`
}

// Keys is the decoded key material NewKeys returns. A nil field is a key that
// was not needed and not set, or the token encrypter key left unset. The host
// builds each collaborator from its field: the JWT signer from
// string(JWTSecret), the challenge protector's key ring from ChallengePepper,
// the identifier keyer from IdentifierKey, AES-GCM encrypters from the two
// encrypter keys, and the TOTP encrypter from cryptids.NewAESGCMRing(TOTPRing).
// A TOTPRing with no keys was not needed and not set. Keys prints redacted
// through fmt and slog.
//
// When a key's ring variables are set its ring field is filled and its single
// field is nil: build the JWT signer from JWTRing (with JWTLegacyKeyID as the
// legacy id when set), the challenge protector from
// protection.HMACKeyRing(ChallengeRing), and the encrypters from
// DeliveryEncrypterRing / TokenEncrypterRing with their legacy ids. A ring with
// no keys is the single form (or unset). Code that checks only a single field
// (TokenEncrypterKey, say) sees nil once the ring is set and silently stops
// wiring that collaborator: branch on len(ring.Keys) > 0 first.
//
// Reading what the single key wrote needs ring adapters that know the legacy
// id: cryptids.NewAESGCMRing(ring, cryptids.WithLegacyKeyID(id)) from sdk
// v0.23.0 and golangjwt.NewRing(ring, golangjwt.WithLegacyKeyID(id)) from
// golang-jwt v0.4.0. An older NewAESGCMRing treats legacy as an ordinary key:
// it writes v1.legacy.… envelopes and cannot open single-key ones.
type Keys struct {
	JWTSecret            []byte
	ChallengePepper      []byte
	IdentifierKey        []byte
	DeliveryEncrypterKey []byte
	TokenEncrypterKey    []byte
	TOTPRing             cryptids.KeyRing

	JWTRing               cryptids.KeyRing
	ChallengeRing         cryptids.KeyRing
	DeliveryEncrypterRing cryptids.KeyRing
	TokenEncrypterRing    cryptids.KeyRing
	// JWTLegacyKeyID, DeliveryLegacyKeyID and TokenLegacyKeyID are LegacyKeyID
	// when the single variable joined that ring, else empty.
	JWTLegacyKeyID      string
	DeliveryLegacyKeyID string
	TokenLegacyKeyID    string
}

// NewKeys decodes in for the runtime mode. need names the keys the host wires;
// the constructor already refuses a missing protector, keyer or encrypter, so
// need decides only what an unset value means:
//
//   - production: each unset needed key is an ErrKeyRequired naming its
//     variable;
//   - development: each unset needed key gets an ephemeral crypto/rand key and
//     one WARN, valid for this process only;
//   - KeyTokenEncrypter is optional in both modes: unset leaves it nil, with one
//     INFO line when needed, and it is never minted;
//   - KeyTOTPRing is a ring, not one key: development mints a one-key ring
//     (id "ephemeral") with the same WARN, production refuses it unset only when
//     it is needed.
//
// A set value is decoded whether or not it is needed: hex, at least 32 bytes,
// and exactly 32 for the two encrypter keys; the TOTP ring parses with
// cryptids.ParseKeyRing (each key exactly 32 bytes, the active id present). A
// malformed value is an ErrKeyRequired in every mode. Every failure is reported together with
// errors.Join. An unknown KeyName or an invalid mode is an error.
//
// The JWT, challenge-pepper, delivery and token keys also take a ring form
// (AUTH_JWT_KEYS / AUTH_JWT_ACTIVE_KEY, AUTH_CHALLENGE_PEPPERS /
// AUTH_CHALLENGE_ACTIVE_PEPPER, AUTH_DELIVERY_ENCRYPTER_KEYS / …_ACTIVE_KEY,
// AUTH_TOKEN_ENCRYPTER_KEYS / …_ACTIVE_KEY). Per key:
//
//   - the single variable alone fills the single field, as before;
//   - the ring pair alone fills the ring field (JWT and pepper keys at least
//     32 bytes, encrypter keys exactly 32) and satisfies need, so nothing is
//     minted;
//   - both, for the JWT, delivery and token keys, fill the ring with the
//     single key joined under LegacyKeyID and set the …LegacyKeyID field. The
//     join comes before validation, so the active id may be legacy: an
//     adapter given the legacy id (sdk >= v0.23.0 WithLegacyKeyID, golang-jwt
//     >= v0.4.0 NewRing) then reads both shapes and writes the old one;
//   - both, for the pepper, is an ErrKeyRequired: challenge rows and recovery
//     codes store their key id, so the ring must name the current id itself.
func NewKeys(mode environment.Mode, in KeysInput, log *slog.Logger, need ...KeyName) (Keys, error) {
	if err := validateRuntimeMode(mode); err != nil {
		return Keys{}, err
	}
	if log == nil {
		log = slog.Default()
	}
	var keys Keys
	specs := []struct {
		name  KeyName
		value string
		dst   *[]byte
		exact bool
		ring  *ringSpec
	}{
		{KeyJWT, in.JWTSecret, &keys.JWTSecret, false, &ringSpec{keyJWTKeys, keyJWTActive, in.JWTKeys, in.JWTActiveKey, &keys.JWTRing, &keys.JWTLegacyKeyID}},
		{KeyChallengePepper, in.ChallengePepper, &keys.ChallengePepper, false, &ringSpec{keyChallengePeppers, keyChallengeActivePepper, in.ChallengePeppers, in.ChallengeActivePepper, &keys.ChallengeRing, nil}},
		{KeyIdentifier, in.IdentifierKey, &keys.IdentifierKey, false, nil},
		{KeyDeliveryEncrypter, in.DeliveryEncrypterKey, &keys.DeliveryEncrypterKey, true, &ringSpec{keyDeliveryEncrypterKeys, keyDeliveryEncrypterActiveKey, in.DeliveryEncrypterKeys, in.DeliveryEncrypterActiveKey, &keys.DeliveryEncrypterRing, &keys.DeliveryLegacyKeyID}},
		{KeyTokenEncrypter, in.TokenEncrypterKey, &keys.TokenEncrypterKey, true, &ringSpec{keyTokenEncrypterKeys, keyTokenEncrypterActiveKey, in.TokenEncrypterKeys, in.TokenEncrypterActiveKey, &keys.TokenEncrypterRing, &keys.TokenLegacyKeyID}},
	}
	needed := make(map[KeyName]bool, len(need))
	for _, name := range need {
		known := name == KeyTOTPRing
		for _, spec := range specs {
			known = known || spec.name == name
		}
		if !known {
			return Keys{}, fmt.Errorf("authentication: NewKeys: unknown key %q: %w", name, sdk.ErrInvalidInput)
		}
		needed[name] = true
	}

	var errs []error
	for _, spec := range specs {
		raw, err := decodeKey(spec.name, spec.value, spec.exact)
		if err == nil && spec.ring.set() {
			ring, legacy, err := spec.ring.parse(spec.name, raw, spec.exact)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			*spec.ring.dst = ring
			if spec.ring.legacyDst != nil {
				*spec.ring.legacyDst = legacy
			}
			continue
		}
		switch {
		case err != nil:
			errs = append(errs, err)
		case raw != nil:
			*spec.dst = raw
		case !needed[spec.name]:
		case spec.name == KeyTokenEncrypter:
			log.Info("authentication: AUTH_TOKEN_ENCRYPTER_KEY is unset; OAuth provider tokens are not persisted (login and linking still work)")
		case mode.IsProduction():
			errs = append(errs, fmt.Errorf("%w: %s is unset", ErrKeyRequired, spec.name))
		default:
			ephemeral := make([]byte, keyMinBytes)
			if _, err := rand.Read(ephemeral); err != nil {
				return Keys{}, fmt.Errorf("authentication: generate an ephemeral %s: %w", spec.name, err)
			}
			*spec.dst = ephemeral
			log.Warn("authentication: key unset; using an EPHEMERAL per-boot key — development and single-instance only, it does not survive a restart and instances cannot share it", "key", string(spec.name))
		}
	}
	ring, err := decodeTOTPRing(mode, in, needed[KeyTOTPRing], log)
	if err != nil {
		errs = append(errs, err)
	}
	keys.TOTPRing = ring
	if len(errs) > 0 {
		return Keys{}, errors.Join(errs...)
	}
	return keys, nil
}

// ringSpec is one key's ring form: its two variables, their values, where the
// parsed ring goes and, for a key whose single variable may join the ring,
// where its legacy id goes (nil for the pepper).
type ringSpec struct {
	keysVar, activeVar string
	keys, active       string
	dst                *cryptids.KeyRing
	legacyDst          *string
}

// set reports whether either ring variable is set.
func (r *ringSpec) set() bool {
	return r != nil && (r.keys != "" || r.active != "")
}

// parse builds the ring, joining single under LegacyKeyID when it is set and
// the key allows it, then validates it: at least one key, the active id
// present, every key exactly 32 bytes when exact and at least 32 otherwise.
// Errors wrap ErrKeyRequired, name the variables and a key id, never material.
func (r *ringSpec) parse(name KeyName, single []byte, exact bool) (cryptids.KeyRing, string, error) {
	vars := fmt.Sprintf("%s / %s", r.keysVar, r.activeVar)
	if single != nil && r.legacyDst == nil {
		return cryptids.KeyRing{}, "", fmt.Errorf("%w: %s and %s are both set; set one form (name the current key id inside %s)", ErrKeyRequired, name, r.keysVar, r.keysVar)
	}
	ring := cryptids.KeyRing{Active: strings.TrimSpace(r.active), Keys: map[string][]byte{}}
	for entry := range strings.SplitSeq(r.keys, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, material, ok := strings.Cut(entry, ":")
		id = strings.TrimSpace(id)
		if !ok || !ringKeyIDPattern.MatchString(id) {
			return cryptids.KeyRing{}, "", fmt.Errorf("%w: %s is malformed: entry %d is not id:hex with a valid id: %w", ErrKeyRequired, vars, len(ring.Keys)+1, cryptids.ErrKeyMalformed)
		}
		if _, dup := ring.Keys[id]; dup {
			return cryptids.KeyRing{}, "", fmt.Errorf("%w: %s is malformed: key %q appears twice: %w", ErrKeyRequired, vars, id, cryptids.ErrKeyMalformed)
		}
		raw, err := hex.DecodeString(strings.TrimSpace(material))
		if err != nil {
			return cryptids.KeyRing{}, "", fmt.Errorf("%w: %s is malformed: key %q is not valid hex: %w", ErrKeyRequired, vars, id, cryptids.ErrKeyMalformed)
		}
		ring.Keys[id] = raw
	}
	legacy := ""
	if single != nil {
		if _, taken := ring.Keys[LegacyKeyID]; taken {
			return cryptids.KeyRing{}, "", fmt.Errorf("%w: %s names the reserved id %q while %s is set; rename the entry or unset %s", ErrKeyRequired, r.keysVar, LegacyKeyID, name, name)
		}
		ring.Keys[LegacyKeyID] = single
		legacy = LegacyKeyID
	}
	if len(ring.Keys) == 0 {
		return cryptids.KeyRing{}, "", fmt.Errorf("%w: %s is malformed: %w", ErrKeyRequired, vars, cryptids.ErrKeyRingEmpty)
	}
	for id, raw := range ring.Keys {
		switch {
		case exact && len(raw) != encrypterKeyBytes:
			return cryptids.KeyRing{}, "", fmt.Errorf("%w: %s is malformed: key %q must be exactly %d bytes: %w", ErrKeyRequired, vars, id, encrypterKeyBytes, cryptids.ErrKeyMalformed)
		case !exact && len(raw) < keyMinBytes:
			return cryptids.KeyRing{}, "", fmt.Errorf("%w: %s is malformed: key %q must be at least %d bytes: %w", ErrKeyRequired, vars, id, keyMinBytes, cryptids.ErrKeyMalformed)
		}
	}
	if _, ok := ring.Keys[ring.Active]; !ok {
		return cryptids.KeyRing{}, "", fmt.Errorf("%w: %s is malformed: active key %q is not in the ring: %w", ErrKeyRequired, vars, ring.Active, cryptids.ErrKeyRingActiveMissing)
	}
	return ring, legacy, nil
}

// decodeTOTPRing parses the TOTP key ring, mints a development one when it is
// needed and unset, and refuses a needed unset ring in production.
func decodeTOTPRing(mode environment.Mode, in KeysInput, needed bool, log *slog.Logger) (cryptids.KeyRing, error) {
	if in.TOTPKeys != "" || in.TOTPActiveKey != "" {
		ring, err := cryptids.ParseKeyRing(in.TOTPKeys, in.TOTPActiveKey)
		if err != nil {
			return cryptids.KeyRing{}, fmt.Errorf("%w: %s / %s is malformed: %w", ErrKeyRequired, KeyTOTPRing, keyTOTPActive, err)
		}
		return ring, nil
	}
	switch {
	case !needed:
		return cryptids.KeyRing{}, nil
	case mode.IsProduction():
		return cryptids.KeyRing{}, fmt.Errorf("%w: %s is unset", ErrKeyRequired, KeyTOTPRing)
	}
	ephemeral := make([]byte, encrypterKeyBytes)
	if _, err := rand.Read(ephemeral); err != nil {
		return cryptids.KeyRing{}, fmt.Errorf("authentication: generate an ephemeral %s: %w", KeyTOTPRing, err)
	}
	log.Warn("authentication: key unset; using an EPHEMERAL per-boot key — development and single-instance only, it does not survive a restart and instances cannot share it", "key", string(KeyTOTPRing))
	return cryptids.KeyRing{Active: ephemeralRingKeyID, Keys: map[string][]byte{ephemeralRingKeyID: ephemeral}}, nil
}

// decodeKey decodes one hex value. Empty returns nil, nil. The decoder's own
// error text names the offending byte, so only its sentinel is wrapped.
func decodeKey(name KeyName, value string, exact bool) ([]byte, error) {
	want := fmt.Sprintf("at least %d bytes", keyMinBytes)
	if exact {
		want = fmt.Sprintf("exactly %d bytes", encrypterKeyBytes)
	}
	raw, err := environment.DecodeSecret(value, keyMinBytes)
	switch {
	case errors.Is(err, environment.ErrSecretEncoding):
		return nil, fmt.Errorf("%w: %s is not valid hex: %w", ErrKeyRequired, name, environment.ErrSecretEncoding)
	case errors.Is(err, environment.ErrSecretUnderMinimum):
		return nil, fmt.Errorf("%w: %s must decode to %s: %w", ErrKeyRequired, name, want, environment.ErrSecretUnderMinimum)
	case err != nil:
		return nil, fmt.Errorf("%w: %s could not be decoded", ErrKeyRequired, name)
	case exact && raw != nil && len(raw) != encrypterKeyBytes:
		return nil, fmt.Errorf("%w: %s must decode to %s", ErrKeyRequired, name, want)
	}
	return raw, nil
}

// String reports which keys are present, never their material.
func (k Keys) String() string {
	return renderKeys("authentication.Keys", k.fields())
}

// GoString keeps %#v redacted.
func (k Keys) GoString() string { return k.String() }

// LogValue keeps slog output redacted.
func (k Keys) LogValue() slog.Value { return keysLogValue(k.fields()) }

func (k Keys) fields() []keyField {
	return []keyField{
		{"JWTSecret", k.JWTSecret != nil},
		{"ChallengePepper", k.ChallengePepper != nil},
		{"IdentifierKey", k.IdentifierKey != nil},
		{"DeliveryEncrypterKey", k.DeliveryEncrypterKey != nil},
		{"TokenEncrypterKey", k.TokenEncrypterKey != nil},
		{"TOTPRing", len(k.TOTPRing.Keys) > 0},
		{"JWTRing", len(k.JWTRing.Keys) > 0},
		{"ChallengeRing", len(k.ChallengeRing.Keys) > 0},
		{"DeliveryEncrypterRing", len(k.DeliveryEncrypterRing.Keys) > 0},
		{"TokenEncrypterRing", len(k.TokenEncrypterRing.Keys) > 0},
	}
}

// String reports which values are present, never the values.
func (in KeysInput) String() string {
	return renderKeys("authentication.KeysInput", in.fields())
}

// GoString keeps %#v redacted.
func (in KeysInput) GoString() string { return in.String() }

// LogValue keeps slog output redacted.
func (in KeysInput) LogValue() slog.Value { return keysLogValue(in.fields()) }

func (in KeysInput) fields() []keyField {
	return []keyField{
		{"JWTSecret", in.JWTSecret != ""},
		{"ChallengePepper", in.ChallengePepper != ""},
		{"IdentifierKey", in.IdentifierKey != ""},
		{"DeliveryEncrypterKey", in.DeliveryEncrypterKey != ""},
		{"TokenEncrypterKey", in.TokenEncrypterKey != ""},
		{"TOTPKeys", in.TOTPKeys != ""},
		{"JWTKeys", in.JWTKeys != ""},
		{"ChallengePeppers", in.ChallengePeppers != ""},
		{"DeliveryEncrypterKeys", in.DeliveryEncrypterKeys != ""},
		{"TokenEncrypterKeys", in.TokenEncrypterKeys != ""},
	}
}

// keyField is one key's name and presence, the only facts the redacted
// renderings expose.
type keyField struct {
	name string
	set  bool
}

func (f keyField) marker() string {
	if f.set {
		return redactedKey
	}
	return unsetKey
}

func renderKeys(typeName string, fields []keyField) string {
	var b strings.Builder
	b.WriteString(typeName)
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(f.name)
		b.WriteByte(':')
		b.WriteString(f.marker())
	}
	b.WriteByte('}')
	return b.String()
}

func keysLogValue(fields []keyField) slog.Value {
	attrs := make([]slog.Attr, 0, len(fields))
	for _, f := range fields {
		attrs = append(attrs, slog.String(f.name, f.marker()))
	}
	return slog.GroupValue(attrs...)
}

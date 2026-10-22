package authentication

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// The five key names. Each value is the environment variable KeysInput reads
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

// ErrKeyRequired is returned by NewKeys for each needed key that is unset in
// production and for every key whose value is malformed, joined into one error.
// Each wrapped error names the environment variable and never its value; a
// malformed value also wraps environment.ErrSecretEncoding or
// environment.ErrSecretUnderMinimum.
var ErrKeyRequired = errors.New("authentication: a needed key is unset or malformed")

// KeyName names one of the pocket's five keys by its environment variable.
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
}

// Keys is the decoded key material NewKeys returns. A nil field is a key that
// was not needed and not set, or the token encrypter key left unset. The host
// builds each collaborator from its field: the JWT signer from
// string(JWTSecret), the challenge protector's key ring from ChallengePepper,
// the identifier keyer from IdentifierKey, and AES-GCM encrypters from the two
// encrypter keys. Keys prints redacted through fmt and slog.
type Keys struct {
	JWTSecret            []byte
	ChallengePepper      []byte
	IdentifierKey        []byte
	DeliveryEncrypterKey []byte
	TokenEncrypterKey    []byte
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
//     INFO line when needed, and it is never minted.
//
// A set value is decoded whether or not it is needed: hex, at least 32 bytes,
// and exactly 32 for the two encrypter keys. A malformed value is an
// ErrKeyRequired in every mode. Every failure is reported together with
// errors.Join. An unknown KeyName or an invalid mode is an error.
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
	}{
		{KeyJWT, in.JWTSecret, &keys.JWTSecret, false},
		{KeyChallengePepper, in.ChallengePepper, &keys.ChallengePepper, false},
		{KeyIdentifier, in.IdentifierKey, &keys.IdentifierKey, false},
		{KeyDeliveryEncrypter, in.DeliveryEncrypterKey, &keys.DeliveryEncrypterKey, true},
		{KeyTokenEncrypter, in.TokenEncrypterKey, &keys.TokenEncrypterKey, true},
	}
	needed := make(map[KeyName]bool, len(need))
	for _, name := range need {
		known := false
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
	if len(errs) > 0 {
		return Keys{}, errors.Join(errs...)
	}
	return keys, nil
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

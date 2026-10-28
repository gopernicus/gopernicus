package authentication

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// The pepper ring reaches the challenge protector by plain conversion (BE-8):
// this stops compiling if the two types ever diverge.
var _ = protection.HMACKeyRing(cryptids.KeyRing{})

// ringRole is one key with a ring form, as the tests drive it.
type ringRole struct {
	name     KeyName
	exact    bool
	legacy   bool
	set      func(in *KeysInput, keys, active string)
	single   func(in *KeysInput, value string)
	ring     func(k Keys) cryptids.KeyRing
	field    func(k Keys) []byte
	legacyID func(k Keys) string
}

var ringRoles = []ringRole{
	{KeyJWT, false, true,
		func(in *KeysInput, k, a string) { in.JWTKeys, in.JWTActiveKey = k, a },
		func(in *KeysInput, v string) { in.JWTSecret = v },
		func(k Keys) cryptids.KeyRing { return k.JWTRing }, func(k Keys) []byte { return k.JWTSecret },
		func(k Keys) string { return k.JWTLegacyKeyID }},
	{KeyChallengePepper, false, false,
		func(in *KeysInput, k, a string) { in.ChallengePeppers, in.ChallengeActivePepper = k, a },
		func(in *KeysInput, v string) { in.ChallengePepper = v },
		func(k Keys) cryptids.KeyRing { return k.ChallengeRing }, func(k Keys) []byte { return k.ChallengePepper },
		func(Keys) string { return "" }},
	{KeyDeliveryEncrypter, true, true,
		func(in *KeysInput, k, a string) { in.DeliveryEncrypterKeys, in.DeliveryEncrypterActiveKey = k, a },
		func(in *KeysInput, v string) { in.DeliveryEncrypterKey = v },
		func(k Keys) cryptids.KeyRing { return k.DeliveryEncrypterRing }, func(k Keys) []byte { return k.DeliveryEncrypterKey },
		func(k Keys) string { return k.DeliveryLegacyKeyID }},
	{KeyTokenEncrypter, true, true,
		func(in *KeysInput, k, a string) { in.TokenEncrypterKeys, in.TokenEncrypterActiveKey = k, a },
		func(in *KeysInput, v string) { in.TokenEncrypterKey = v },
		func(k Keys) cryptids.KeyRing { return k.TokenEncrypterRing }, func(k Keys) []byte { return k.TokenEncrypterKey },
		func(k Keys) string { return k.TokenLegacyKeyID }},
}

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestNewKeysRingFormAlone proves each ring pair alone fills the ring field,
// leaves the single field nil and sets no legacy id.
func TestNewKeysRingFormAlone(t *testing.T) {
	for _, role := range ringRoles {
		t.Run(string(role.name), func(t *testing.T) {
			var in KeysInput
			role.set(&in, "k1:"+hexKey(0x11, 32)+", k2:"+hexKey(0x12, 32), "k2")
			keys, err := NewKeys(environment.ModeProduction, in, discardLog(), role.name)
			if err != nil {
				t.Fatal(err)
			}
			ring := role.ring(keys)
			if ring.Active != "k2" || len(ring.Keys) != 2 || ring.Keys["k1"][0] != 0x11 {
				t.Fatalf("ring = active %q, %d keys", ring.Active, len(ring.Keys))
			}
			if role.field(keys) != nil || role.legacyID(keys) != "" {
				t.Fatalf("single field %v, legacy id %q", role.field(keys), role.legacyID(keys))
			}
		})
	}
}

// TestNewKeysRingKeyLength proves BE-1: the JWT and pepper rings take keys of
// at least 32 bytes (a 64-byte secret can join), the encrypter rings exactly
// 32.
func TestNewKeysRingKeyLength(t *testing.T) {
	for _, role := range ringRoles {
		for _, n := range []int{31, 32, 33, 64} {
			var in KeysInput
			role.set(&in, "k1:"+hexKey(0x21, n), "k1")
			_, err := NewKeys(environment.ModeDevelopment, in, discardLog())
			want := n == 32 || (!role.exact && n > 32)
			if (err == nil) != want {
				t.Errorf("%s with a %d-byte key: err = %v, want accepted=%v", role.name, n, err, want)
			}
			if err != nil && (!errors.Is(err, ErrKeyRequired) || !errors.Is(err, cryptids.ErrKeyMalformed)) {
				t.Errorf("%s with a %d-byte key: err = %v", role.name, n, err)
			}
		}
	}
}

// TestNewKeysBothFormsJoinLegacy proves the migration window for the JWT,
// delivery and token keys: the single key joins the ring as legacy, the legacy
// id is set, and legacy may be the active id (phase a).
func TestNewKeysBothFormsJoinLegacy(t *testing.T) {
	for _, role := range ringRoles {
		if !role.legacy {
			continue
		}
		for _, active := range []string{"k1", LegacyKeyID} {
			t.Run(string(role.name)+"/active="+active, func(t *testing.T) {
				var in KeysInput
				role.single(&in, hexKey(0x31, 32))
				role.set(&in, "k1:"+hexKey(0x32, 32), active)
				keys, err := NewKeys(environment.ModeProduction, in, discardLog(), role.name)
				if err != nil {
					t.Fatal(err)
				}
				ring := role.ring(keys)
				if ring.Active != active || len(ring.Keys) != 2 || ring.Keys[LegacyKeyID][0] != 0x31 || ring.Keys["k1"][0] != 0x32 {
					t.Fatalf("ring = active %q, %d keys", ring.Active, len(ring.Keys))
				}
				if role.legacyID(keys) != LegacyKeyID || role.field(keys) != nil {
					t.Fatalf("legacy id %q, single field %v", role.legacyID(keys), role.field(keys))
				}
				if role.exact {
					if _, err := cryptids.NewAESGCMRing(ring); err != nil {
						t.Fatalf("NewAESGCMRing: %v", err)
					}
				}
			})
		}
	}
	// A 64-byte JWT secret joins a JWT ring.
	keys, err := NewKeys(environment.ModeProduction, KeysInput{JWTSecret: hexKey(0x33, 64), JWTKeys: "k1:" + hexKey(0x34, 32), JWTActiveKey: "k1"}, discardLog(), KeyJWT)
	if err != nil || len(keys.JWTRing.Keys[LegacyKeyID]) != 64 {
		t.Fatalf("64-byte legacy JWT secret: %v", err)
	}
}

// TestNewKeysRingRefusals covers the refused combinations: a ring entry named
// legacy beside the single variable, the pepper in both forms, and every
// malformed ring. Each names the variables and never echoes material.
func TestNewKeysRingRefusals(t *testing.T) {
	material := hexKey(0x41, 32)
	type refusal struct {
		name  string
		role  ringRole
		in    func(role ringRole) KeysInput
		names []string
	}
	var cases []refusal
	for _, role := range ringRoles {
		keysVar := map[KeyName]string{KeyJWT: keyJWTKeys, KeyChallengePepper: keyChallengePeppers, KeyDeliveryEncrypter: keyDeliveryEncrypterKeys, KeyTokenEncrypter: keyTokenEncrypterKeys}[role.name]
		if role.legacy {
			cases = append(cases, refusal{"legacy entry beside single", role, func(r ringRole) KeysInput {
				var in KeysInput
				r.single(&in, material)
				r.set(&in, "legacy:"+material, "legacy")
				return in
			}, []string{keysVar, string(role.name), `"legacy"`}})
		} else {
			cases = append(cases, refusal{"both forms", role, func(r ringRole) KeysInput {
				var in KeysInput
				r.single(&in, material)
				r.set(&in, "dev:"+material, "dev")
				return in
			}, []string{keysVar, string(role.name)}})
		}
		for _, bad := range []struct{ name, keys, active string }{
			{"active missing", "k1:" + material, "k2"},
			{"active unset", "k1:" + material, ""},
			{"keys unset", "", "k1"},
			{"bad hex", "k1:zz" + material[2:], "k1"},
			{"bad id", "K1:" + material, "K1"},
			{"no colon", material, "k1"},
			{"duplicate id", "k1:" + material + ",k1:" + material, "k1"},
		} {
			cases = append(cases, refusal{bad.name, role, func(r ringRole) KeysInput {
				var in KeysInput
				r.set(&in, bad.keys, bad.active)
				return in
			}, []string{keysVar}})
		}
	}
	for _, tc := range cases {
		for _, mode := range []environment.Mode{environment.ModeDevelopment, environment.ModeProduction} {
			_, err := NewKeys(mode, tc.in(tc.role), discardLog(), tc.role.name)
			if !errors.Is(err, ErrKeyRequired) {
				t.Errorf("%s/%s/%s: err = %v, want ErrKeyRequired", tc.role.name, tc.name, mode, err)
				continue
			}
			for _, name := range tc.names {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("%s/%s: err does not name %s: %v", tc.role.name, tc.name, name, err)
				}
			}
			if strings.Contains(err.Error(), material[4:]) || strings.Contains(err.Error(), "invalid byte") {
				t.Errorf("%s/%s: err echoes material or the decoder: %v", tc.role.name, tc.name, err)
			}
		}
	}
}

// TestNewKeysRingEntryNamedLegacyAlone proves legacy is only reserved beside
// the single variable: a ring alone may use the id, and no legacy id is set.
func TestNewKeysRingEntryNamedLegacyAlone(t *testing.T) {
	keys, err := NewKeys(environment.ModeProduction, KeysInput{JWTKeys: "legacy:" + hexKey(0x51, 32), JWTActiveKey: "legacy"}, discardLog(), KeyJWT)
	if err != nil || keys.JWTLegacyKeyID != "" || keys.JWTRing.Active != LegacyKeyID {
		t.Fatalf("keys = %v, legacy %q, err %v", keys.JWTRing.Active, keys.JWTLegacyKeyID, err)
	}
}

// TestNewKeysRingSatisfiesNeed is BE-4: a set ring satisfies need, so
// production does not refuse the unset single variable and development mints
// no ephemeral key (and logs no token-encrypter INFO) beside it.
func TestNewKeysRingSatisfiesNeed(t *testing.T) {
	var in KeysInput
	for _, role := range ringRoles {
		role.set(&in, "r1:"+hexKey(0x61, 32), "r1")
	}
	need := []KeyName{KeyJWT, KeyChallengePepper, KeyDeliveryEncrypter, KeyTokenEncrypter}
	if _, err := NewKeys(environment.ModeProduction, in, discardLog(), need...); err != nil {
		t.Fatalf("production with every ring set: %v", err)
	}
	var buf bytes.Buffer
	keys, err := NewKeys(environment.ModeDevelopment, in, slog.New(slog.NewTextHandler(&buf, nil)), need...)
	if err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("development logged beside set rings:\n%s", buf.String())
	}
	for _, role := range ringRoles {
		if role.field(keys) != nil || role.legacyID(keys) != "" {
			t.Errorf("%s: an ephemeral single key or a legacy id appeared beside the ring", role.name)
		}
		if _, ok := role.ring(keys).Keys[LegacyKeyID]; ok {
			t.Errorf("%s: a legacy key was minted into the ring", role.name)
		}
	}
	if keys.ChallengeRing.Active != "r1" {
		t.Fatal("pepper ring not parsed")
	}
	if _, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing(keys.ChallengeRing)); err != nil {
		t.Fatalf("protector from the converted pepper ring: %v", err)
	}
}

// TestKeysInputRingFormsParseFromEnvironment proves the eight ring variables
// fill KeysInput through ParseEnvTags.
func TestKeysInputRingFormsParseFromEnvironment(t *testing.T) {
	values := map[string]string{
		keyJWTKeys: "k1:" + hexKey(0x71, 32), keyJWTActive: "k1",
		keyChallengePeppers: "dev:" + hexKey(0x72, 32), keyChallengeActivePepper: "dev",
		keyDeliveryEncrypterKeys: "d1:" + hexKey(0x73, 32), keyDeliveryEncrypterActiveKey: "d1",
		keyTokenEncrypterKeys: "t1:" + hexKey(0x74, 32), keyTokenEncrypterActiveKey: "t1",
	}
	for k, v := range values {
		t.Setenv(k, v)
	}
	var in KeysInput
	if err := environment.ParseEnvTags("", &in); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{
		keyJWTKeys: in.JWTKeys, keyJWTActive: in.JWTActiveKey,
		keyChallengePeppers: in.ChallengePeppers, keyChallengeActivePepper: in.ChallengeActivePepper,
		keyDeliveryEncrypterKeys: in.DeliveryEncrypterKeys, keyDeliveryEncrypterActiveKey: in.DeliveryEncrypterActiveKey,
		keyTokenEncrypterKeys: in.TokenEncrypterKeys, keyTokenEncrypterActiveKey: in.TokenEncrypterActiveKey,
	}
	for k, v := range values {
		if got[k] != v {
			t.Errorf("%s not parsed", k)
		}
	}
}

// TestKeysRingFieldsPrintRedacted proves the ring fields print as presence
// markers only.
func TestKeysRingFieldsPrintRedacted(t *testing.T) {
	var in KeysInput
	for _, role := range ringRoles {
		role.set(&in, "r1:"+hexKey(0x81, 32), "r1")
	}
	keys, err := NewKeys(environment.ModeProduction, in, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("keys", "keys", keys, "input", in)
	for _, out := range []string{fmt.Sprintf("%v %+v %#v", keys, keys, keys), fmt.Sprintf("%v %+v %#v", in, in, in), logged.String()} {
		if strings.Contains(out, hexKey(0x81, 4)) || strings.Contains(out, "[129 129") {
			t.Errorf("output leaks ring material: %s", out)
		}
	}
	for _, field := range []string{"JWTRing:[redacted]", "ChallengeRing:[redacted]", "DeliveryEncrypterRing:[redacted]", "TokenEncrypterRing:[redacted]"} {
		if !strings.Contains(fmt.Sprint(keys), field) {
			t.Errorf("Keys.String() lacks %s: %s", field, keys)
		}
	}
	for _, field := range []string{"JWTKeys:[redacted]", "ChallengePeppers:[redacted]", "DeliveryEncrypterKeys:[redacted]", "TokenEncrypterKeys:[redacted]"} {
		if !strings.Contains(fmt.Sprint(in), field) {
			t.Errorf("KeysInput.String() lacks %s: %s", field, in)
		}
	}
}

// TestRingParserMatchesCryptidsParseKeyRing is the drift test for the local
// ring parser: for exact-32 rings with no legacy join it accepts and rejects
// exactly what cryptids.ParseKeyRing does at the pinned sdk, and both build
// the same ring.
func TestRingParserMatchesCryptidsParseKeyRing(t *testing.T) {
	a, b := hexKey(0x91, 32), hexKey(0x92, 32)
	cases := []struct{ keys, active string }{
		{"k1:" + a, "k1"},
		{"k1:" + a + ",k2:" + b, "k2"},
		{" k1 : " + a + " , ,k2:" + b + ",", " k1 "},
		{"k1:" + a, "k2"},
		{"k1:" + a, ""},
		{"", "k1"},
		{",,", "k1"},
		{"k1:" + a + ",k1:" + b, "k1"},
		{"K1:" + a, "K1"},
		{"-k:" + a, "-k"},
		{strings.Repeat("a", 32) + ":" + a, strings.Repeat("a", 32)},
		{strings.Repeat("a", 33) + ":" + a, strings.Repeat("a", 33)},
		{"k1:" + hexKey(0x91, 31), "k1"},
		{"k1:" + hexKey(0x91, 33), "k1"},
		{"k1:" + a[:63], "k1"},
		{"k1:zz" + a[2:], "k1"},
		{"k1", "k1"},
		{"k1:", "k1"},
		{"legacy:" + a, "legacy"},
		{"k_1-x:" + a, "k_1-x"},
	}
	for _, tc := range cases {
		spec := &ringSpec{keysVar: "K", activeVar: "A", keys: tc.keys, active: tc.active}
		local, _, localErr := spec.parse("SINGLE", nil, true)
		sdkRing, sdkErr := cryptids.ParseKeyRing(tc.keys, tc.active)
		if (localErr == nil) != (sdkErr == nil) {
			t.Errorf("keys %q active %q: local err %v, cryptids err %v", tc.keys, tc.active, localErr, sdkErr)
			continue
		}
		if localErr != nil {
			continue
		}
		if local.Active != sdkRing.Active || len(local.Keys) != len(sdkRing.Keys) {
			t.Errorf("keys %q: local ring %q/%d, cryptids %q/%d", tc.keys, local.Active, len(local.Keys), sdkRing.Active, len(sdkRing.Keys))
		}
		for id, raw := range sdkRing.Keys {
			if !bytes.Equal(local.Keys[id], raw) {
				t.Errorf("keys %q: key %q differs", tc.keys, id)
			}
		}
	}
}

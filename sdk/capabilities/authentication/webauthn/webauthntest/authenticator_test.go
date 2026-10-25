package webauthntest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"
)

func testCreationOptions(t *testing.T, edit func(map[string]any)) json.RawMessage {
	t.Helper()
	opts := map[string]any{
		"rp":               map[string]any{"id": suiteRPID, "name": suiteRPName},
		"user":             map[string]any{"id": b64.EncodeToString([]byte("handle-1")), "name": "u", "displayName": "U"},
		"challenge":        b64.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		"pubKeyCredParams": []any{map[string]any{"type": "public-key", "alg": -7}},
	}
	if edit != nil {
		edit(opts)
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testRequestOptions(t *testing.T, allow ...[]byte) json.RawMessage {
	t.Helper()
	opts := map[string]any{"challenge": b64.EncodeToString(bytes.Repeat([]byte{9}, 32)), "rpId": suiteRPID}
	var descriptors []any
	for _, id := range allow {
		descriptors = append(descriptors, map[string]any{"type": "public-key", "id": b64.EncodeToString(id)})
	}
	opts["allowCredentials"] = descriptors
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func registrationAuthData(t *testing.T, response []byte) []byte {
	t.Helper()
	var r registrationResponseJSON
	if err := json.Unmarshal(response, &r); err != nil {
		t.Fatal(err)
	}
	raw, err := b64.DecodeString(r.Response.AuthenticatorData)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertionAuthData(t *testing.T, response []byte) (authData []byte, id string) {
	t.Helper()
	var r authenticationResponseJSON
	if err := json.Unmarshal(response, &r); err != nil {
		t.Fatal(err)
	}
	raw, err := b64.DecodeString(r.Response.AuthenticatorData)
	if err != nil {
		t.Fatal(err)
	}
	return raw, r.RawID
}

func TestNewAuthenticatorPanics(t *testing.T) {
	for name, cfg := range map[string]AuthenticatorConfig{"no RPID": {Origin: suiteOrigin}, "no Origin": {RPID: suiteRPID}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("NewAuthenticator(%s) did not panic", name)
				}
			}()
			NewAuthenticator(cfg)
		}()
	}
}

func TestAuthenticatorRegistrationLayout(t *testing.T) {
	a := newAuthenticator(true)
	a.SetSignCount(0x01020304)
	a.setBackup(true, true)
	a.SetExtensionOutputs(map[string]any{"credProtect": uint64(2)})
	response, err := a.Register(testCreationOptions(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	data := registrationAuthData(t, response)
	hash := sha256.Sum256([]byte(suiteRPID))
	if !bytes.Equal(data[:32], hash[:]) {
		t.Error("rpIdHash is not SHA-256(RPID)")
	}
	if want := flagUserPresent | flagUserVerified | flagBackupEligible | flagBackupState | flagAttestedData | flagExtensionData; data[32] != want {
		t.Errorf("flags = %08b, want %08b", data[32], want)
	}
	if got := binary.BigEndian.Uint32(data[33:37]); got != 0x01020304 {
		t.Errorf("counter = %#x, want the big-endian 0x01020304", got)
	}
	if !bytes.Equal(data[37:53], testAAGUID) {
		t.Errorf("AAGUID = %x, want %x", data[37:53], testAAGUID)
	}
	idLength := int(binary.BigEndian.Uint16(data[53:55]))
	id := data[55 : 55+idLength]
	key := a.publicKeyOf(id)
	if idLength != 32 || len(key) != 77 {
		t.Fatalf("credential id %d bytes, COSE_Key %d bytes; want 32 and 77", idLength, len(key))
	}
	if !bytes.Equal(data[55+idLength:55+idLength+77], key) {
		t.Error("the attested credential data does not end in the credential's COSE_Key")
	}
	if want := []byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20}; !bytes.Equal(key[:10], want) {
		t.Errorf("COSE_Key head = %x, want %x", key[:10], want)
	}
	if got := data[55+idLength+77:]; !bytes.Equal(got, append([]byte{0xa1}, append(cborText("credProtect"), 0x02)...)) {
		t.Errorf("extension outputs = %x", got)
	}
}

func TestAuthenticatorCounterAdvancesPerCeremony(t *testing.T) {
	a := newAuthenticator(false)
	response, err := a.Register(testCreationOptions(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	data := registrationAuthData(t, response)
	if got := binary.BigEndian.Uint32(data[33:37]); got != 0 || data[32] != flagUserPresent|flagAttestedData {
		t.Errorf("registration counter %d flags %08b, want 0 and UP|AT", got, data[32])
	}
	for _, want := range []uint32{1, 2} {
		response, err := a.Assert(testRequestOptions(t))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := assertionAuthData(t, response)
		if got := binary.BigEndian.Uint32(data[33:37]); got != want || len(data) != 37 {
			t.Errorf("assertion counter %d (%d bytes), want %d (37 bytes)", got, len(data), want)
		}
	}
	a.SetSignCount(5)
	response, err = a.Assert(testRequestOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := assertionAuthData(t, response); binary.BigEndian.Uint32(data[33:37]) != 5 {
		t.Error("SetSignCount(5) did not set the next ceremony's counter")
	}
}

func TestAuthenticatorRefusals(t *testing.T) {
	a := newAuthenticator(true)
	if _, err := a.Assert(testRequestOptions(t)); !errors.Is(err, ErrNoCredential) {
		t.Errorf("Assert with no credential = %v, want ErrNoCredential", err)
	}
	for name, options := range map[string]json.RawMessage{
		"another rp.id": testCreationOptions(t, func(o map[string]any) { o["rp"] = map[string]any{"id": foreignRPID} }),
		"no ES256": testCreationOptions(t, func(o map[string]any) {
			o["pubKeyCredParams"] = []any{map[string]any{"type": "public-key", "alg": -257}}
		}),
		"padded challenge": testCreationOptions(t, func(o map[string]any) { o["challenge"] = "AAAA==" }),
		"empty user id":    testCreationOptions(t, func(o map[string]any) { o["user"] = map[string]any{"id": ""} }),
		"not JSON":         json.RawMessage("nope"),
	} {
		if _, err := a.Register(options); err == nil {
			t.Errorf("Register(%s) = nil error, want a refusal", name)
		}
	}
	response, err := a.Register(testCreationOptions(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := b64.DecodeString(registrationRawID(t, response))
	excluding := testCreationOptions(t, func(o map[string]any) {
		o["excludeCredentials"] = []any{map[string]any{"type": "public-key", "id": b64.EncodeToString(first)}}
	})
	if _, err := a.Register(excluding); !errors.Is(err, ErrCredentialExcluded) {
		t.Errorf("Register(excluding a held credential) = %v, want ErrCredentialExcluded", err)
	}
	if _, err := a.Assert(testRequestOptions(t, []byte("unknown"))); !errors.Is(err, ErrNoCredential) {
		t.Errorf("Assert(allowCredentials naming no held credential) = %v, want ErrNoCredential", err)
	}
	foreign, _ := json.Marshal(map[string]any{"challenge": b64.EncodeToString([]byte("challenge")), "rpId": foreignRPID})
	if _, err := a.Assert(foreign); err == nil {
		t.Error("Assert(another rpId) = nil error, want a refusal")
	}
	defer func() {
		if recover() == nil {
			t.Error("SetExtensionOutputs(a float) did not panic")
		}
	}()
	a.SetExtensionOutputs(map[string]any{"f": 1.5})
}

func TestAuthenticatorCredentialSelection(t *testing.T) {
	a := newAuthenticator(true)
	register := func(handle string) []byte {
		t.Helper()
		response, err := a.Register(testCreationOptions(t, func(o map[string]any) {
			o["user"] = map[string]any{"id": b64.EncodeToString([]byte(handle)), "name": handle}
		}))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := b64.DecodeString(registrationRawID(t, response))
		return id
	}
	alice, bob := register("alice"), register("bob")
	pick := func(allow ...[]byte) string {
		t.Helper()
		response, err := a.Assert(testRequestOptions(t, allow...))
		if err != nil {
			t.Fatal(err)
		}
		_, id := assertionAuthData(t, response)
		return id
	}
	if got := pick(); got != b64.EncodeToString(bob) {
		t.Error("a discoverable assertion did not sign with the newest credential")
	}
	if got := pick(alice); got != b64.EncodeToString(alice) {
		t.Error("allowCredentials did not select the named credential")
	}
	again := register("alice")
	if a.publicKeyOf(alice) != nil || a.publicKeyOf(again) == nil {
		t.Error("re-registering a user handle did not replace its credential")
	}
}

func registrationRawID(t *testing.T, response []byte) string {
	t.Helper()
	var r registrationResponseJSON
	if err := json.Unmarshal(response, &r); err != nil {
		t.Fatal(err)
	}
	return r.RawID
}

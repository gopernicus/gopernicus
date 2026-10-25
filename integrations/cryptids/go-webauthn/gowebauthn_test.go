package gowebauthn_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"

	gowebauthn "github.com/gopernicus/gopernicus/integrations/cryptids/go-webauthn"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn/webauthntest"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	testRPID   = "example.com"
	testOrigin = "https://example.com"
)

func TestNewRefusesSubMillisecondTimeout(t *testing.T) {
	cfg := testConfig(t)
	cfg.Timeout = 500 * time.Microsecond
	rp, err := gowebauthn.New(cfg)
	if !errors.Is(err, sdk.ErrInvalidInput) || rp != nil {
		t.Fatalf("New(Timeout 500µs) = %v, %v; want nil, sdk.ErrInvalidInput (go-webauthn would replace it with its own default)", rp, err)
	}
	cfg.Timeout = time.Millisecond
	if _, err := gowebauthn.New(cfg); err != nil {
		t.Fatalf("New(Timeout 1ms) = %v, want nil", err)
	}
}

func TestStateNamesCeremonyKind(t *testing.T) {
	rp := newRelyingParty(t)
	ctx := context.Background()
	a := newAuthenticator()
	user := newUser()

	registration, err := rp.BeginRegistration(ctx, webauthn.RegistrationStart{User: user})
	if err != nil {
		t.Fatalf("BeginRegistration = %v", err)
	}
	if got := stateKind(t, registration.State); got != "registration" {
		t.Errorf("registration State kind = %q, want registration", got)
	}
	registrationResponse := mustRegister(t, a, registration.Options)
	for _, kind := range []any{"assertion", "", nil} {
		cred, err := rp.FinishRegistration(ctx, webauthn.RegistrationFinish{State: withKind(t, registration.State, kind), Response: registrationResponse})
		if !errors.Is(err, webauthn.ErrCeremonyRejected) || cred.ID != nil {
			t.Errorf("FinishRegistration(kind %v) = %+v, %v; want ErrCeremonyRejected", kind, cred, err)
		}
	}
	stored, err := rp.FinishRegistration(ctx, webauthn.RegistrationFinish{State: registration.State, Response: registrationResponse})
	if err != nil {
		t.Fatalf("FinishRegistration(the faithful control) = %v, want nil", err)
	}

	assertion, err := rp.BeginAssertion(ctx, webauthn.AssertionStart{})
	if err != nil {
		t.Fatalf("BeginAssertion = %v", err)
	}
	if got := stateKind(t, assertion.State); got != "assertion" {
		t.Errorf("assertion State kind = %q, want assertion", got)
	}
	assertionResponse := mustAssert(t, a, assertion.Options)
	finish := func(state []byte) (webauthn.Assertion, error) {
		return rp.FinishAssertion(ctx, webauthn.AssertionFinish{State: state, Response: assertionResponse, Credential: stored, UserHandle: user.Handle})
	}
	for _, kind := range []any{"registration", "", nil} {
		got, err := finish(withKind(t, assertion.State, kind))
		if !errors.Is(err, webauthn.ErrCeremonyRejected) || got != (webauthn.Assertion{}) {
			t.Errorf("FinishAssertion(kind %v) = %+v, %v; want ErrCeremonyRejected", kind, got, err)
		}
	}
	if _, err := finish(assertion.State); err != nil {
		t.Fatalf("FinishAssertion(the faithful control) = %v, want nil", err)
	}
}

func TestUnsolicitedClientExtensionOutputs(t *testing.T) {
	rp := newRelyingParty(t)
	ctx := context.Background()
	outputs := map[string]any{"credProps": map[string]any{"rk": true}, "webauthntest-unsolicited": map[string]any{"set": true}}
	a := newAuthenticator()
	user := newUser()

	registration, err := rp.BeginRegistration(ctx, webauthn.RegistrationStart{User: user})
	if err != nil {
		t.Fatalf("BeginRegistration = %v", err)
	}
	cred, err := rp.FinishRegistration(ctx, webauthn.RegistrationFinish{
		State:    registration.State,
		Response: withClientExtensionResults(t, mustRegister(t, newAuthenticator(), registration.Options), outputs),
	})
	if !errors.Is(err, webauthn.ErrCeremonyRejected) || cred.ID != nil {
		t.Errorf("FinishRegistration(unsolicited client extension outputs) = %+v, %v; want ErrCeremonyRejected", cred, err)
	}

	registration, err = rp.BeginRegistration(ctx, webauthn.RegistrationStart{User: user})
	if err != nil {
		t.Fatalf("BeginRegistration = %v", err)
	}
	stored, err := rp.FinishRegistration(ctx, webauthn.RegistrationFinish{State: registration.State, Response: mustRegister(t, a, registration.Options)})
	if err != nil {
		t.Fatalf("FinishRegistration = %v", err)
	}
	assertion, err := rp.BeginAssertion(ctx, webauthn.AssertionStart{})
	if err != nil {
		t.Fatalf("BeginAssertion = %v", err)
	}
	got, err := rp.FinishAssertion(ctx, webauthn.AssertionFinish{
		State:      assertion.State,
		Response:   withClientExtensionResults(t, mustAssert(t, a, assertion.Options), outputs),
		Credential: stored,
		UserHandle: user.Handle,
	})
	if err != nil || !got.UserVerified {
		t.Errorf("FinishAssertion(unsolicited client extension outputs) = %+v, %v; want them ignored", got, err)
	}
}

func TestErrorsExposeNoLibraryType(t *testing.T) {
	rp := newRelyingParty(t)
	ctx := context.Background()
	registration, err := rp.BeginRegistration(ctx, webauthn.RegistrationStart{User: newUser()})
	if err != nil {
		t.Fatalf("BeginRegistration = %v", err)
	}
	response := mustRegister(t, newAuthenticator(), registration.Options)
	var edited map[string]any
	if err := json.Unmarshal(response, &edited); err != nil {
		t.Fatal(err)
	}
	edited["id"] = "AAAA"
	response, err = json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rp.FinishRegistration(ctx, webauthn.RegistrationFinish{State: registration.State, Response: response})
	if !errors.Is(err, webauthn.ErrCeremonyRejected) {
		t.Fatalf("FinishRegistration(id differs from rawId) = %v, want ErrCeremonyRejected", err)
	}
	var libraryErr *protocol.Error
	if errors.As(err, &libraryErr) {
		t.Errorf("FinishRegistration error %v exposes a go-webauthn *protocol.Error", err)
	}
}

func testConfig(t *testing.T) webauthn.Config {
	t.Helper()
	origins, err := web.NewOriginPolicy(web.OriginPolicyConfig{Exact: []string{testOrigin}})
	if err != nil {
		t.Fatal(err)
	}
	return webauthn.Config{RPID: testRPID, RPName: "gowebauthn", Origins: origins}
}

func newRelyingParty(t *testing.T) *gowebauthn.RelyingParty {
	t.Helper()
	rp, err := gowebauthn.New(testConfig(t))
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	return rp
}

func newAuthenticator() *webauthntest.Authenticator {
	return webauthntest.NewAuthenticator(webauthntest.AuthenticatorConfig{RPID: testRPID, Origin: testOrigin, UserVerifying: true})
}

func newUser() webauthn.User {
	handle := make([]byte, 32)
	_, _ = rand.Read(handle)
	return webauthn.User{Handle: handle, Name: "user@example.com", DisplayName: "User"}
}

func mustRegister(t *testing.T, a *webauthntest.Authenticator, options json.RawMessage) []byte {
	t.Helper()
	response, err := a.Register(options)
	if err != nil {
		t.Fatalf("Authenticator.Register = %v", err)
	}
	return response
}

func mustAssert(t *testing.T, a *webauthntest.Authenticator, options json.RawMessage) []byte {
	t.Helper()
	response, err := a.Assert(options)
	if err != nil {
		t.Fatalf("Authenticator.Assert = %v", err)
	}
	return response
}

func stateKind(t *testing.T, state []byte) string {
	t.Helper()
	var envelope struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(state, &envelope); err != nil {
		t.Fatalf("decode State: %v", err)
	}
	return envelope.Kind
}

// withKind rewrites a State's kind; a nil kind removes the member.
func withKind(t *testing.T, state []byte, kind any) []byte {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(state, &envelope); err != nil {
		t.Fatalf("decode State: %v", err)
	}
	if kind == nil {
		delete(envelope, "kind")
	} else {
		envelope["kind"] = kind
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// withClientExtensionResults replaces a response's clientExtensionResults,
// which no signature covers.
func withClientExtensionResults(t *testing.T, response []byte, outputs map[string]any) []byte {
	t.Helper()
	var edited map[string]any
	if err := json.Unmarshal(response, &edited); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	edited["clientExtensionResults"] = outputs
	out, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

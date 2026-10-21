//go:build emulator

// The Firebase Auth emulator leg. It proves plumbing against a real emulator
// (firebase-tools, workshop/dev/firebase) and skips LOUDLY without one. The
// emulator issues UNSIGNED tokens, so nothing here proves signature or key
// verification against Google's real keys; the generic OIDC verifier's tests
// prove the signed path, and the live leg proves a real project.
//
//	make dev-firebase
//	FIREBASE_AUTH_EMULATOR_HOST=127.0.0.1:9099 go test -tags=emulator ./...
//	make dev-firebase-down
package firebase

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"firebase.google.com/go/v4/auth"

	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/authenticationtest"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// defaultEmulatorProject matches workshop/dev/firebase/docker-compose.yml.
const defaultEmulatorProject = "demo-gopernicus"

type emulator struct {
	host, project string
	client        *auth.Client
}

func requireEmulator(t *testing.T) emulator {
	t.Helper()
	host := os.Getenv(EmulatorHostEnv)
	if host == "" {
		t.Skip("SKIP (Firebase Auth emulator NOT verified): set FIREBASE_AUTH_EMULATOR_HOST (make dev-firebase, then FIREBASE_AUTH_EMULATOR_HOST=127.0.0.1:9099)")
	}
	project := os.Getenv("FIREBASE_PROJECT_ID")
	if project == "" {
		project = defaultEmulatorProject
	}
	client, err := NewClient(context.Background(), ClientConfig{ProjectID: project})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return emulator{host: host, project: project, client: client}
}

func (e emulator) config() Config {
	return Config{ProjectID: e.project, Mode: environment.ModeDevelopment}
}

// rest calls the emulator's Identity Toolkit REST API. Client endpoints take
// any API key; admin endpoints accept the "owner" bearer.
func (e emulator) rest(t *testing.T, path string, body any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "http://"+e.host+"/identitytoolkit.googleapis.com/v1/"+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(path, "projects/") {
		req.Header.Set("Authorization", "Bearer owner")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("emulator %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("emulator %s: %d %s", path, resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("emulator %s: %v", path, err)
	}
	return out
}

func (e emulator) signUp(t *testing.T, email, password, name string) (uid, idToken string) {
	t.Helper()
	out := e.rest(t, "accounts:signUp?key=emulator", map[string]any{
		"email": email, "password": password, "displayName": name, "returnSecureToken": true,
	})
	return out["localId"].(string), out["idToken"].(string)
}

func (e emulator) signIn(t *testing.T, email, password string) string {
	t.Helper()
	out := e.rest(t, "accounts:signInWithPassword?key=emulator", map[string]any{
		"email": email, "password": password, "returnSecureToken": true,
	})
	return out["idToken"].(string)
}

func (e emulator) signInAnonymously(t *testing.T) (uid, idToken string) {
	t.Helper()
	out := e.rest(t, "accounts:signUp?key=emulator", map[string]any{"returnSecureToken": true})
	return out["localId"].(string), out["idToken"].(string)
}

func uniqueEmail(t *testing.T) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "emu-" + hex.EncodeToString(b) + "@example.test"
}

func header(raw string) sdkauth.Credential {
	return sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: raw}
}

func TestEmulatorIDToken(t *testing.T) {
	e := requireEmulator(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	uid, idToken := e.signUp(t, email, "correct-horse", "Emu Lator")

	p, err := New(ctx, e.client, e.config())
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Authenticate(ctx, header(idToken))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.Issuer != IDTokenIssuer(e.project) || id.Subject != uid || id.Email != email || id.EmailVerified ||
		id.DisplayName != "Emu Lator" || id.AuthenticatedAt.IsZero() || id.ExpiresAt.Before(time.Now()) ||
		id.Attributes[AttributeSignInProvider] != "password" || id.MFA || id.Phone != "" {
		t.Fatalf("Identity = %+v", id)
	}
	t.Logf("Identity: %+v", id)

	// Record what the Admin SDK's Token.Claims carries (README "Token.Claims").
	tok, err := e.client.VerifyIDToken(ctx, idToken)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(tok.Claims))
	for k := range tok.Claims {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("Token.Claims keys: %v", keys)
	for _, stripped := range []string{"iss", "aud", "exp", "iat", "sub", "uid"} {
		if _, ok := tok.Claims[stripped]; ok {
			t.Errorf("Token.Claims carries %q; the README says the SDK strips it", stripped)
		}
	}
	for _, kept := range []string{"email", "email_verified", "name", "auth_time", "user_id", "firebase"} {
		if _, ok := tok.Claims[kept]; !ok {
			t.Errorf("Token.Claims lacks %q; the README says it is kept", kept)
		}
	}

	// Emulator mode never checks signatures: a garbage signature verifies.
	// This is why production construction refuses emulator mode.
	if _, err := p.Authenticate(ctx, header(idToken+"garbage-signature")); err != nil {
		t.Errorf("emulator mode now checks signatures? %v", err)
	}
}

func TestEmulatorSessionCookie(t *testing.T) {
	e := requireEmulator(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	uid, idToken := e.signUp(t, email, "correct-horse", "Cookie User")

	idp, err := New(ctx, e.client, e.config())
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := NewSessionCookie(ctx, e.client, e.config())
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := idp.MintSessionCookie(ctx, idToken, time.Hour)
	if err != nil {
		t.Fatalf("MintSessionCookie: %v", err)
	}
	id, err := cookies.Authenticate(ctx, sdkauth.Credential{Transport: sdkauth.TransportCookie, Raw: cookie})
	if err != nil {
		t.Fatalf("cookie Authenticate: %v", err)
	}
	if id.Issuer != SessionCookieIssuer(e.project) || id.Subject != uid || id.Email != email {
		t.Fatalf("cookie Identity = %+v", id)
	}
	if _, err := idp.Authenticate(ctx, header(cookie)); !errors.Is(err, sdkauth.ErrIssuerMismatch) {
		t.Errorf("cookie at the ID-token provider: %v", err)
	}
	if _, err := cookies.Authenticate(ctx, header(idToken)); !errors.Is(err, sdkauth.ErrIssuerMismatch) {
		t.Errorf("ID token at the cookie provider: %v", err)
	}
	if _, err := idp.MintSessionCookie(ctx, "a.b.c", time.Hour); !errors.Is(err, sdkauth.ErrInvalidCredential) {
		t.Errorf("mint from garbage: %v", err)
	}
}

// TestEmulatorRevocation: a token minted before RevokeRefreshTokens is
// reported revoked by the facts (ValidAfter after its AuthenticatedAt), a new
// sign-in is not, and a disabled user reads Disabled.
func TestEmulatorRevocation(t *testing.T) {
	e := requireEmulator(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	uid, before := e.signUp(t, email, "correct-horse", "")

	p, err := New(ctx, e.client, e.config())
	if err != nil {
		t.Fatal(err)
	}
	old, err := p.Authenticate(ctx, header(before))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := p.CheckRevocation(ctx, p.Issuer(), uid)
	if err != nil || facts.Disabled || old.AuthenticatedAt.Before(facts.ValidAfter) {
		t.Fatalf("fresh user: facts=%+v err=%v (token auth %v)", facts, err, old.AuthenticatedAt)
	}

	// Firebase revocation has one-second granularity.
	time.Sleep(1100 * time.Millisecond)
	if err := e.client.RevokeRefreshTokens(ctx, uid); err != nil {
		t.Fatal(err)
	}
	facts, err = p.CheckRevocation(ctx, p.Issuer(), uid)
	if err != nil {
		t.Fatal(err)
	}
	if !old.AuthenticatedAt.Before(facts.ValidAfter) {
		t.Fatalf("pre-revocation token not revoked by facts: auth %v, ValidAfter %v", old.AuthenticatedAt, facts.ValidAfter)
	}
	t.Logf("revoked: token auth_time %v < ValidAfter %v", old.AuthenticatedAt, facts.ValidAfter)
	// In emulator mode the Admin SDK itself also checks revocation on verify.
	if _, err := p.Authenticate(ctx, header(before)); !errors.Is(err, sdkauth.ErrInvalidCredential) {
		t.Errorf("revoked token in emulator mode: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)
	fresh, err := p.Authenticate(ctx, header(e.signIn(t, email, "correct-horse")))
	if err != nil {
		t.Fatalf("re-sign-in after revoke: %v", err)
	}
	if fresh.AuthenticatedAt.Before(facts.ValidAfter) {
		t.Fatalf("re-sign-in reads revoked: auth %v, ValidAfter %v", fresh.AuthenticatedAt, facts.ValidAfter)
	}

	if _, err := e.client.UpdateUser(ctx, uid, (&auth.UserToUpdate{}).Disabled(true)); err != nil {
		t.Fatal(err)
	}
	if facts, err := p.CheckRevocation(ctx, p.Issuer(), uid); err != nil || !facts.Disabled {
		t.Fatalf("disabled: facts=%+v err=%v", facts, err)
	}
	if _, err := p.CheckRevocation(ctx, p.Issuer(), "no-such-"+uid); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("unknown subject: %v", err)
	}
}

func TestEmulatorAnonymousDenied(t *testing.T) {
	e := requireEmulator(t)
	ctx := context.Background()
	_, idToken := e.signInAnonymously(t)

	p, err := New(ctx, e.client, e.config())
	if err != nil {
		t.Fatal(err)
	}
	if id, err := p.Authenticate(ctx, header(idToken)); !errors.Is(err, sdkauth.ErrSignInMethodDenied) {
		t.Fatalf("anonymous: id=%+v err=%v, want ErrSignInMethodDenied", id, err)
	}
	// Firebase mints a cookie for an anonymous user; the cookie provider denies it.
	cookie, err := p.MintSessionCookie(ctx, idToken, time.Hour)
	if err != nil {
		t.Fatalf("MintSessionCookie(anonymous): %v", err)
	}
	cookies, err := NewSessionCookie(ctx, e.client, e.config())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cookies.Authenticate(ctx, sdkauth.Credential{Transport: sdkauth.TransportCookie, Raw: cookie}); !errors.Is(err, sdkauth.ErrSignInMethodDenied) {
		t.Fatalf("anonymous cookie: %v, want ErrSignInMethodDenied", err)
	}
}

func TestEmulatorProductionRefused(t *testing.T) {
	e := requireEmulator(t)
	prod := Config{ProjectID: e.project, Mode: environment.ModeProduction}
	if p, err := New(context.Background(), e.client, prod); !errors.Is(err, ErrEmulatorInProduction) || p != nil {
		t.Fatalf("New production: p=%v err=%v", p, err)
	}
	if p, err := NewSessionCookie(context.Background(), e.client, prod); !errors.Is(err, ErrEmulatorInProduction) || p != nil {
		t.Fatalf("NewSessionCookie production: p=%v err=%v", p, err)
	}
}

// TestEmulatorConformance runs the sdk suites against the real emulator with
// unsigned suite-minted tokens. The suite subject is created with validSince
// reset, because the emulator checks revocation on every verify and the suite
// backdates iat.
func TestEmulatorConformance(t *testing.T) {
	e := requireEmulator(t)
	ctx := context.Background()
	const subject = "authenticationtest-subject"
	if _, err := e.client.GetUser(ctx, subject); auth.IsUserNotFound(err) {
		if _, err := e.client.CreateUser(ctx, (&auth.UserToCreate{}).UID(subject)); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	e.rest(t, "projects/"+e.project+"/accounts:update", map[string]any{"localId": subject, "validSince": "1"})

	for name, build := range map[string]func(context.Context, *auth.Client, Config) (*Provider, error){
		"IDToken":       New,
		"SessionCookie": NewSessionCookie,
	} {
		t.Run(name, func(t *testing.T) {
			newProvider := func(t *testing.T) sdkauth.Provider {
				p, err := build(ctx, e.client, e.config())
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			authenticationtest.RunProvider(t, newProvider, unsignedMinter(e.project))
			authenticationtest.RunRevocation(t, func(t *testing.T) sdkauth.RevocationChecker {
				return newProvider(t).(sdkauth.RevocationChecker)
			}, newProvider(t).Issuer(), subject)
		})
	}
}

// TestEmulatorPhone signs in by SMS code (the emulator exposes the code) and
// proves a phone token maps to a verified phone with no email.
func TestEmulatorPhone(t *testing.T) {
	e := requireEmulator(t)
	ctx := context.Background()
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	phone := fmt.Sprintf("+1555%07d", (int(b[0])<<16|int(b[1])<<8|int(b[2]))%10000000)

	session := e.rest(t, "accounts:sendVerificationCode?key=emulator", map[string]any{"phoneNumber": phone, "recaptchaToken": "emulator"})["sessionInfo"].(string)
	resp, err := http.Get("http://" + e.host + "/emulator/v1/projects/" + e.project + "/verificationCodes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var codes struct {
		VerificationCodes []struct{ Code, SessionInfo string } `json:"verificationCodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&codes); err != nil {
		t.Fatal(err)
	}
	var code string
	for _, c := range codes.VerificationCodes {
		if c.SessionInfo == session {
			code = c.Code
		}
	}
	if code == "" {
		t.Fatal("verification code not found")
	}
	idToken := e.rest(t, "accounts:signInWithPhoneNumber?key=emulator", map[string]any{"sessionInfo": session, "code": code})["idToken"].(string)

	p, err := New(ctx, e.client, e.config())
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Authenticate(ctx, header(idToken))
	if err != nil {
		t.Fatal(err)
	}
	if id.Phone != phone || !id.PhoneVerified || id.Email != "" || id.Attributes[AttributeSignInProvider] != "phone" {
		t.Fatalf("phone Identity = %+v", id)
	}
}

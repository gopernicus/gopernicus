package firebase

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cloudauth "cloud.google.com/go/auth"
	"firebase.google.com/go/v4/auth"
	"golang.org/x/oauth2"

	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/authenticationtest"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const testProject = "demo-gopernicus-unit"

// emulatorPayload is the payload of a real Auth emulator ID token
// (firebase-tools 15.34.0, email/password sign-up), with the subject renamed.
const emulatorPayload = `{
 "name": "Alice",
 "email": "a@x.test",
 "email_verified": true,
 "auth_time": 1792553247,
 "user_id": "uid-alice",
 "firebase": {"identities": {"email": ["a@x.test"]}, "sign_in_provider": "password"},
 "iat": 1792553247,
 "exp": 1792556847,
 "aud": "demo-gopernicus-unit",
 "iss": "https://securetoken.google.com/demo-gopernicus-unit",
 "sub": "uid-alice"
}`

// fakeUser is what the fake Identity Toolkit serves from accounts:lookup.
type fakeUser struct {
	Disabled   bool
	ValidSince int64 // seconds
}

// fakeToolkit stands in for the Auth emulator's REST surface, so the real
// Admin SDK client runs end to end (emulator mode: unsigned tokens, lookups on
// every verification) with no container.
type fakeToolkit struct {
	mu      sync.Mutex
	users   map[string]fakeUser
	status  int // non-zero: every call answers this status
	lookups atomic.Int64
	srv     *httptest.Server
}

func newFakeToolkit(t *testing.T) *fakeToolkit {
	t.Helper()
	f := &fakeToolkit{users: map[string]fakeUser{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	t.Setenv(EmulatorHostEnv, strings.TrimPrefix(f.srv.URL, "http://"))
	return f
}

func (f *fakeToolkit) put(uid string, u fakeUser) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[uid] = u
}

func (f *fakeToolkit) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	status := f.status
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"message":"FAKE_FAILURE"}}`)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/accounts:lookup"):
		f.lookups.Add(1)
		var users []map[string]any
		ids, _ := body["localId"].([]any)
		f.mu.Lock()
		for _, id := range ids {
			uid, _ := id.(string)
			if u, ok := f.users[uid]; ok {
				users = append(users, map[string]any{
					"localId":    uid,
					"disabled":   u.Disabled,
					"validSince": itoa(u.ValidSince),
				})
			}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"users": users})
	case strings.HasSuffix(r.URL.Path, ":createSessionCookie"):
		raw, _ := body["idToken"].(string)
		claims, err := decodeUnsigned(raw)
		if err != nil || claims["iss"] != IDTokenIssuer(testProject) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"INVALID_ID_TOKEN"}}`)
			return
		}
		claims["iss"] = SessionCookieIssuer(testProject)
		_ = json.NewEncoder(w).Encode(map[string]any{"sessionCookie": unsigned(claims)})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func unsigned(claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p) + "."
}

func decodeUnsigned(raw string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("segments")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	return claims, json.Unmarshal(b, &claims)
}

func emulatorClient(t *testing.T) *auth.Client {
	t.Helper()
	client, err := NewClient(context.Background(), ClientConfig{ProjectID: testProject})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func devConfig() Config {
	return Config{ProjectID: testProject, Mode: environment.ModeDevelopment, Logger: slog.New(slog.DiscardHandler)}
}

func unsignedMinter(projectID string) authenticationtest.Minter {
	return func(t *testing.T, claims map[string]any) string {
		c := make(map[string]any, len(claims)+1)
		for k, v := range claims {
			c[k] = v
		}
		if _, ok := c["aud"]; !ok {
			c["aud"] = projectID
		}
		return unsigned(c)
	}
}

// TestConformance runs the sdk suites over the real Admin SDK in emulator mode
// against the fake Identity Toolkit.
func TestConformance(t *testing.T) {
	fake := newFakeToolkit(t)
	fake.put("authenticationtest-subject", fakeUser{ValidSince: 1})
	client := emulatorClient(t)

	for name, build := range map[string]func(context.Context, *auth.Client, Config) (*Provider, error){
		"IDToken":       New,
		"SessionCookie": NewSessionCookie,
	} {
		t.Run(name, func(t *testing.T) {
			newProvider := func(t *testing.T) sdkauth.Provider {
				p, err := build(context.Background(), client, devConfig())
				if err != nil {
					t.Fatalf("construct: %v", err)
				}
				return p
			}
			authenticationtest.RunProvider(t, newProvider, unsignedMinter(testProject))
			newChecker := func(t *testing.T) sdkauth.RevocationChecker {
				return newProvider(t).(sdkauth.RevocationChecker)
			}
			authenticationtest.RunRevocation(t, newChecker, newProvider(t).Issuer(), "authenticationtest-subject")
		})
	}
}

func TestIssuers(t *testing.T) {
	if got := IDTokenIssuer("p1"); got != "https://securetoken.google.com/p1" {
		t.Errorf("IDTokenIssuer = %q", got)
	}
	if got := SessionCookieIssuer("p1"); got != "https://session.firebase.google.com/p1" {
		t.Errorf("SessionCookieIssuer = %q", got)
	}
}

// TestProfileParity proves the go-oidc path (Profile through NewOIDC) and the
// Admin SDK path build the same Identity from the same emulator token payload.
func TestProfileParity(t *testing.T) {
	fake := newFakeToolkit(t)
	fake.put("uid-alice", fakeUser{ValidSince: 1})
	var claims map[string]any
	if err := json.Unmarshal([]byte(emulatorPayload), &claims); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	claims["iat"], claims["auth_time"], claims["exp"] = float64(now-10), float64(now-20), float64(now+600)
	raw := unsigned(claims)

	oidc := sdkauth.NewOIDC(IDTokenIssuer(testProject), staticVerifier{claims: claims}, Profile(testProject))
	viaProfile, err := oidc.Authenticate(context.Background(), sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: raw})
	if err != nil {
		t.Fatalf("NewOIDC(Profile).Authenticate: %v", err)
	}

	p, err := New(context.Background(), emulatorClient(t), devConfig())
	if err != nil {
		t.Fatal(err)
	}
	viaAdmin, err := p.Authenticate(context.Background(), sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: raw})
	if err != nil {
		t.Fatalf("Provider.Authenticate: %v", err)
	}
	if !reflect.DeepEqual(viaProfile, viaAdmin) {
		t.Fatalf("identities differ:\n profile %+v\n admin   %+v", viaProfile, viaAdmin)
	}
	want := sdkauth.Identity{
		Issuer: IDTokenIssuer(testProject), Subject: "uid-alice",
		Email: "a@x.test", EmailVerified: true, DisplayName: "Alice",
		AuthenticatedAt: time.Unix(now-20, 0).UTC(), ExpiresAt: time.Unix(now+600, 0).UTC(),
		Attributes: map[string]string{AttributeSignInProvider: "password"},
	}
	if !reflect.DeepEqual(viaAdmin, want) {
		t.Fatalf("Identity = %+v\nwant       %+v", viaAdmin, want)
	}

	anon := cloneClaims(claims)
	anon["firebase"] = map[string]any{"sign_in_provider": SignInAnonymous}
	if _, err := sdkauth.NewOIDC(IDTokenIssuer(testProject), staticVerifier{claims: anon}, Profile(testProject)).
		Authenticate(context.Background(), sdkauth.Credential{Raw: unsigned(anon)}); !errors.Is(err, sdkauth.ErrSignInMethodDenied) {
		t.Fatalf("Profile anonymous: err = %v, want ErrSignInMethodDenied", err)
	}
}

type staticVerifier struct{ claims map[string]any }

func (s staticVerifier) Verify(context.Context, string) (map[string]any, error) {
	return cloneClaims(s.claims), nil
}

func cloneClaims(c map[string]any) map[string]any {
	out := make(map[string]any, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

func TestIdentityFromToken(t *testing.T) {
	iss := IDTokenIssuer(testProject)
	base := func() *auth.Token {
		return &auth.Token{
			Issuer: iss, Subject: "u1", UID: "u1", AuthTime: 1000, IssuedAt: 2000, Expires: 5000,
			Firebase: auth.FirebaseInfo{SignInProvider: "phone", Tenant: "tenant-a"},
			Claims: map[string]any{
				"phone_number": "+15555550100",
				"email":        "u1@x.test",
				"firebase":     map[string]any{"sign_in_provider": "phone", "sign_in_second_factor": "totp"},
			},
		}
	}

	id, err := identityFromToken(base(), iss, DefaultDenySignIn())
	if err != nil {
		t.Fatal(err)
	}
	want := sdkauth.Identity{
		Issuer: iss, Subject: "u1", Email: "u1@x.test", Phone: "+15555550100", PhoneVerified: true,
		Tenant: "tenant-a", AuthenticatedAt: time.Unix(1000, 0).UTC(), ExpiresAt: time.Unix(5000, 0).UTC(),
		MFA: true, Attributes: map[string]string{AttributeSignInProvider: "phone"},
	}
	if !reflect.DeepEqual(id, want) {
		t.Fatalf("Identity = %+v\nwant       %+v", id, want)
	}
	if id.EmailVerified {
		t.Error("EmailVerified without email_verified claim")
	}

	tok := base()
	tok.AuthTime = 0
	if id, _ := identityFromToken(tok, iss, nil); !id.AuthenticatedAt.Equal(time.Unix(2000, 0)) {
		t.Errorf("AuthenticatedAt without auth_time = %v, want iat", id.AuthenticatedAt)
	}

	// The struct is authoritative: a claims map disagreeing on the subject or
	// sign-in provider changes nothing.
	tok = base()
	tok.Claims["sub"] = "someone-else"
	tok.Claims["firebase"] = map[string]any{"sign_in_provider": "password"}
	tok.Firebase.SignInProvider = SignInAnonymous
	if _, err := identityFromToken(tok, iss, DefaultDenySignIn()); !errors.Is(err, sdkauth.ErrSignInMethodDenied) {
		t.Errorf("anonymous by struct field: err = %v, want ErrSignInMethodDenied", err)
	}
	if id, err := identityFromToken(tok, iss, []string{}); err != nil || id.Subject != "u1" {
		t.Errorf("empty DenySignIn admits anonymous with struct subject: id=%+v err=%v", id, err)
	}

	for name, mutate := range map[string]func(*auth.Token){
		"foreign issuer": func(tk *auth.Token) { tk.Issuer = SessionCookieIssuer(testProject) },
		"no subject":     func(tk *auth.Token) { tk.Subject = "" },
		"no exp":         func(tk *auth.Token) { tk.Expires = 0 },
	} {
		tk := base()
		mutate(tk)
		id, err := identityFromToken(tk, iss, nil)
		if !errors.Is(err, sdkauth.ErrInvalidCredential) || !reflect.DeepEqual(id, sdkauth.Identity{}) {
			t.Errorf("%s: id=%+v err=%v", name, id, err)
		}
	}
	if _, err := identityFromToken(nil, iss, nil); !errors.Is(err, sdkauth.ErrInvalidCredential) {
		t.Errorf("nil token: %v", err)
	}
}

func TestAuthenticateRejectsBeforeVerify(t *testing.T) {
	var calls atomic.Int64
	p := &Provider{
		issuer: IDTokenIssuer(testProject),
		verify: func(context.Context, string) (*auth.Token, error) {
			calls.Add(1)
			return nil, errors.New("unreachable")
		},
	}
	mint := unsignedMinter(testProject)
	for name, raw := range map[string]string{
		"oversize": mint(t, map[string]any{"iss": p.issuer, "pad": strings.Repeat("a", sdkauth.MaxCredentialBytes)}),
		"foreign":  mint(t, map[string]any{"iss": SessionCookieIssuer(testProject)}),
		"shape":    "a.b",
		"base64":   "a.!!!.c",
		"json":     "a." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".c",
	} {
		if _, err := p.Authenticate(context.Background(), sdkauth.Credential{Raw: raw}); !errors.Is(err, sdkauth.ErrInvalidCredential) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := p.Authenticate(context.Background(), sdkauth.Credential{Raw: mint(t, map[string]any{"iss": "x"})}); !errors.Is(err, sdkauth.ErrIssuerMismatch) {
		t.Errorf("foreign iss: %v, want ErrIssuerMismatch", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("verify called %d times on cheap rejections", n)
	}
}

func TestAnonymousDeniedByDefault(t *testing.T) {
	fake := newFakeToolkit(t)
	fake.put("anon", fakeUser{ValidSince: 1})
	client := emulatorClient(t)
	now := time.Now().Unix()
	raw := unsigned(map[string]any{
		"iss": IDTokenIssuer(testProject), "aud": testProject, "sub": "anon", "iat": now, "exp": now + 600, "auth_time": now,
		"firebase": map[string]any{"sign_in_provider": SignInAnonymous},
	})

	p, _ := New(context.Background(), client, devConfig())
	if _, err := p.Authenticate(context.Background(), sdkauth.Credential{Raw: raw}); !errors.Is(err, sdkauth.ErrSignInMethodDenied) {
		t.Fatalf("default: err = %v, want ErrSignInMethodDenied", err)
	}
	cfg := devConfig()
	cfg.DenySignIn = []string{}
	p, _ = New(context.Background(), client, cfg)
	if id, err := p.Authenticate(context.Background(), sdkauth.Credential{Raw: raw}); err != nil || id.Subject != "anon" {
		t.Fatalf("DenySignIn=[]: id=%+v err=%v", id, err)
	}
}

func TestConstruction(t *testing.T) {
	t.Setenv(EmulatorHostEnv, "127.0.0.1:1")
	client := emulatorClient(t)
	ctx := context.Background()

	for name, cfg := range map[string]Config{
		"no project":   {Mode: environment.ModeDevelopment},
		"path project": {ProjectID: "a/b", Mode: environment.ModeDevelopment},
		"no mode":      {ProjectID: testProject},
		"invalid mode": {ProjectID: testProject, Mode: "staging"},
	} {
		if _, err := New(ctx, client, cfg); !errors.Is(err, ErrInvalidConfig) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidConfig", name, err)
		}
	}
	if _, err := New(ctx, nil, devConfig()); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("nil client: %v", err)
	}
	if _, err := NewClient(ctx, ClientConfig{}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("NewClient without project: %v", err)
	}
}

func TestProductionRefusesEmulator(t *testing.T) {
	t.Setenv(EmulatorHostEnv, "127.0.0.1:1")
	client := emulatorClient(t)
	prod := Config{ProjectID: testProject, Mode: environment.ModeProduction}
	for name, build := range map[string]func(context.Context, *auth.Client, Config) (*Provider, error){"New": New, "NewSessionCookie": NewSessionCookie} {
		if p, err := build(context.Background(), client, prod); !errors.Is(err, ErrEmulatorInProduction) || p != nil {
			t.Errorf("%s production under emulator: p=%v err=%v", name, p, err)
		}
	}

	var logs bytes.Buffer
	dev := Config{ProjectID: testProject, Mode: environment.ModeDevelopment, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	if _, err := New(context.Background(), client, dev); err != nil {
		t.Fatalf("development under emulator: %v", err)
	}
	if !strings.Contains(logs.String(), "UNSIGNED") {
		t.Errorf("development emulator warning missing: %q", logs.String())
	}

	t.Setenv(EmulatorHostEnv, "")
	if _, err := New(context.Background(), client, prod); err != nil {
		t.Errorf("production without emulator: %v", err)
	}
}

func TestCheckRevocation(t *testing.T) {
	fake := newFakeToolkit(t)
	fake.put("live", fakeUser{ValidSince: 1700000000})
	fake.put("off", fakeUser{Disabled: true, ValidSince: 1700000000})
	p, err := New(context.Background(), emulatorClient(t), devConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	facts, err := p.CheckRevocation(ctx, p.Issuer(), "live")
	if err != nil || facts.Disabled || !facts.ValidAfter.Equal(time.Unix(1700000000, 0)) || facts.CheckedAt.IsZero() {
		t.Fatalf("live: %+v %v", facts, err)
	}
	if facts, err := p.CheckRevocation(ctx, SessionCookieIssuer(testProject), "off"); err != nil || !facts.Disabled {
		t.Fatalf("off via session issuer: %+v %v", facts, err)
	}
	if _, err := p.CheckRevocation(ctx, "https://securetoken.google.com/other", "live"); !errors.Is(err, sdkauth.ErrIssuerMismatch) {
		t.Errorf("foreign project: %v", err)
	}
	if _, err := p.CheckRevocation(ctx, p.Issuer(), "ghost"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}

	for status, kind := range map[int]sdkauth.UnavailableKind{
		http.StatusUnauthorized:        sdkauth.UnavailableRejectedCredential,
		http.StatusForbidden:           sdkauth.UnavailableRejectedCredential,
		http.StatusInternalServerError: sdkauth.UnavailableUpstream,
	} {
		fake.mu.Lock()
		fake.status = status
		fake.mu.Unlock()
		_, err := p.CheckRevocation(ctx, p.Issuer(), "live")
		assertUnavailable(t, err, kind)
	}
}

func TestClassify(t *testing.T) {
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for name, tc := range map[string]struct {
		ctx  context.Context
		err  error
		kind sdkauth.UnavailableKind
	}{
		"deadline":          {ctx, context.DeadlineExceeded, sdkauth.UnavailableDeadline},
		"caller canceled":   {canceled, errors.New("boom"), sdkauth.UnavailableDeadline},
		"adc key rejected":  {ctx, &url.Error{Op: "Post", Err: &oauth2.RetrieveError{Response: &http.Response{StatusCode: 400}}}, sdkauth.UnavailableRejectedCredential},
		"adc token 5xx":     {ctx, &oauth2.RetrieveError{Response: &http.Response{StatusCode: 503}}, sdkauth.UnavailableUpstream},
		"json key rejected": {ctx, &cloudauth.Error{Response: &http.Response{StatusCode: 401}}, sdkauth.UnavailableRejectedCredential},
		"network":           {ctx, &net.OpError{Op: "dial", Err: errors.New("refused")}, sdkauth.UnavailableNetwork},
	} {
		t.Run(name, func(t *testing.T) {
			assertUnavailable(t, classify(tc.ctx, tc.err, sdkauth.ErrInvalidCredential), tc.kind)
		})
	}
	err := classify(ctx, errors.New("ID token has invalid 'aud'"), sdkauth.ErrInvalidCredential)
	if !errors.Is(err, sdkauth.ErrInvalidCredential) || errors.Is(err, sdk.ErrUnavailable) {
		t.Errorf("token rejection: %v", err)
	}
}

func assertUnavailable(t *testing.T, err error, kind sdkauth.UnavailableKind) {
	t.Helper()
	var u *sdkauth.UnavailableError
	if !errors.As(err, &u) || u.Kind != kind {
		t.Fatalf("err = %v, want UnavailableError kind %q", err, kind)
	}
	if !errors.Is(err, sdk.ErrUnavailable) || errors.Is(err, sdkauth.ErrInvalidCredential) {
		t.Fatalf("err = %v: an outage must match only sdk.ErrUnavailable", err)
	}
}

func TestMintSessionCookie(t *testing.T) {
	fake := newFakeToolkit(t)
	fake.put("u1", fakeUser{ValidSince: 1})
	client := emulatorClient(t)
	idp, _ := New(context.Background(), client, devConfig())
	cookies, _ := NewSessionCookie(context.Background(), client, devConfig())
	ctx := context.Background()
	now := time.Now().Unix()
	idToken := unsigned(map[string]any{
		"iss": IDTokenIssuer(testProject), "aud": testProject, "sub": "u1", "iat": now, "exp": now + 600, "auth_time": now,
		"firebase": map[string]any{"sign_in_provider": "password"},
	})

	for _, ttl := range []time.Duration{0, MinSessionCookieTTL - time.Second, MaxSessionCookieTTL + time.Second} {
		if _, err := idp.MintSessionCookie(ctx, idToken, ttl); !errors.Is(err, ErrInvalidSessionTTL) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("ttl %s: %v", ttl, err)
		}
	}
	if _, err := idp.MintSessionCookie(ctx, "", time.Hour); !errors.Is(err, sdkauth.ErrInvalidCredential) {
		t.Errorf("empty id token: %v", err)
	}
	if _, err := idp.MintSessionCookie(ctx, "not-a-token", time.Hour); !errors.Is(err, sdkauth.ErrInvalidCredential) {
		t.Errorf("refused id token: %v", err)
	}

	cookie, err := idp.MintSessionCookie(ctx, idToken, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	id, err := cookies.Authenticate(ctx, sdkauth.Credential{Transport: sdkauth.TransportCookie, Raw: cookie})
	if err != nil || id.Subject != "u1" || id.Issuer != SessionCookieIssuer(testProject) {
		t.Fatalf("cookie: id=%+v err=%v", id, err)
	}
	if _, err := idp.Authenticate(ctx, sdkauth.Credential{Raw: cookie}); !errors.Is(err, sdkauth.ErrIssuerMismatch) {
		t.Errorf("cookie at the ID-token provider: %v", err)
	}
}

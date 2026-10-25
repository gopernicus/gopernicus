package authenticationhttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/webauthnstub"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn/webauthntest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	pkRPID     = "example.test"
	pkOrigin   = "https://example.test"
	pkEmail    = "passkey@example.com"
	pkPassword = "password123456789"
	pkCSRF     = "csrf-token-passkeys"
)

// memPasskeys is the inbound test's passkey.Repository over memUsers.
type memPasskeys struct {
	users *memUsers
	mu    sync.Mutex
	m     map[string]passkey.Passkey
}

func (f *memPasskeys) Create(_ context.Context, in passkey.CreateInput) (passkey.Passkey, error) {
	p := in.Passkey
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	u, ok := f.users.byID[p.UserID]
	if !ok {
		return passkey.Passkey{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return passkey.Passkey{}, session.ErrUserNotActive
	}
	if u.AuthRevision != in.ExpectedAuthRevision {
		return passkey.Passkey{}, sdk.ErrConflict
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	owned := 0
	for _, ex := range f.m {
		if ex.UserID == p.UserID && in.Bootstrap && ex.AAL2Capable() {
			return passkey.Passkey{}, passkey.ErrBootstrapClosed
		}
	}
	for _, ex := range f.m {
		if bytes.Equal(ex.CredentialID, p.CredentialID) {
			return passkey.Passkey{}, sdk.ErrAlreadyExists
		}
		if ex.UserID == p.UserID {
			owned++
			if !bytes.Equal(ex.UserHandle, p.UserHandle) {
				return passkey.Passkey{}, sdk.ErrConflict
			}
		}
	}
	if owned >= in.MaxPerUser {
		return passkey.Passkey{}, passkey.ErrPasskeyLimit
	}
	if p.ID == "" {
		p.ID = sdk.IDGenerator{}.MustGenerate()
	}
	f.m[p.ID] = p
	return p, nil
}

func (f *memPasskeys) GetByCredentialID(_ context.Context, id []byte) (passkey.Passkey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.m {
		if bytes.Equal(p.CredentialID, id) {
			return p, nil
		}
	}
	return passkey.Passkey{}, sdk.ErrNotFound
}

func (f *memPasskeys) ListByUser(_ context.Context, userID string) ([]passkey.Passkey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []passkey.Passkey{}
	for _, p := range f.m {
		if p.UserID == userID {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b passkey.Passkey) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (f *memPasskeys) Rename(_ context.Context, id, userID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[id]
	if !ok || p.UserID != userID {
		return sdk.ErrNotFound
	}
	p.Name = name
	f.m[id] = p
	return nil
}

func (f *memPasskeys) RecordUse(_ context.Context, id string, expected, count uint32, backupState bool, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[id]
	if !ok || p.SignCount != expected {
		return sdk.ErrConflict
	}
	p.SignCount, p.BackupState, p.LastUsedAt = count, backupState, &at
	f.m[id] = p
	return nil
}

func (f *memPasskeys) owns(userID, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[id]
	return ok && p.UserID == userID
}

func (f *memPasskeys) remove(match func(passkey.Passkey) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, p := range f.m {
		if match(p) {
			delete(f.m, id)
		}
	}
}

// capturePasskeyViews records the passkey page models.
type capturePasskeyViews struct {
	mu      sync.Mutex
	manage  PasskeysPage
	stepUp  PasskeyStepUpPage
	renders int
}

func (v *capturePasskeyViews) Passkeys(m PasskeysPage) web.Renderer {
	v.mu.Lock()
	v.manage, v.renders = m, v.renders+1
	v.mu.Unlock()
	return stubRenderer{"passkeys"}
}

func (v *capturePasskeyViews) PasskeyStepUp(m PasskeyStepUpPage) web.Renderer {
	v.mu.Lock()
	v.stepUp, v.renders = m, v.renders+1
	v.mu.Unlock()
	return stubRenderer{"passkey_step_up"}
}

type passkeyOptions struct {
	views      *capturePasskeyViews
	limits     PublicRouteLimits
	maxPerUser int
	unwired    bool
	adminCheck UserAdminCheck
	adminMFA   bool
	machine    bool
	uv         webauthn.Requirement
}

type passkeyFixture struct {
	h        http.Handler
	svc      *testService
	users    *memUsers
	sessions *memSessions
	passkeys *memPasskeys
	events   *spySecurityEvents
	signer   *fakeSigner
	rp       *webauthnstub.RelyingParty
	offset   *time.Duration
	userID   string
}

func newPasskeyFixture(t *testing.T, o passkeyOptions) *passkeyFixture {
	t.Helper()
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	grants := &memAuthGrants{m: map[string]authgrant.Grant{}}
	passkeys := &memPasskeys{users: users, m: map[string]passkey.Passkey{}}
	events := newSpySecurityEvents()
	signer := newFakeSigner()
	var offset time.Duration
	clock := func() time.Time { return time.Now().Add(offset) }
	policy, err := web.NewOriginPolicy(web.OriginPolicyConfig{Exact: []string{pkOrigin}})
	if err != nil {
		t.Fatal(err)
	}
	rp, err := webauthnstub.New(webauthn.Config{RPID: pkRPID, RPName: "Example", Origins: policy})
	if err != nil {
		t.Fatal(err)
	}
	rp.Now = clock
	fixture := authenticationFixture{
		Users: users, Identifiers: idents, Passwords: passwords, Sessions: sessions,
		Challenges: challenges, Protector: memProtector{},
		PasswordResets:       &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		CredentialMutations:  &memCredentialMutations{users: users, idents: idents, passwords: passwords, passkeys: passkeys},
		AuthenticationGrants: grants,
		SecurityEvents:       events,
		Hasher:               fakeHasher{},
		Limiter:              ratelimiter.NewMemory(),
		TokenSigner:          signer,
		Clock:                clock,
		Passkeys:             passkeys,
	}
	if !o.unwired {
		fixture.Passkey = authlogic.PasskeyConfig{RelyingParty: rp, MaxPerUser: o.maxPerUser, UserVerification: o.uv}
	}
	if o.adminCheck != nil {
		fixture.UserAdmin = memUserAdmin{}
	}
	if o.machine {
		fixture.ServiceAccounts = &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}}
		fixture.APIKeys = &memAPIKeys{m: map[string]apikey.APIKey{}}
	}
	svc := newServiceWithFakes(fixture)
	deps := mountDeps{Auth: svc, RouteLimits: o.limits, UserAdminCheck: o.adminCheck, AdministrationMFA: o.adminMFA,
		Mutation: MutationSecurity{AllowedOrigins: []string{pkOrigin}, SessionCookieName: svc.SessionCookieName()}}
	if svc.Service.PasskeysEnabled() {
		deps.Passkeys = svc.Service
	}
	if o.machine {
		deps.MachineGate = allowMachineGate
	}
	if o.views != nil {
		deps.Views, deps.PasskeyViews = stubViews{}, o.views
		svc.Adapter.passkeyStepUpPath = passkeyStepUpPathFor(svc.Adapter.browserLoginPath)
	}
	h := web.NewWebHandler()
	mount(h, deps)
	f := &passkeyFixture{h: h, svc: svc, users: users, sessions: sessions, passkeys: passkeys, events: events, signer: signer, rp: rp, offset: &offset, userID: "u-passkey"}
	users.mu.Lock()
	users.byID[f.userID] = user.User{ID: f.userID, DisplayName: "Pat Passkey", Status: user.StatusActive}
	users.mu.Unlock()
	passwords.mu.Lock()
	passwords.m[f.userID] = "hash:" + pkPassword
	passwords.mu.Unlock()
	idents.insert(verifiedEmail("id-primary", f.userID, pkEmail))
	return f
}

// call sends a JSON request. A cookie caller carries the session cookie and the
// double-submit pair; a bearer caller the Authorization header.
type call struct {
	method, path, body string
	cookie             *http.Cookie
	bearer             string
	origin             string
}

func (f *passkeyFixture) do(t *testing.T, c call) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
	if c.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: pkCSRF})
		r.Header.Set(csrfHeaderName, pkCSRF)
	}
	if c.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.origin != "" {
		r.Header.Set("Origin", c.origin)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

func (f *passkeyFixture) login(t *testing.T) *http.Cookie {
	t.Helper()
	rec := f.do(t, call{method: "POST", path: "/auth/login", body: `{"email":"` + pkEmail + `","password":"` + pkPassword + `"}`})
	if rec.Code != http.StatusOK || sessionCookie(rec) == nil {
		t.Fatalf("login = %d %s", rec.Code, rec.Body)
	}
	return sessionCookie(rec)
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return out
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func newPasskeyClient(verifying bool) *webauthntest.Authenticator {
	return webauthntest.NewAuthenticator(webauthntest.AuthenticatorConfig{RPID: pkRPID, Origin: pkOrigin, UserVerifying: verifying})
}

// options calls an options route and returns its ceremony and public_key.
func (f *passkeyFixture) options(t *testing.T, c call) (string, json.RawMessage) {
	t.Helper()
	c.method, c.body = "POST", "{}"
	rec := f.do(t, c)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d %s", c.path, rec.Code, rec.Body)
	}
	var body struct {
		Ceremony  string          `json:"ceremony"`
		PublicKey json.RawMessage `json:"public_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Ceremony == "" || len(body.PublicKey) == 0 {
		t.Fatalf("options body %s: %v", rec.Body, err)
	}
	return body.Ceremony, body.PublicKey
}

func finishBody(t *testing.T, ceremony string, credential []byte, name string) string {
	t.Helper()
	body := map[string]any{"ceremony": ceremony, "credential": json.RawMessage(credential)}
	if name != "" {
		body["name"] = name
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// enroll registers a passkey on a over the HTTP routes.
func (f *passkeyFixture) enroll(t *testing.T, a *webauthntest.Authenticator, cookie *http.Cookie, name string) map[string]any {
	t.Helper()
	ceremony, options := f.options(t, call{path: "/auth/passkeys/registration/options", cookie: cookie})
	response, err := a.Register(options)
	if err != nil {
		t.Fatal(err)
	}
	rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration", body: finishBody(t, ceremony, response, name), cookie: cookie})
	if rec.Code != http.StatusCreated {
		t.Fatalf("registration = %d %s", rec.Code, rec.Body)
	}
	return decode(t, rec)
}

func (f *passkeyFixture) signIn(t *testing.T, a *webauthntest.Authenticator) *httptest.ResponseRecorder {
	t.Helper()
	ceremony, options := f.options(t, call{path: "/auth/passkeys/assertion/options"})
	response, err := a.Assert(options)
	if err != nil {
		t.Fatal(err)
	}
	return f.do(t, call{method: "POST", path: "/auth/passkeys/assertion", body: finishBody(t, ceremony, response, "")})
}

func (f *passkeyFixture) stepUp(t *testing.T, a *webauthntest.Authenticator, c call) *httptest.ResponseRecorder {
	t.Helper()
	c.path = "/auth/step-up/passkey/options"
	ceremony, options := f.options(t, c)
	response, err := a.Assert(options)
	if err != nil {
		t.Fatal(err)
	}
	c.method, c.path, c.body = "POST", "/auth/step-up/passkey", finishBody(t, ceremony, response, "")
	return f.do(t, c)
}

func (f *passkeyFixture) aal2Cookie(t *testing.T, a *webauthntest.Authenticator) *http.Cookie {
	t.Helper()
	rec := f.signIn(t, a)
	if rec.Code != http.StatusOK || sessionCookie(rec) == nil {
		t.Fatalf("passkey sign-in = %d %s", rec.Code, rec.Body)
	}
	return sessionCookie(rec)
}

// TestPasskeyWireGoldens pins the §5 wire contract the script and SPAs share.
func TestPasskeyWireGoldens(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{})
	cookie := f.login(t)
	a := newPasskeyClient(true)
	var bodies []string

	// The one optional request field raises the ceremony to required user
	// verification; the answer keeps the same shape. A later options call
	// replaces this ceremony.
	required := f.do(t, call{method: "POST", path: "/auth/passkeys/registration/options", body: `{"user_verification":"required"}`, cookie: cookie})
	requiredOpts := decode(t, required)
	if required.Code != http.StatusOK || !slices.Equal(keys(requiredOpts), []string{"ceremony", "public_key"}) || creationUserVerification(requiredOpts) != "required" {
		t.Fatalf("registration options with user_verification = %d %v", required.Code, requiredOpts)
	}

	// The creation options necessarily carry the caller's own user handle and
	// excluded credential ids to their authenticator; every other body carries
	// neither.
	rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration/options", body: "{}", cookie: cookie})
	if strings.Contains(rec.Body.String(), `"state"`) {
		t.Errorf("registration options carry ceremony state: %s", rec.Body)
	}
	opts := decode(t, rec)
	if rec.Code != http.StatusOK || !slices.Equal(keys(opts), []string{"ceremony", "public_key"}) {
		t.Fatalf("registration options = %d %v", rec.Code, keys(opts))
	}
	publicKey, _ := opts["public_key"].(map[string]any)
	if publicKey["challenge"] == nil || publicKey["rp"] == nil || publicKey["user"] == nil {
		t.Fatalf("public_key is not the inner creation options: %v", publicKey)
	}
	if creationUserVerification(opts) != "preferred" {
		t.Fatalf("registration options without user_verification = %v", publicKey)
	}
	raw, _ := json.Marshal(publicKey)
	response, err := a.Register(raw)
	if err != nil {
		t.Fatal(err)
	}
	rec = f.do(t, call{method: "POST", path: "/auth/passkeys/registration", body: finishBody(t, opts["ceremony"].(string), response, "Laptop"), cookie: cookie})
	bodies = append(bodies, rec.Body.String())
	item := decode(t, rec)
	if rec.Code != http.StatusCreated || !slices.Equal(keys(item), []string{"backup_eligible", "backup_state", "created_at", "id", "name", "transports", "user_verified"}) || item["name"] != "Laptop" || item["user_verified"] != true {
		t.Fatalf("registration finish = %d %v", rec.Code, item)
	}

	rec = f.signIn(t, a)
	bodies = append(bodies, rec.Body.String())
	login := f.do(t, call{method: "POST", path: "/auth/login", body: `{"email":"` + pkEmail + `","password":"` + pkPassword + `"}`})
	if rec.Code != http.StatusOK || sessionCookie(rec) == nil || refreshCookie(rec) == nil || !slices.Equal(keys(decode(t, rec)), keys(decode(t, login))) {
		t.Fatalf("sign-in finish = %d %s, want exactly the login body %s", rec.Code, rec.Body, login.Body)
	}

	rec = f.stepUp(t, a, call{cookie: sessionCookie(login)})
	bodies = append(bodies, rec.Body.String())
	stepped := decode(t, rec)
	if rec.Code != http.StatusOK || stepped["assurance"] != "aal2" || !slices.Equal(keys(stepped), []string{"assurance", "display_name", "email", "email_verified", "id"}) || sessionCookie(rec) == nil {
		t.Fatalf("cookie step-up = %d %v", rec.Code, stepped)
	}
	bearer := f.do(t, call{method: "POST", path: "/auth/login", body: `{"email":"` + pkEmail + `","password":"` + pkPassword + `"}`})
	rec = f.stepUp(t, a, call{bearer: sessionCookie(bearer).Value})
	bodies = append(bodies, rec.Body.String())
	stepped = decode(t, rec)
	if rec.Code != http.StatusOK || !slices.Equal(keys(stepped), []string{"access_token", "assurance", "display_name", "email", "email_verified", "expires_at", "id", "refresh_token"}) {
		t.Fatalf("bearer step-up = %d %v", rec.Code, stepped)
	}

	rec = f.do(t, call{method: "GET", path: "/auth/passkeys", bearer: stepped["access_token"].(string)})
	bodies = append(bodies, rec.Body.String())
	list := decode(t, rec)
	items, _ := list["items"].([]any)
	if rec.Code != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["last_used_at"] == nil {
		t.Fatalf("list = %d %v", rec.Code, list)
	}

	stored, _ := f.passkeys.ListByUser(context.Background(), f.userID)
	for _, b := range bodies {
		for _, secret := range [][]byte{stored[0].CredentialID, stored[0].PublicKey, stored[0].UserHandle} {
			if strings.Contains(b, base64.RawURLEncoding.EncodeToString(secret)) || strings.Contains(b, base64.StdEncoding.EncodeToString(secret)) {
				t.Errorf("a response carries credential material: %s", b)
			}
		}
		if strings.Contains(b, `"state"`) {
			t.Errorf("a response carries ceremony state: %s", b)
		}
	}
}

// creationUserVerification reads authenticatorSelection.userVerification from
// a decoded options body.
func creationUserVerification(body map[string]any) string {
	publicKey, _ := body["public_key"].(map[string]any)
	selection, _ := publicKey["authenticatorSelection"].(map[string]any)
	uv, _ := selection["userVerification"].(string)
	return uv
}

// TestPasskeyRegistrationOptionsUserVerification pins the options field:
// absent and preferred keep the host's requirement, required raises it,
// preferred never lowers a host's required, any other value or an unknown
// field is 400, and a required ceremony refuses a presence-only response with
// 401 passkey_rejected while a verified one stores an AAL2-capable passkey.
func TestPasskeyRegistrationOptionsUserVerification(t *testing.T) {
	for _, tc := range []struct {
		name string
		host webauthn.Requirement
		body string
		want string
	}{
		{"empty", "", "{}", "preferred"},
		{"preferred", "", `{"user_verification":"preferred"}`, "preferred"},
		{"required", "", `{"user_verification":"required"}`, "required"},
		{"host required, empty", webauthn.Required, "{}", "required"},
		{"host required, preferred does not lower it", webauthn.Required, `{"user_verification":"preferred"}`, "required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPasskeyFixture(t, passkeyOptions{uv: tc.host})
			rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration/options", body: tc.body, cookie: f.login(t)})
			if got := creationUserVerification(decode(t, rec)); rec.Code != http.StatusOK || got != tc.want {
				t.Errorf("%s = %d %q, want %q", tc.body, rec.Code, got, tc.want)
			}
		})
	}

	f := newPasskeyFixture(t, passkeyOptions{})
	cookie := f.login(t)
	for _, body := range []string{`{"user_verification":"discouraged"}`, `{"user_verification":"Required"}`, `{"user_verification":true}`, `{"uv":"required"}`} {
		if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration/options", body: body, cookie: cookie}); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", body, rec.Code, rec.Body)
		}
	}

	presence := newPasskeyClient(false)
	ceremony, options := f.requiredOptions(t, cookie)
	response, err := presence.Register(options)
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration", body: finishBody(t, ceremony, response, ""), cookie: cookie}); rec.Code != http.StatusUnauthorized || decode(t, rec)["code"] != "passkey_rejected" {
		t.Fatalf("presence-only response to a required ceremony = %d %s", rec.Code, rec.Body)
	}
	var failed []securityevent.SecurityEvent
	for _, evt := range f.events.recorded() {
		if evt.EventType == securityevent.TypePasskeyFailed {
			failed = append(failed, evt)
		}
	}
	if len(failed) != 1 || failed[0].Details["reason"] != securityevent.ReasonUVRequired {
		t.Fatalf("passkey_failed rows = %+v", failed)
	}
	presence.SetUserVerified(true)
	ceremony, options = f.requiredOptions(t, cookie)
	if response, err = presence.Register(options); err != nil {
		t.Fatal(err)
	}
	rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration", body: finishBody(t, ceremony, response, ""), cookie: cookie})
	if rec.Code != http.StatusCreated || decode(t, rec)["user_verified"] != true {
		t.Fatalf("verified response to a required ceremony = %d %s", rec.Code, rec.Body)
	}
}

func (f *passkeyFixture) requiredOptions(t *testing.T, cookie *http.Cookie) (string, json.RawMessage) {
	t.Helper()
	rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration/options", body: `{"user_verification":"required"}`, cookie: cookie})
	var body struct {
		Ceremony  string          `json:"ceremony"`
		PublicKey json.RawMessage `json:"public_key"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("required options = %d %s", rec.Code, rec.Body)
	}
	return body.Ceremony, body.PublicKey
}

func TestPasskeyErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{authlogic.ErrPasskeyRejected, http.StatusUnauthorized, "passkey_rejected"},
		{authlogic.ErrPasskeyLimit, http.StatusConflict, "passkey_limit"},
		{authlogic.ErrPasskeyExists, http.StatusConflict, "passkey_exists"},
		{authlogic.ErrChallengeExpired, http.StatusGone, "challenge_expired"},
	} {
		rec := httptest.NewRecorder()
		respondPasskeyError(rec, tc.err)
		if rec.Code != tc.status || decode(t, rec)["code"] != tc.code {
			t.Errorf("%v = %d %s, want %d %s", tc.err, rec.Code, rec.Body, tc.status, tc.code)
		}
	}
	rec := httptest.NewRecorder()
	respondPasskeyError(rec, authlogic.ErrStepUpRequired)
	if rec.Code != http.StatusForbidden {
		t.Errorf("step-up required = %d, want 403", rec.Code)
	}

	f := newPasskeyFixture(t, passkeyOptions{maxPerUser: 1})
	cookie := f.login(t)
	a := newPasskeyClient(false)
	ceremony, options := f.options(t, call{path: "/auth/passkeys/registration/options", cookie: cookie})
	response, _ := a.Register(options)
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration", body: finishBody(t, ceremony, []byte(`{"id":"x"}`), ""), cookie: cookie}); rec.Code != http.StatusUnauthorized || decode(t, rec)["code"] != "passkey_rejected" {
		t.Fatalf("garbage credential = %d %s", rec.Code, rec.Body)
	}
	ceremony, options = f.options(t, call{path: "/auth/passkeys/registration/options", cookie: cookie})
	response, _ = a.Register(options)
	*f.offset = webauthn.MaxTimeout + time.Second
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration", body: finishBody(t, ceremony, response, ""), cookie: cookie}); rec.Code != http.StatusGone || decode(t, rec)["code"] != "challenge_expired" {
		t.Fatalf("expired ceremony = %d %s", rec.Code, rec.Body)
	}
	*f.offset = 0
	f.enroll(t, a, f.login(t), "")
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration/options", body: "{}", cookie: cookie}); rec.Code != http.StatusConflict || decode(t, rec)["code"] != "passkey_limit" {
		t.Fatalf("options at the cap = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/assertion", body: `{"ceremony":"nope","credential":{}}`}); rec.Code != http.StatusUnauthorized || decode(t, rec)["code"] != "passkey_rejected" {
		t.Fatalf("unknown ceremony = %d %s", rec.Code, rec.Body)
	}
}

func TestPasskeyRegistrationGateAnswers403(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{})
	cookie := f.login(t)
	*f.offset = 6 * time.Minute
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/registration/options", body: "{}", cookie: cookie}); rec.Code != http.StatusForbidden {
		t.Fatalf("stale login = %d %s, want 403", rec.Code, rec.Body)
	}
	// The open question ruled YES: a password step-up grant bootstraps the first passkey.
	rec := f.do(t, call{method: "POST", path: "/auth/step-up/password", body: `{"purpose":"register_passkey","password":"` + pkPassword + `"}`, cookie: cookie})
	if rec.Code != http.StatusOK {
		t.Fatalf("password step-up = %d %s", rec.Code, rec.Body)
	}
	f.enroll(t, newPasskeyClient(true), cookie, "")
}

func TestPasskeyRouteTiers(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{limits: PublicRouteLimits{Passkey: ratelimiter.PerMinute(2)}})
	cookie := f.login(t)
	for _, c := range []call{
		{method: "POST", path: "/auth/passkeys/registration/options", body: "{}"},
		{method: "POST", path: "/auth/step-up/passkey/options", body: "{}"},
		{method: "GET", path: "/auth/passkeys"},
		{method: "DELETE", path: "/auth/passkeys/p1"},
	} {
		if rec := f.do(t, c); rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s without a session = %d %q", c.method, c.path, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
	r := httptest.NewRequest("POST", "/auth/passkeys/registration/options", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cookie mutation without the CSRF pair = %d, want 403", rec.Code)
	}
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/assertion/options", body: "{}", origin: "https://evil.example"}); rec.Code != http.StatusForbidden || decode(t, rec)["code"] != originRejectedCode {
		t.Errorf("cross-site sign-in options = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/assertion/options", body: "{}", origin: pkOrigin}); rec.Code != http.StatusOK {
		t.Errorf("same-origin sign-in options = %d", rec.Code)
	}
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/assertion", body: `{"ceremony":"x","credential":{}}`}); rec.Code != http.StatusUnauthorized {
		t.Errorf("second budgeted call = %d, want 401", rec.Code)
	}
	if rec := f.do(t, call{method: "POST", path: "/auth/passkeys/assertion/options", body: "{}"}); rec.Code != http.StatusTooManyRequests {
		t.Errorf("third budgeted call = %d, want 429 (one shared passkey key)", rec.Code)
	}

	off := newPasskeyFixture(t, passkeyOptions{unwired: true})
	if rec := off.do(t, call{method: "POST", path: "/auth/passkeys/assertion/options", body: "{}"}); rec.Code != http.StatusNotFound {
		t.Errorf("unwired passkeys = %d, want 404", rec.Code)
	}
}

func TestPasskeyRemoveRevokesEverySession(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{})
	a := newPasskeyClient(true)
	item := f.enroll(t, a, f.login(t), "")
	other := f.login(t)
	path := "/auth/passkeys/" + item["id"].(string)
	if rec := f.do(t, call{method: "DELETE", path: path, cookie: other}); rec.Code != http.StatusForbidden {
		t.Fatalf("removal at AAL1 = %d %s, want the 403 step-up", rec.Code, rec.Body)
	}
	aal2 := f.aal2Cookie(t, a)
	rec := f.do(t, call{method: "DELETE", path: path, cookie: aal2})
	if rec.Code != http.StatusOK {
		t.Fatalf("removal at AAL2 = %d %s", rec.Code, rec.Body)
	}
	if c := sessionCookie(rec); c == nil || c.MaxAge >= 0 {
		t.Fatalf("removal did not clear the session cookie: %+v", c)
	}
	if rec := f.do(t, call{method: "GET", path: "/auth/passkeys", cookie: other}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("another session after removal = %d, want revoked", rec.Code)
	}
	if rec := f.signIn(t, a); rec.Code != http.StatusUnauthorized {
		t.Fatalf("removed credential sign-in = %d", rec.Code)
	}
	if rows := f.rows(securityevent.TypePasskeyRemoved); len(rows) != 1 {
		t.Fatalf("passkey_removed rows = %+v", rows)
	}
}

func (f *passkeyFixture) rows(eventType string) []securityevent.SecurityEvent {
	var out []securityevent.SecurityEvent
	for _, evt := range f.events.recorded() {
		if evt.EventType == eventType {
			out = append(out, evt)
		}
	}
	return out
}

// TestBrowserRequireMFARedirectsToPasskeyStepUp pins the Browser()+RequireMFA()
// denial: an AAL1 cookie is sent to the passkey step-up page (return_to on GET
// and HEAD only) when the pages are wired, and gets the JSON challenge
// otherwise; an AAL2 cookie passes.
func TestBrowserRequireMFARedirectsToPasskeyStepUp(t *testing.T) {
	gated := func(f *passkeyFixture) http.Handler {
		mw := f.svc.RequirePrincipal(Accept(CredentialAccessToken), Transports(TransportCookie), Browser(), RequireMFA())
		return mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	}
	serve := func(h http.Handler, method, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		r.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	f := newPasskeyFixture(t, passkeyOptions{views: &capturePasskeyViews{}})
	a := newPasskeyClient(true)
	aal1 := f.login(t)
	f.enroll(t, a, aal1, "")
	h := gated(f)
	rec := serve(h, "GET", "/demo/mfa/page?x=1", aal1)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/auth/step-up/passkey?return_to="+url.QueryEscape("/demo/mfa/page?x=1") {
		t.Fatalf("AAL1 GET = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := serve(h, "POST", "/demo/mfa/page", aal1); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/auth/step-up/passkey" {
		t.Fatalf("AAL1 POST = %d %q, want no return_to", rec.Code, rec.Header().Get("Location"))
	}
	if rec := serve(h, "GET", "/demo/mfa/page", f.aal2Cookie(t, a)); rec.Code != http.StatusOK {
		t.Fatalf("AAL2 cookie = %d, want 200", rec.Code)
	}

	plain := newPasskeyFixture(t, passkeyOptions{})
	rec = serve(gated(plain), "GET", "/demo/mfa/page", plain.login(t))
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
		t.Fatalf("without passkey pages = %d %q, want the JSON challenge", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}

	for login, want := range map[string]string{"/auth/login": "/auth/step-up/passkey", "/api/v1/auth/login": "/api/v1/auth/step-up/passkey", "/signin": "/auth/step-up/passkey"} {
		if got := passkeyStepUpPathFor(login); got != want {
			t.Errorf("passkeyStepUpPathFor(%q) = %q, want %q", login, got, want)
		}
	}
}

func TestPasskeyHTMLPages(t *testing.T) {
	views := &capturePasskeyViews{}
	f := newPasskeyFixture(t, passkeyOptions{views: views})
	a := newPasskeyClient(true)
	cookie := f.login(t)
	item := f.enroll(t, a, cookie, "Phone")
	id := item["id"].(string)
	page := func(target string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		r.AddCookie(c)
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, r)
		return rec
	}

	if rec := page("/auth/account/passkeys?return_to=/demo/mfa/page&auth=passkey_added", cookie); rec.Code != http.StatusOK || rec.Body.String() != "PAGE:passkeys" {
		t.Fatalf("manage page = %d %s", rec.Code, rec.Body)
	}
	m := views.manage
	if len(m.Passkeys) != 1 || m.Passkeys[0].Action != "/auth/passkeys/"+url.PathEscape(id) || m.Passkeys[0].Name != "Phone" || m.ReturnTo != "/demo/mfa/page" ||
		m.Links.Manage != passkeysPagePath || m.Links.RegistrationOptions == "" || m.Message != "Your passkey was added." {
		t.Fatalf("manage model = %+v", m)
	}
	if rec := page("/auth/step-up/passkey?return_to=/demo/mfa/page", cookie); rec.Code != http.StatusOK || !views.stepUp.HasPasskeys || !views.stepUp.CanReachAAL2 || views.stepUp.ReturnTo != "/demo/mfa/page" {
		t.Fatalf("step-up page = %d %+v", rec.Code, views.stepUp)
	}

	form := func(values url.Values, c *http.Cookie) *httptest.ResponseRecorder {
		values.Set("csrf_token", pkCSRF)
		r := httptest.NewRequest("POST", "/auth/passkeys/"+id, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", pkOrigin)
		r.AddCookie(c)
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: pkCSRF})
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, r)
		return rec
	}
	if rec := form(url.Values{"action": {"rename"}, "name": {"Work phone"}}, cookie); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != passkeysPagePath+"?auth=passkey_renamed" {
		t.Fatalf("rename = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := form(url.Values{"action": {"remove"}}, f.login(t)); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != passkeyStepUpPagePath+"?return_to="+url.QueryEscape(passkeysPagePath) {
		t.Fatalf("remove at AAL1 = %d %q, want the passkey step-up page", rec.Code, rec.Header().Get("Location"))
	}
	if rec := form(url.Values{"action": {"remove"}}, f.aal2Cookie(t, a)); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != loginDone(outcomePasskeyRemoved) {
		t.Fatalf("remove at AAL2 = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	unwired := newPasskeyFixture(t, passkeyOptions{})
	r := httptest.NewRequest("GET", "/auth/account/passkeys", nil)
	r.AddCookie(unwired.login(t))
	rec := httptest.NewRecorder()
	unwired.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("pages without PasskeyViews = %d, want 404", rec.Code)
	}
}

func TestPasskeyAdminReset(t *testing.T) {
	var actions []UserAdminAction
	check := func(_ context.Context, req UserAdminCheckRequest) error {
		actions = append(actions, req.Action)
		return nil
	}
	f := newPasskeyFixture(t, passkeyOptions{adminCheck: check, adminMFA: true})
	a := newPasskeyClient(true)
	f.enroll(t, a, f.login(t), "")
	path := "/auth/admin/users/" + f.userID + "/passkeys/reset"
	if rec := f.do(t, call{method: "POST", path: path, body: "{}", cookie: f.login(t)}); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
		t.Fatalf("reset at AAL1 = %d %s, want the admins challenge", rec.Code, rec.Body)
	}
	rec := f.do(t, call{method: "POST", path: path, body: "{}", cookie: f.aal2Cookie(t, a)})
	if rec.Code != http.StatusOK || decode(t, rec)["removed"] != float64(1) {
		t.Fatalf("reset at AAL2 = %d %s", rec.Code, rec.Body)
	}
	if !slices.Contains(actions, UserAdminResetPasskeys) {
		t.Fatalf("host check actions = %v", actions)
	}
	if rows := f.rows(securityevent.TypePasskeysReset); len(rows) != 1 || rows[0].Actor.ID != f.userID {
		t.Fatalf("passkeys_reset = %+v", rows)
	}
}

func TestMachineLifecycleUnderAdminsBesidePasskeys(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{machine: true, adminMFA: true, adminCheck: func(context.Context, UserAdminCheckRequest) error { return nil }})
	a := newPasskeyClient(true)
	f.enroll(t, a, f.login(t), "")
	body := `{"name":"bot"}`
	rec := f.do(t, call{method: "POST", path: "/auth/service-accounts", body: body, cookie: f.login(t)})
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != mfaChallenge {
		t.Fatalf("AAL1 machine lifecycle = %d %s", rec.Code, rec.Body)
	}
	rows := f.rows(securityevent.TypeMFARequiredDenied)
	if len(rows) == 0 || rows[len(rows)-1].Details["gate"] != "machine" || rows[len(rows)-1].Details["credential"] != "access_token" {
		t.Fatalf("mfa_required_denied = %+v", rows)
	}
	if rec := f.do(t, call{method: "POST", path: "/auth/service-accounts", body: body, cookie: f.aal2Cookie(t, a)}); rec.Code != http.StatusCreated {
		t.Fatalf("AAL2 machine lifecycle = %d %s", rec.Code, rec.Body)
	}

	without := newPasskeyFixture(t, passkeyOptions{machine: true, adminMFA: true, unwired: true, adminCheck: func(context.Context, UserAdminCheckRequest) error { return nil }})
	if rec := without.do(t, call{method: "POST", path: "/auth/service-accounts", body: body, cookie: without.login(t)}); rec.Code != http.StatusCreated {
		t.Fatalf("admins without passkeys gates the machine lifecycle: %d %s", rec.Code, rec.Body)
	}
}

func TestPasskeyViewsWiring(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{})
	if _, err := New(f.svc.Service, "development", WithPasskeyViews(&capturePasskeyViews{})); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("PasskeyViews without Views = %v, want ErrInvalidInput", err)
	}
	adapter, err := New(f.svc.Service, "development", WithBrowser(BrowserConfig{Views: stubViews{}}), WithPasskeyViews(&capturePasskeyViews{}))
	if err != nil || adapter.passkeyStepUpPath != passkeyStepUpPagePath {
		t.Fatalf("New = %v step-up path %q", err, adapter.passkeyStepUpPath)
	}
	off := newPasskeyFixture(t, passkeyOptions{unwired: true})
	if _, err := New(off.svc.Service, "development", WithBrowser(BrowserConfig{Views: stubViews{}}), WithPasskeyViews(&capturePasskeyViews{})); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("PasskeyViews without passkeys = %v, want ErrInvalidInput", err)
	}
}

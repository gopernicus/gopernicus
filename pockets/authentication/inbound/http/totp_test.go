package authenticationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// memTOTP is the inbound tests' totp.Repository over memUsers. Confirm and
// Remove run under the users lock.
type memTOTP struct {
	users    *memUsers
	passkeys *memPasskeys
	mu       sync.Mutex
	creds    map[string]totp.Credential
	codes    map[string][]totp.RecoveryCode
}

func newMemTOTP(users *memUsers, passkeys *memPasskeys) *memTOTP {
	return &memTOTP{users: users, passkeys: passkeys, creds: map[string]totp.Credential{}, codes: map[string][]totp.RecoveryCode{}}
}

func (f *memTOTP) Begin(_ context.Context, c totp.Credential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ex, ok := f.creds[c.UserID]; ok && ex.Confirmed() {
		return totp.ErrAlreadyEnrolled
	}
	f.creds[c.UserID] = c
	return nil
}

func (f *memTOTP) Get(_ context.Context, userID string) (totp.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[userID]
	if !ok {
		return totp.Credential{}, sdk.ErrNotFound
	}
	return c, nil
}

func (f *memTOTP) Confirm(_ context.Context, in totp.Confirmation) error {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	u, ok := f.users.byID[in.UserID]
	if !ok {
		return sdk.ErrNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[in.UserID]
	switch {
	case !ok:
		return sdk.ErrNotFound
	case c.Confirmed():
		return totp.ErrAlreadyEnrolled
	case u.AuthRevision != in.ExpectedAuthRevision:
		return sdk.ErrConflict
	}
	at := in.ConfirmedAt
	c.ConfirmedAt, c.LastUsedStep, c.SessionID, c.ExpiresAt = &at, in.Step, "", nil
	f.creds[in.UserID] = c
	f.codes[in.UserID] = slices.Clone(in.Codes)
	u.AuthRevision++
	f.users.byID[in.UserID] = u
	return nil
}

func (f *memTOTP) RecordUse(_ context.Context, userID string, expected, step int64, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[userID]
	if !ok || !c.Confirmed() || c.LastUsedStep != expected || step <= expected {
		return sdk.ErrConflict
	}
	c.LastUsedStep, c.LastUsedAt, c.FailedAttempts, c.LockedUntil = step, &at, 0, nil
	f.creds[userID] = c
	return nil
}

func (f *memTOTP) RecordFailure(_ context.Context, userID string, lockAt int, lockFor time.Duration, now time.Time) (totp.Failure, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[userID]
	if !ok {
		return totp.Failure{}, sdk.ErrNotFound
	}
	c.FailedAttempts++
	out := totp.Failure{Attempts: c.FailedAttempts}
	if c.FailedAttempts >= lockAt {
		until := now.Add(lockFor)
		c.FailedAttempts, c.LockedUntil, out.LockedUntil = 0, &until, &until
	}
	f.creds[userID] = c
	return out, nil
}

func (f *memTOTP) ReplaceRecoveryCodes(_ context.Context, userID string, codes []totp.RecoveryCode, expected *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if expected != nil && (len(f.codes[userID]) == 0 || !f.codes[userID][0].GeneratedAt.Equal(*expected)) {
		return sdk.ErrConflict
	}
	f.codes[userID] = slices.Clone(codes)
	return nil
}

func (f *memTOTP) ConsumeRecoveryCode(_ context.Context, userID string, candidates []challenge.DigestCandidate, _ time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, rc := range f.codes[userID] {
		if slices.ContainsFunc(candidates, func(c challenge.DigestCandidate) bool { return c.KeyID == rc.KeyID && c.Digest == rc.Digest }) {
			f.codes[userID] = slices.Delete(slices.Clone(f.codes[userID]), i, i+1)
			return len(f.codes[userID]), nil
		}
	}
	return 0, sdk.ErrNotFound
}

func (f *memTOTP) CountRecoveryCodes(_ context.Context, userID string) (int, *time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.codes[userID]) == 0 {
		return 0, nil, nil
	}
	at := f.codes[userID][0].GeneratedAt
	return len(f.codes[userID]), &at, nil
}

func (f *memTOTP) Remove(ctx context.Context, userID string, expected int64) (totp.Removed, error) {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	u, ok := f.users.byID[userID]
	if !ok {
		return totp.Removed{}, sdk.ErrNotFound
	}
	if u.AuthRevision != expected {
		return totp.Removed{}, sdk.ErrConflict
	}
	f.mu.Lock()
	_, had := f.creds[userID]
	n := len(f.codes[userID])
	delete(f.creds, userID)
	delete(f.codes, userID)
	f.mu.Unlock()
	if !had {
		return totp.Removed{}, nil
	}
	u.AuthRevision++
	f.users.byID[userID] = u
	if f.users.sess != nil {
		_ = f.users.sess.DeleteByUser(ctx, userID)
	}
	return totp.Removed{Credential: true, RecoveryCodes: n}, nil
}

func (f *memTOTP) RecoveryCodeKeyIDs(context.Context) ([]string, error) { return nil, nil }

func testTOTPRing(t *testing.T) *cryptids.AESGCMRing {
	t.Helper()
	ring, err := cryptids.NewAESGCMRing(cryptids.KeyRing{Active: "t1", Keys: map[string][]byte{"t1": []byte(strings.Repeat("t", 32))}})
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

// captureTOTPViews records the TOTP page models.
type captureTOTPViews struct {
	mu      sync.Mutex
	manage  TOTPPage
	enroll  TOTPEnrollPage
	codes   RecoveryCodesPage
	chooser MFAStepUpPage
}

func (v *captureTOTPViews) TOTP(m TOTPPage) web.Renderer {
	v.mu.Lock()
	v.manage = m
	v.mu.Unlock()
	return stubRenderer{"totp"}
}

func (v *captureTOTPViews) TOTPEnroll(m TOTPEnrollPage) web.Renderer {
	v.mu.Lock()
	v.enroll = m
	v.mu.Unlock()
	return stubRenderer{"totp_enroll"}
}

func (v *captureTOTPViews) RecoveryCodes(m RecoveryCodesPage) web.Renderer {
	v.mu.Lock()
	v.codes = m
	v.mu.Unlock()
	return stubRenderer{"recovery_codes"}
}

func (v *captureTOTPViews) MFAStepUp(m MFAStepUpPage) web.Renderer {
	v.mu.Lock()
	v.chooser = m
	v.mu.Unlock()
	return stubRenderer{"mfa_step_up"}
}

// totpCode is the code of secret at the fixture clock plus steps.
func (f *passkeyFixture) totpCode(t *testing.T, secret string, steps int64) string {
	t.Helper()
	raw, err := totp.DecodeSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	return totp.Code(raw, totp.Step(time.Now().Add(*f.offset))+steps)
}

// enrollTOTP enrolls the fixture user from cookie and returns the secret and
// the recovery codes.
func (f *passkeyFixture) enrollTOTP(t *testing.T, cookie *http.Cookie) (string, []string) {
	t.Helper()
	rec := f.do(t, call{method: "POST", path: "/auth/totp/enrollment", body: `{}`, cookie: cookie})
	if rec.Code != http.StatusOK {
		t.Fatalf("enrollment = %d %s", rec.Code, rec.Body)
	}
	secret := decode(t, rec)["secret"].(string)
	rec = f.do(t, call{method: "POST", path: "/auth/totp/enrollment/confirm", body: `{"code":"` + f.totpCode(t, secret, 0) + `"}`, cookie: cookie})
	if rec.Code != http.StatusCreated {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body)
	}
	var codes []string
	for _, c := range decode(t, rec)["recovery_codes"].([]any) {
		codes = append(codes, c.(string))
	}
	return secret, codes
}

// totpStepUp steps cookie up with the next step's code and returns the new
// session cookie.
func (f *passkeyFixture) totpStepUp(t *testing.T, cookie *http.Cookie, secret string) *http.Cookie {
	t.Helper()
	*f.offset += totp.Period
	rec := f.do(t, call{method: "POST", path: "/auth/step-up/totp", body: `{"code":"` + f.totpCode(t, secret, 0) + `"}`, cookie: cookie})
	if rec.Code != http.StatusOK || sessionCookie(rec) == nil {
		t.Fatalf("TOTP step-up = %d %s", rec.Code, rec.Body)
	}
	return sessionCookie(rec)
}

// TestTOTPWireGoldens pins the §6 wire contract SPAs and the goth forms share,
// and that no response but the enrollment carries the secret.
func TestTOTPWireGoldens(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{totp: true})
	cookie := f.login(t)
	var bodies []string

	rec := f.do(t, call{method: "POST", path: "/auth/totp/enrollment", body: `{}`, cookie: cookie})
	enrollment := decode(t, rec)
	if rec.Code != http.StatusOK || !slices.Equal(keys(enrollment), []string{"account", "algorithm", "digits", "expires_at", "issuer", "otpauth_uri", "period", "secret"}) ||
		enrollment["algorithm"] != "SHA1" || enrollment["digits"] != float64(6) || enrollment["period"] != float64(30) || enrollment["issuer"] != "Example" || enrollment["account"] != pkEmail {
		t.Fatalf("enrollment = %d %v", rec.Code, enrollment)
	}
	secret := enrollment["secret"].(string)
	if u, err := url.Parse(enrollment["otpauth_uri"].(string)); err != nil || u.Query().Get("secret") != secret {
		t.Fatalf("otpauth_uri = %v", enrollment["otpauth_uri"])
	}

	rec = f.do(t, call{method: "POST", path: "/auth/totp/enrollment/confirm", body: `{"code":"000000"}`, cookie: cookie})
	if f.totpCode(t, secret, 0) != "000000" && (rec.Code != http.StatusUnauthorized || decode(t, rec)["code"] != "totp_rejected") {
		t.Fatalf("wrong confirm = %d %s", rec.Code, rec.Body)
	}
	bodies = append(bodies, rec.Body.String())
	rec = f.do(t, call{method: "POST", path: "/auth/totp/enrollment/confirm", body: `{"code":"` + f.totpCode(t, secret, 0) + `"}`, cookie: cookie})
	confirmed := decode(t, rec)
	if rec.Code != http.StatusCreated || !slices.Equal(keys(confirmed), []string{"enrolled_at", "generated_at", "recovery_codes"}) || len(confirmed["recovery_codes"].([]any)) != 10 {
		t.Fatalf("confirm = %d %v", rec.Code, confirmed)
	}
	recovery := confirmed["recovery_codes"].([]any)[0].(string)
	generatedAt := confirmed["generated_at"].(string)
	rec = f.do(t, call{method: "POST", path: "/auth/totp/enrollment/confirm", body: `{"code":"` + f.totpCode(t, secret, 0) + `"}`, cookie: cookie})
	if rec.Code != http.StatusConflict || decode(t, rec)["code"] != "totp_exists" {
		t.Fatalf("re-confirm = %d %s", rec.Code, rec.Body)
	}
	bodies = append(bodies, rec.Body.String())
	rec = f.do(t, call{method: "POST", path: "/auth/totp/enrollment", body: `{}`, cookie: cookie})
	if rec.Code != http.StatusConflict || decode(t, rec)["code"] != "totp_exists" {
		t.Fatalf("re-enrollment = %d %s", rec.Code, rec.Body)
	}

	rec = f.do(t, call{method: "GET", path: "/auth/totp", cookie: cookie})
	status := decode(t, rec)
	if rec.Code != http.StatusOK || !slices.Equal(keys(status), []string{"enrolled", "enrolled_at", "pending", "recovery_codes_remaining"}) || status["enrolled"] != true || status["recovery_codes_remaining"] != float64(10) {
		t.Fatalf("status = %d %v", rec.Code, status)
	}
	bodies = append(bodies, rec.Body.String())
	rec = f.do(t, call{method: "GET", path: "/auth/recovery-codes", cookie: cookie})
	if got := decode(t, rec); rec.Code != http.StatusOK || !slices.Equal(keys(got), []string{"generated_at", "remaining"}) || got["generated_at"] != generatedAt {
		t.Fatalf("recovery-codes status = %d %v", rec.Code, got)
	}
	bodies = append(bodies, rec.Body.String())
	rec = f.do(t, call{method: "GET", path: "/auth/methods", cookie: cookie})
	methods := decode(t, rec)
	if methods["assurance"] != "aal1" || !slices.Equal(keys(methods["totp"].(map[string]any)), []string{"enrolled"}) || methods["recovery_codes"].(map[string]any)["remaining"] != float64(10) {
		t.Fatalf("methods = %v", methods)
	}
	bodies = append(bodies, rec.Body.String())

	// An AAL1 session cannot remove the factor or regenerate the codes.
	for _, c := range []call{{method: "DELETE", path: "/auth/totp", cookie: cookie}, {method: "POST", path: "/auth/recovery-codes", body: `{}`, cookie: cookie}} {
		if rec := f.do(t, c); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s at AAL1 = %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}

	*f.offset += totp.Period
	rec = f.do(t, call{method: "POST", path: "/auth/step-up/totp", body: `{"code":"` + f.totpCode(t, secret, 0) + `"}`, cookie: cookie})
	stepped := decode(t, rec)
	if rec.Code != http.StatusOK || stepped["assurance"] != "aal2" || sessionCookie(rec) == nil || stepped["access_token"] != nil {
		t.Fatalf("cookie step-up = %d %v", rec.Code, stepped)
	}
	login := f.do(t, call{method: "POST", path: "/auth/login", body: `{"email":"` + pkEmail + `","password":"` + pkPassword + `"}`})
	want := append(keys(decode(t, login)), "assurance")
	slices.Sort(want)
	if !slices.Equal(keys(stepped), want) {
		t.Fatalf("step-up keys = %v, want the login body plus assurance %v", keys(stepped), want)
	}
	bodies = append(bodies, rec.Body.String())
	aal2 := sessionCookie(rec)
	if claims, err := f.signer.Verify(aal2.Value); err != nil || !slices.Equal(claims["methods"].([]any), []any{string(session.MethodPassword), string(session.MethodTOTP)}) {
		t.Fatalf("methods claim = %v, %v", claims["methods"], err)
	}
	rec = f.do(t, call{method: "POST", path: "/auth/recovery-codes", body: `{"expected_generated_at":"2001-01-01T00:00:00Z"}`, cookie: aal2})
	if rec.Code != http.StatusConflict || decode(t, rec)["code"] != "recovery_codes_stale" {
		t.Fatalf("stale regenerate = %d %s", rec.Code, rec.Body)
	}
	*f.offset += 2 * time.Second
	rec = f.do(t, call{method: "POST", path: "/auth/recovery-codes", body: `{"expected_generated_at":"` + generatedAt + `"}`, cookie: aal2})
	regenerated := decode(t, rec)
	if rec.Code != http.StatusCreated || !slices.Equal(keys(regenerated), []string{"generated_at", "recovery_codes"}) || len(regenerated["recovery_codes"].([]any)) != 10 {
		t.Fatalf("regenerate = %d %v", rec.Code, regenerated)
	}

	// A bearer caller gets the new token pair beside the body.
	bearerLogin := decode(t, f.do(t, call{method: "POST", path: "/auth/token", body: `{"email":"` + pkEmail + `","password":"` + pkPassword + `"}`}))
	rec = f.do(t, call{method: "POST", path: "/auth/step-up/recovery-code", body: `{"code":"` + regenerated["recovery_codes"].([]any)[0].(string) + `"}`, bearer: bearerLogin["access_token"].(string)})
	if got := decode(t, rec); rec.Code != http.StatusOK || got["assurance"] != "aal2" || got["access_token"] == nil || got["refresh_token"] == nil {
		t.Fatalf("bearer recovery step-up = %d %v", rec.Code, got)
	}
	rec = f.do(t, call{method: "POST", path: "/auth/step-up/recovery-code", body: `{"code":"` + recovery + `"}`, cookie: aal2})
	if rec.Code != http.StatusUnauthorized || decode(t, rec)["code"] != "recovery_code_rejected" {
		t.Fatalf("a replaced recovery code = %d %s", rec.Code, rec.Body)
	}
	bodies = append(bodies, rec.Body.String())

	rec = f.do(t, call{method: "DELETE", path: "/auth/totp", cookie: aal2})
	if rec.Code != http.StatusOK || decode(t, rec)["status"] != "removed" || f.sessions.countFor(f.userID) != 0 {
		t.Fatalf("remove = %d %s (sessions %d)", rec.Code, rec.Body, f.sessions.countFor(f.userID))
	}
	for _, body := range bodies {
		if strings.Contains(body, secret) || strings.Contains(body, "hmac|") || strings.Contains(body, "v1.t1.") {
			t.Fatalf("a non-enrollment response carries a secret or digest: %s", body)
		}
	}
}

// TestTOTPLockoutAnswers429WithRetryAfter pins the lockout and budget answers.
func TestTOTPLockoutAnswers429WithRetryAfter(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{totp: true})
	cookie := f.login(t)
	secret, _ := f.enrollTOTP(t, cookie)
	*f.offset += totp.Period
	wrong := "000000"
	for _, s := range []int64{-1, 0, 1} {
		if f.totpCode(t, secret, s) == wrong {
			wrong = "111111"
		}
	}
	var rec *httptest.ResponseRecorder
	for range 5 {
		rec = f.do(t, call{method: "POST", path: "/auth/step-up/totp", body: `{"code":"` + wrong + `"}`, cookie: cookie})
	}
	retry, _ := strconv.Atoi(rec.Header().Get("Retry-After"))
	if rec.Code != http.StatusTooManyRequests || decode(t, rec)["code"] != "too_many_attempts" || retry < 299 || retry > 300 {
		t.Fatalf("fifth wrong code = %d %s Retry-After %q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
	rec = f.do(t, call{method: "POST", path: "/auth/step-up/totp", body: `{"code":"` + f.totpCode(t, secret, 0) + `"}`, cookie: cookie})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a correct code while locked = %d", rec.Code)
	}
	if rows := f.rows(securityevent.TypeTOTPLocked); len(rows) != 1 {
		t.Fatalf("totp_locked rows = %d", len(rows))
	}

	budget := newPasskeyFixture(t, passkeyOptions{totp: true, limitsAuth: authlogic.AuthenticationLimits{SecondFactor: authlogic.RequestBudget{PerSubject: ratelimiter.PerMinute(1), PerIP: ratelimiter.PerMinute(100)}}})
	c := budget.login(t)
	budget.do(t, call{method: "POST", path: "/auth/step-up/totp", body: `{"code":"123456"}`, cookie: c})
	rec = budget.do(t, call{method: "POST", path: "/auth/step-up/totp", body: `{"code":"123456"}`, cookie: c})
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "60" || decode(t, rec)["code"] != "too_many_attempts" {
		t.Fatalf("over budget = %d %s %q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
}

// TestTOTPOptIn pins the host opt-in: without TOTP no TOTP route mounts, the
// inventory carries no totp or recovery_codes member (assurance is the one
// additive field), and the Browser() MFA redirect keeps the passkey page.
func TestTOTPOptIn(t *testing.T) {
	f := newPasskeyFixture(t, passkeyOptions{views: &capturePasskeyViews{}})
	cookie := f.login(t)
	for _, c := range []call{
		{method: "POST", path: "/auth/totp/enrollment", body: `{}`}, {method: "POST", path: "/auth/totp/enrollment/confirm", body: `{"code":"1"}`},
		{method: "GET", path: "/auth/totp"}, {method: "DELETE", path: "/auth/totp"}, {method: "GET", path: "/auth/recovery-codes"},
		{method: "POST", path: "/auth/step-up/totp", body: `{"code":"1"}`}, {method: "POST", path: "/auth/step-up/recovery-code", body: `{"code":"1"}`},
		{method: "GET", path: "/auth/step-up/mfa"}, {method: "GET", path: "/auth/account/totp"},
	} {
		c.cookie = cookie
		if rec := f.do(t, c); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s without TOTP = %d", c.method, c.path, rec.Code)
		}
	}
	methods := decode(t, f.do(t, call{method: "GET", path: "/auth/methods", cookie: cookie}))
	if !slices.Equal(keys(methods), []string{"assurance", "has_password", "identifiers", "oauth", "passkeys"}) {
		t.Fatalf("methods keys = %v", keys(methods))
	}
	if f.svc.Adapter.mfaStepUpPath != passkeyStepUpPagePath {
		t.Fatalf("redirect target = %q", f.svc.Adapter.mfaStepUpPath)
	}

	// An OAuth-only-shaped host: no passkeys, no TOTP. Its inventory gains
	// assurance alone; no second-factor route exists.
	bare := newPasskeyFixture(t, passkeyOptions{unwired: true})
	c := bare.login(t)
	if rec := bare.do(t, call{method: "GET", path: "/auth/totp", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /auth/totp on a bare host = %d", rec.Code)
	}
	if got := keys(decode(t, bare.do(t, call{method: "GET", path: "/auth/methods", cookie: c}))); !slices.Equal(got, []string{"assurance", "has_password", "identifiers", "oauth"}) {
		t.Fatalf("bare methods keys = %v", got)
	}
}

// TestTOTPChooserRedirectAndForms pins the chooser as the Browser()+RequireMFA()
// target with TOTP views, the PRG enrollment that re-reads the same secret, the
// confirm that renders the codes directly, and the step-up form.
func TestTOTPChooserRedirectAndForms(t *testing.T) {
	views := &captureTOTPViews{}
	f := newPasskeyFixture(t, passkeyOptions{totpViews: views})
	cookie := f.login(t)
	gated := f.svc.RequirePrincipal(Accept(CredentialAccessToken), Transports(TransportCookie), Browser(), RequireMFA())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	r := httptest.NewRequest("GET", "/demo/mfa/page", nil)
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/auth/step-up/mfa?return_to="+url.QueryEscape("/demo/mfa/page") {
		t.Fatalf("AAL1 page = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	form := func(path string, values url.Values, c *http.Cookie) *httptest.ResponseRecorder {
		values.Set("csrf_token", pkCSRF)
		r := httptest.NewRequest("POST", path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", pkOrigin)
		r.AddCookie(c)
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: pkCSRF})
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, r)
		return rec
	}
	page := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(c)
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, r)
		return rec
	}
	if rec := page("/auth/step-up/mfa", cookie); rec.Code != http.StatusOK || !views.chooser.OfferSetUpTOTP || views.chooser.OfferTOTP || views.chooser.TOTP.Manage != "/auth/account/totp" {
		t.Fatalf("chooser before enrollment = %d %+v", rec.Code, views.chooser)
	}
	if rec := form("/auth/totp/enrollment", url.Values{}, cookie); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/auth/account/totp/enroll" {
		t.Fatalf("enrollment form = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := page("/auth/account/totp/enroll", cookie); rec.Code != http.StatusOK {
		t.Fatalf("enroll page = %d", rec.Code)
	}
	first := views.enroll
	if len(strings.Fields(first.Secret)) != 8 || !strings.HasPrefix(first.OTPAuthURI, "otpauth://totp/") || first.Links.Confirm != "/auth/totp/enrollment/confirm" {
		t.Fatalf("enroll page model = %+v", first)
	}
	secret := strings.ReplaceAll(first.Secret, " ", "")
	if rec := form("/auth/totp/enrollment/confirm", url.Values{"code": {"12"}}, cookie); rec.Code != http.StatusUnauthorized || views.enroll.Secret != first.Secret || views.enroll.Message == "" {
		t.Fatalf("a wrong confirm re-render = %d %+v", rec.Code, views.enroll)
	}
	if rec := form("/auth/totp/enrollment/confirm", url.Values{"code": {f.totpCode(t, secret, 0)}}, cookie); rec.Code != http.StatusOK || rec.Body.String() != "PAGE:recovery_codes" || len(views.codes.Codes) != 10 || views.codes.Continue != "/auth/account/totp" {
		t.Fatalf("confirm form = %d %s %+v", rec.Code, rec.Body, views.codes)
	}
	if rec := form("/auth/totp/enrollment/confirm", url.Values{"code": {f.totpCode(t, secret, 0)}}, cookie); rec.Code != http.StatusConflict || rec.Body.String() != "PAGE:totp" || views.manage.Message != totpAlreadySetUpMsg {
		t.Fatalf("a re-POSTed confirm = %d %s %q", rec.Code, rec.Body, views.manage.Message)
	}
	if rec := page("/auth/step-up/mfa?return_to=/demo/mfa/page", cookie); rec.Code != http.StatusOK || !views.chooser.OfferTOTP || !views.chooser.OfferRecoveryCode || views.chooser.ReturnTo != "/demo/mfa/page" {
		t.Fatalf("chooser after enrollment = %+v", views.chooser)
	}
	*f.offset += totp.Period
	rec = form("/auth/step-up/totp", url.Values{"code": {f.totpCode(t, secret, 0)}, "return_to": {"/demo/mfa/page"}}, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/demo/mfa/page" || sessionCookie(rec) == nil {
		t.Fatalf("step-up form = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	r = httptest.NewRequest("GET", "/demo/mfa/page", nil)
	r.AddCookie(sessionCookie(rec))
	rec = httptest.NewRecorder()
	gated.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("the AAL2 cookie on the gated page = %d", rec.Code)
	}
}

// countFor counts userID's sessions.
func (s *memSessions) countFor(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, sess := range s.m {
		if sess.UserID == userID {
			n++
		}
	}
	return n
}

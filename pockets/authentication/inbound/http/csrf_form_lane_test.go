package authenticationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// csrfGateDenial is the message only the header double-submit gate writes, so a
// case can tell the gate's 403 apart from a handler's.
const csrfGateDenial = "invalid or missing CSRF token"

const formLaneToken = "form-lane-token"

type gatedRoute struct{ method, path string }

// strictCSRFRoutes is every cookie mutation on the posture fixture's surface whose
// handler does NOT run the body double-submit: the gate must demand the header
// token whatever the body's content type.
var strictCSRFRoutes = []gatedRoute{
	{"PATCH", "/auth/identifiers/id-u1"},
	{"DELETE", "/auth/identifiers/id-u1"},
	{"POST", "/auth/admin/users/u2/deactivate"},
	{"POST", "/auth/admin/users/u2/reactivate"},
	{"POST", "/auth/admin/users/u2/verification/resend"},
	{"POST", "/auth/service-accounts"},
	{"POST", "/auth/service-accounts/sa-x/keys"},
	{"POST", "/auth/api-keys/key-x/revoke"},
	{"POST", "/auth/invitations/project/p1"},
	{"POST", "/auth/invitations/accept"},
	{"POST", "/auth/invitations/sync"},
	{"POST", "/auth/invitations/inv-1/cancel"},
	{"POST", "/auth/invitations/inv-1/resend"},
}

// formLaneRoutes is every posture-fixture route whose form arm runs accountForm
// (formCSRFOK) and therefore opts into requireBrowserSafeFormMutation.
var formLaneRoutes = []gatedRoute{
	{"POST", "/auth/password/change"},
	{"POST", "/auth/password/set"},
	{"POST", "/auth/password/remove/start"},
	{"POST", "/auth/password/remove"},
	{"POST", "/auth/step-up/begin"},
	{"POST", "/auth/step-up/password"},
	{"POST", "/auth/step-up/code"},
	{"POST", "/auth/identifiers/email"},
	{"POST", "/auth/identifiers/email/confirm"},
	{"POST", "/auth/identifiers/phone"},
	{"POST", "/auth/identifiers/phone/confirm"},
	{"POST", "/auth/identifiers/id-u1"},
	{"POST", "/auth/oauth/google/unlink/start"},
	{"POST", "/auth/oauth/google/unlink"},
}

// browserVariant is the origin surface of a cookie request. Both clear
// browserOriginAllowed, so only the token decides.
type browserVariant struct {
	name    string
	headers map[string]string
}

var browserVariants = []browserVariant{
	{"no Origin and no Sec-Fetch-Site", nil},
	{"same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}},
	{"same-origin with Origin", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://app.example.com"}},
}

// gatedRequest builds a cookie (or bearer) request with the given body and
// content type, the __Host-auth_csrf cookie, and optionally the header token.
func gatedRequest(rt gatedRoute, contentType, body string, v browserVariant, sessionCookie *http.Cookie, headerToken bool) *http.Request {
	r := httptest.NewRequest(rt.method, rt.path, strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	for k, val := range v.headers {
		r.Header.Set(k, val)
	}
	if sessionCookie != nil {
		r.AddCookie(sessionCookie)
	}
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: formLaneToken})
	if headerToken {
		r.Header.Set(csrfHeaderName, formLaneToken)
	}
	return r
}

func serveRec(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func isGateCSRFDenial(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), csrfGateDenial)
}

// isHandlerCSRFPage reports the form lane's own refusal: formCSRFOK renders the
// generic error page at 403 (stubViews renders it as PAGE:error).
func isHandlerCSRFPage(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "PAGE:error")
}

func newCSRFPostureFixture(t *testing.T) (*postureFixture, *http.Cookie) {
	t.Helper()
	f := newPostureFixture(t, RouteAuthentication{})
	f.seedAccount("u1", "csrf@example.com")
	return f, f.login(t, "csrf@example.com")
}

// TestStrictCSRFGateRefusesCookieFormBodies is the regression for the form
// deferral: a cookie-authenticated form POST to a route whose handler never runs
// the body double-submit is refused by the gate, even with a matching csrf_token
// field in the body, and even when the request carries no Origin/Sec-Fetch-Site.
func TestStrictCSRFGateRefusesCookieFormBodies(t *testing.T) {
	f, cookie := newCSRFPostureFixture(t)
	bodies := map[string]string{
		"form without token":    "relation=member",
		"form with body token":  "relation=member&csrf_token=" + formLaneToken,
		"multipart-typed empty": "",
	}
	for _, rt := range strictCSRFRoutes {
		for _, v := range browserVariants {
			for name, body := range bodies {
				ct := "application/x-www-form-urlencoded"
				if name == "multipart-typed empty" {
					ct = "multipart/form-data; boundary=x"
				}
				rec := serveRec(f.h, gatedRequest(rt, ct, body, v, cookie, false))
				if !isGateCSRFDenial(rec) {
					t.Errorf("%s %s [%s, %s] = %d %s, want the gate's 403 CSRF denial", rt.method, rt.path, v.name, name, rec.Code, rec.Body)
				}
			}
		}
	}
	if f.inv.createCalled || f.inv.acceptCalled || f.inv.cancelCalled || f.inv.resendCalled {
		t.Fatalf("invitation service ran behind a refused gate: %+v", f.inv)
	}
}

// TestStrictCSRFGateAdmitsHeaderToken proves the strict gate still admits a
// cookie request presenting the header double-submit, whatever the body type.
func TestStrictCSRFGateAdmitsHeaderToken(t *testing.T) {
	f, cookie := newCSRFPostureFixture(t)
	for _, rt := range strictCSRFRoutes {
		for _, v := range browserVariants {
			for _, ct := range []string{"application/x-www-form-urlencoded", "application/json"} {
				rec := serveRec(f.h, gatedRequest(rt, ct, "", v, cookie, true))
				if isGateCSRFDenial(rec) || rec.Code == http.StatusUnauthorized {
					t.Errorf("%s %s [%s, %s] with header token = %d %s, want past the gate", rt.method, rt.path, v.name, ct, rec.Code, rec.Body)
				}
			}
		}
	}
}

// TestStrictCSRFGateBearerUnaffected proves bearer access tokens and API keys
// still skip the browser gate on a form body: they are not a browser CSRF vector.
func TestStrictCSRFGateBearerUnaffected(t *testing.T) {
	f, _ := newCSRFPostureFixture(t)
	access := f.bearerAccessToken(t, "csrf@example.com")
	actAsUser, _ := f.key(t, "helper", true, "u1")
	machine, _ := f.key(t, "bot", false, "")
	cases := []struct {
		name, token string
		rt          gatedRoute
	}{
		{"access token on invitation cancel", access, gatedRoute{"POST", "/auth/invitations/inv-1/cancel"}},
		{"act-as-user key on invitation resend", actAsUser, gatedRoute{"POST", "/auth/invitations/inv-1/resend"}},
		{"service-account key on user deactivate", machine, gatedRoute{"POST", "/auth/admin/users/u2/deactivate"}},
	}
	for _, tc := range cases {
		r := gatedRequest(tc.rt, "application/x-www-form-urlencoded", "x=1", browserVariant{}, nil, false)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		rec := serveRec(f.h, r)
		if isGateCSRFDenial(rec) || rec.Code == http.StatusUnauthorized {
			t.Errorf("%s = %d %s, want past the gate", tc.name, rec.Code, rec.Body)
		}
	}
}

// TestFormLaneRoutesDeferToBodyDoubleSubmit proves the opt-in form lane: a form
// body without csrf_token reaches the handler and is refused by ITS 403, a form
// body with a matching csrf_token is accepted, and a non-form body still needs
// the header token at the gate.
func TestFormLaneRoutesDeferToBodyDoubleSubmit(t *testing.T) {
	f, cookie := newCSRFPostureFixture(t)
	for _, rt := range formLaneRoutes {
		for _, v := range browserVariants {
			missing := serveRec(f.h, gatedRequest(rt, "application/x-www-form-urlencoded", "code=1", v, cookie, false))
			if !isHandlerCSRFPage(missing) {
				t.Errorf("%s %s [%s] form without csrf_token = %d %s, want the handler's 403", rt.method, rt.path, v.name, missing.Code, missing.Body)
			}
			ok := serveRec(f.h, gatedRequest(rt, "application/x-www-form-urlencoded", "code=1&csrf_token="+formLaneToken, v, cookie, false))
			if isHandlerCSRFPage(ok) || isGateCSRFDenial(ok) {
				t.Errorf("%s %s [%s] form with csrf_token = %d %s, want past both CSRF checks", rt.method, rt.path, v.name, ok.Code, ok.Body)
			}
			jsonNoHeader := serveRec(f.h, gatedRequest(rt, "application/json", `{}`, v, cookie, false))
			if !isGateCSRFDenial(jsonNoHeader) {
				t.Errorf("%s %s [%s] JSON without header = %d %s, want the gate's 403", rt.method, rt.path, v.name, jsonNoHeader.Code, jsonNoHeader.Body)
			}
		}
	}
}

// recordingSessions is a SessionManagementService that counts revocations.
type recordingSessions struct{ revokes int }

func (s *recordingSessions) ListUserSessions(context.Context, string, list.Request) (list.Page[session.Session], error) {
	return list.Page[session.Session]{}, nil
}
func (s *recordingSessions) RevokeUserSession(context.Context, string, string) error {
	s.revokes++
	return nil
}
func (s *recordingSessions) RevokeAllUserSessions(context.Context, string) error {
	s.revokes++
	return nil
}

// TestSessionAndConsentFormLane covers the form-lane routes mounted with the
// OAuth2 surface: session revocation and the consent POST.
func TestSessionAndConsentFormLane(t *testing.T) {
	f := newOAuthHTTPFixture(t)
	sessions := &recordingSessions{}
	f.cfg.Sessions = sessions
	router := web.NewWebHandler()
	h := &handlers{svc: f.svc, views: stubViews{}, oauth2: f.cfg, mutation: MutationSecurity{SessionCookieName: f.svc.SessionCookieName()}}
	mountOAuth2(clientInfoRegistrar{inner: router}, h)
	mountSessionManagement(clientInfoRegistrar{inner: router}, h)
	webCookie := &http.Cookie{Name: f.svc.SessionCookieName(), Value: f.accessJWT}

	for _, rt := range []gatedRoute{{"POST", "/auth/sessions/other/revoke"}, {"POST", "/auth/sessions/revoke-all"}, {"POST", "/auth/oauth2/authorize"}} {
		for _, v := range browserVariants {
			missing := serveRec(router, gatedRequest(rt, "application/x-www-form-urlencoded", "decision=allow", v, webCookie, false))
			if missing.Code != http.StatusForbidden || isGateCSRFDenial(missing) {
				t.Errorf("%s [%s] form without csrf_token = %d %s, want the handler's 403", rt.path, v.name, missing.Code, missing.Body)
			}
			jsonNoHeader := serveRec(router, gatedRequest(rt, "application/json", `{}`, v, webCookie, false))
			if !isGateCSRFDenial(jsonNoHeader) {
				t.Errorf("%s [%s] JSON without header = %d %s, want the gate's 403", rt.path, v.name, jsonNoHeader.Code, jsonNoHeader.Body)
			}
		}
	}
	if sessions.revokes != 0 {
		t.Fatalf("revocations behind a refused CSRF check = %d", sessions.revokes)
	}

	for _, rt := range []gatedRoute{{"POST", "/auth/sessions/other/revoke"}, {"POST", "/auth/sessions/revoke-all"}} {
		body := url.Values{"csrf_token": {formLaneToken}}.Encode()
		rec := serveRec(router, gatedRequest(rt, "application/x-www-form-urlencoded", body, browserVariants[1], webCookie, false))
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s form with csrf_token = %d %s, want 303", rt.path, rec.Code, rec.Body)
		}
	}
	if sessions.revokes != 2 {
		t.Fatalf("revocations with a valid body token = %d, want 2", sessions.revokes)
	}
}

// TestBrowserSafeGateLanes pins the two gate variants directly: the strict gate
// never defers a form body, the form gate defers ONLY a form body.
func TestBrowserSafeGateLanes(t *testing.T) {
	cfg := csrfConfig{sessionCookieName: testSessionCookie}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for _, tc := range []struct {
		name     string
		mw       web.Middleware
		ct       string
		bearer   bool
		wantCode int
	}{
		{"strict form", requireBrowserSafeMutation(cfg), "application/x-www-form-urlencoded", false, http.StatusForbidden},
		{"strict multipart", requireBrowserSafeMutation(cfg), "multipart/form-data; boundary=x", false, http.StatusForbidden},
		{"strict json", requireBrowserSafeMutation(cfg), "application/json", false, http.StatusForbidden},
		{"strict bearer form", requireBrowserSafeMutation(cfg), "application/x-www-form-urlencoded", true, http.StatusOK},
		{"form-lane form", requireBrowserSafeFormMutation(cfg), "application/x-www-form-urlencoded", false, http.StatusOK},
		{"form-lane json", requireBrowserSafeFormMutation(cfg), "application/json", false, http.StatusForbidden},
		{"form-lane no content type", requireBrowserSafeFormMutation(cfg), "", false, http.StatusForbidden},
	} {
		r := httptest.NewRequest(http.MethodPost, "/auth/x", strings.NewReader("a=1"))
		if tc.ct != "" {
			r.Header.Set("Content-Type", tc.ct)
		}
		r.AddCookie(&http.Cookie{Name: testSessionCookie, Value: "sess"})
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: formLaneToken})
		if tc.bearer {
			r = httptest.NewRequest(http.MethodPost, "/auth/x", strings.NewReader("a=1"))
			r.Header.Set("Content-Type", tc.ct)
			r.Header.Set("Authorization", "Bearer tok")
		}
		rec := serveRec(tc.mw(ok), r)
		if rec.Code != tc.wantCode {
			t.Errorf("%s = %d, want %d", tc.name, rec.Code, tc.wantCode)
		}
	}
}

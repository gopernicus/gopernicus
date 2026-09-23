package authenticationhttp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func TestBrowserRecoveryEligibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		opts   []PrincipalOption
		bearer bool
		want   bool
	}{
		{"default", nil, false, true},
		{"first-party", []PrincipalOption{FirstParty(), Live()}, false, true},
		{"authoritative-bearer", nil, true, false},
		{"ignored-bearer", []PrincipalOption{Transports(TransportCookie)}, true, true},
		{"header-only", []PrincipalOption{Transports(TransportHeader)}, false, false},
		{"api-key-only", []PrincipalOption{Accept(CredentialAPIKey)}, false, false},
		{"audience-only", []PrincipalOption{Audience("https://api.example.com")}, false, false},
		{"mixed-audience", []PrincipalOption{DelegatedAudience("https://api.example.com")}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bh := newBrowserHarness(t)
			r := httptest.NewRequest(http.MethodGet, "/private?tab=sessions", nil)
			if tc.bearer {
				r.Header.Set("Authorization", "Bearer bad.access.token")
			}
			w := httptest.NewRecorder()
			bh.svc.RequirePrincipal(append(tc.opts, Browser())...)(noContent()).ServeHTTP(w, r)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status=%d", w.Code)
			}
			loc, err := url.Parse(w.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if got := loc.Query().Get("recover") == "1"; got != tc.want {
				t.Fatalf("recovery=%v, want %v: %s", got, tc.want, loc)
			}
			if got := loc.Query().Get("return_to"); got != "/private?tab=sessions" {
				t.Fatalf("return_to=%q", got)
			}
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("browser gate issued cookies before recovery")
			}
		})
	}
}

func TestLoginSessionRecoveryModel(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, query, refresh string
		want                         bool
		returnTo                     string
	}{
		{"expired-access", "", "recover=1&return_to=%2Fprivate%3Fpage%3D2", "opaque-secret", true, "/private?page=2"},
		{"prefixed", "/api/v1", "recover=1&return_to=%2Fprivate", "opaque-secret", true, "/private"},
		{"encoded-prefix", "/team%3Fx", "recover=1&return_to=%2Fprivate", "opaque-secret", true, "/private"},
		{"direct-login", "", "return_to=%2Fprivate", "opaque-secret", false, ""},
		{"no-refresh-cookie", "", "recover=1", "", false, ""},
		{"delegated-refresh", "", "recover=1", session.NewDelegatedRefreshToken(), false, ""},
		{"unsafe-return", "", "recover=1&return_to=https%3A%2F%2Fevil.example", "opaque-secret", true, "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newLoginPostureService(false, false, false)
			views := &loginPostureViews{}
			router := web.NewWebHandler()
			mount(pockets.PrefixRegistrar{Prefix: tc.prefix, Next: router}, mountDeps{Auth: svc, Views: views})
			r := httptest.NewRequest(http.MethodGet, tc.prefix+"/auth/login?"+tc.query, nil)
			if tc.refresh != "" {
				r.AddCookie(&http.Cookie{Name: svc.RefreshCookieName(), Value: tc.refresh})
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusOK || views.calls != 1 {
				t.Fatalf("status=%d calls=%d", w.Code, views.calls)
			}
			m := views.page.SessionRecovery
			if (m != nil) != tc.want {
				t.Fatalf("recovery=%+v, want enabled=%v", m, tc.want)
			}
			if m != nil && *m != (SessionRecovery{
				CheckURL: tc.prefix + "/auth/me", RefreshURL: tc.prefix + "/auth/refresh",
				ReturnTo: tc.returnTo, LockName: "gopernicus:session-refresh",
			}) {
				t.Fatalf("recovery model=%+v", m)
			}
			if w.Header().Get("Cache-Control") != "no-store" || views.page.CSPNonce == "" {
				t.Fatal("recovery lost HTML security policy")
			}
			if sessionCookie(w) != nil || refreshCookie(w) != nil {
				t.Fatal("login GET rotated session credentials")
			}
		})
	}
}

func TestFailedLoginDoesNotRetrySessionRecovery(t *testing.T) {
	svc := newLoginPostureService(false, false, false)
	views := &loginPostureViews{}
	router := web.NewWebHandler()
	mount(router, mountDeps{Auth: svc, Views: views})
	w := doFormReq(t, router, "/auth/login?recover=1", url.Values{"email": {"unknown@example.com"}, "password": {"incorrect-password"}},
		&http.Cookie{Name: svc.RefreshCookieName(), Value: "opaque-secret"})
	if views.calls != 1 || views.page.SessionRecovery != nil || w.Code == http.StatusOK {
		t.Fatalf("failed login status=%d calls=%d recovery=%+v", w.Code, views.calls, views.page.SessionRecovery)
	}
}

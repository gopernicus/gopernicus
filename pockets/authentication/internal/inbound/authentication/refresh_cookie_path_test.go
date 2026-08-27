package authentication

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// Refresh-cookie path transport tests (upstream evidence §2). The resolved refresh
// path is the SINGLE scope used to issue (login, rotation) and delete (logout) the
// refresh cookie, so a host mounting the pocket under a prefix keeps cookie-driven
// refresh alive.

// newRefreshPathHandler mounts the routes over the in-memory fakes with an explicit
// refresh-cookie path (empty → the /auth default) and an optional mount prefix, so a
// test can drive the prefixed-host topology (pocket.PrefixRegistrar) end to end.
func newRefreshPathHandler(t *testing.T, refreshPath, prefix string) http.Handler {
	t.Helper()
	users := newMemUsers()
	identifiers := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:          users,
		Identifiers:    identifiers,
		Passwords:      passwords,
		Sessions:       sessions,
		Challenges:     challenges,
		Protector:      memProtector{},
		PasswordResets: &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		Hasher:         fakeHasher{},
		Deliver:        router,
		Queue:          stubQueue{},
		Limiter:        ratelimiter.NewMemory(),
		Cookie:         authsvc.CookieConfig{RefreshPath: refreshPath},
		TokenSigner:    newFakeSigner(),
	})
	h := web.NewWebHandler()
	var r pocket.RouteRegistrar = h
	if prefix != "" {
		r = pocket.PrefixRegistrar{Prefix: prefix, Next: h}
	}
	Mount(r, Deps{Auth: svc})
	return h
}

// TestRefreshCookiePathIssueRotateDelete proves the resolved path is used at every
// refresh-cookie write: login and rotation ISSUE it and logout DELETES it under the
// same scope. The default stays "/auth"; a prefixed host gets "/api/v1/auth".
func TestRefreshCookiePathIssueRotateDelete(t *testing.T) {
	tests := []struct {
		name        string
		refreshPath string
		prefix      string
		wantPath    string
	}{
		{"default", "", "", "/auth"},
		{"prefixed host", "/api/v1/auth", "/api/v1", "/api/v1/auth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newRefreshPathHandler(t, tt.refreshPath, tt.prefix)
			base := tt.prefix + "/auth"

			do(t, h, "POST", base+"/register", `{"email":"p@example.com","password":"password123456789","display_name":"P"}`)
			login := do(t, h, "POST", base+"/login", `{"email":"p@example.com","password":"password123456789"}`)
			if login.Code != http.StatusOK {
				t.Fatalf("login status = %d, want 200; body=%s", login.Code, login.Body)
			}
			issued := refreshCookie(login)
			if issued == nil {
				t.Fatal("login did not set a refresh cookie")
			}
			if issued.Path != tt.wantPath {
				t.Errorf("login refresh cookie Path = %q, want %q", issued.Path, tt.wantPath)
			}

			rotated := do(t, h, "POST", base+"/refresh", "", &http.Cookie{Name: issued.Name, Value: issued.Value})
			if rotated.Code != http.StatusOK {
				t.Fatalf("refresh status = %d, want 200; body=%s", rotated.Code, rotated.Body)
			}
			rc := refreshCookie(rotated)
			if rc == nil {
				t.Fatal("rotation did not set a refresh cookie")
			}
			if rc.Path != tt.wantPath {
				t.Errorf("rotated refresh cookie Path = %q, want %q", rc.Path, tt.wantPath)
			}

			out := do(t, h, "POST", base+"/logout", "", &http.Cookie{Name: rc.Name, Value: rc.Value})
			if out.Code != http.StatusOK {
				t.Fatalf("logout status = %d, want 200; body=%s", out.Code, out.Body)
			}
			cleared := refreshCookie(out)
			if cleared == nil {
				t.Fatal("logout did not clear the refresh cookie")
			}
			if cleared.Path != tt.wantPath {
				t.Errorf("cleared refresh cookie Path = %q, want %q", cleared.Path, tt.wantPath)
			}
			if cleared.MaxAge >= 0 || cleared.Value != "" {
				t.Errorf("cleared refresh cookie = {value=%q maxAge=%d}, want an expired empty cookie", cleared.Value, cleared.MaxAge)
			}
		})
	}
}

// TestRefreshCookiePathJarMatching is the real cookie-jar proof: a stdlib
// http.CookieJar against a live server mounted under /api/v1 sends the refresh cookie
// to /api/v1/auth/refresh and /api/v1/auth/logout — the endpoints that need it — and to
// nothing else. Without a configured path the jar would scope it to "/auth" and the
// prefixed refresh endpoint would never see it.
func TestRefreshCookiePathJarMatching(t *testing.T) {
	srv := httptest.NewServer(newRefreshPathHandler(t, "/api/v1/auth", "/api/v1"))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	post := func(path, body string) *http.Response {
		t.Helper()
		resp, err := client.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		return resp
	}

	post("/api/v1/auth/register", `{"email":"jar@example.com","password":"password123456789","display_name":"J"}`)
	if resp := post("/api/v1/auth/login", `{"email":"jar@example.com","password":"password123456789"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}

	jarHasRefresh := func(path string) bool {
		t.Helper()
		u, err := url.Parse(srv.URL + path)
		if err != nil {
			t.Fatalf("url.Parse: %v", err)
		}
		for _, c := range jar.Cookies(u) {
			if c.Name == "session_refresh" {
				return true
			}
		}
		return false
	}

	for _, path := range []string{"/api/v1/auth", "/api/v1/auth/refresh", "/api/v1/auth/logout"} {
		if !jarHasRefresh(path) {
			t.Errorf("refresh cookie not sent to %s, want it sent", path)
		}
	}
	for _, path := range []string{"/", "/auth/refresh", "/api/v1", "/api/v1/other", "/api/v1/authentication"} {
		if jarHasRefresh(path) {
			t.Errorf("refresh cookie sent to %s, want it withheld", path)
		}
	}

	// The rotation the jar drives itself: the client carries the cookie to the
	// prefixed refresh endpoint, so cookie-driven refresh works under the prefix.
	if resp := post("/api/v1/auth/refresh", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("cookie-driven refresh status = %d, want 200", resp.StatusCode)
	}
	if !jarHasRefresh("/api/v1/auth/refresh") {
		t.Error("rotated refresh cookie not retained for /api/v1/auth/refresh")
	}

	if resp := post("/api/v1/auth/logout", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", resp.StatusCode)
	}
	if jarHasRefresh("/api/v1/auth/refresh") {
		t.Error("logout left a refresh cookie in the jar; the delete path must match the issue path")
	}
}

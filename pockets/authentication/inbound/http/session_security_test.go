package authenticationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestLoginAndRefreshRejectHostileBrowserOrigin(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("victim", "victim@example.com")
	for _, contentType := range []string{"application/json", ""} {
		for _, site := range []string{"same-site", "cross-site"} {
			rec := serve(f.h, originReq("POST", "/auth/login", `{"email":"victim@example.com","password":"password123456789"}`, contentType, "https://evil.example.com", site))
			if rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
				t.Fatalf("hostile login (%s/%s): %d %s", contentType, site, rec.Code, rec.Body)
			}
		}
	}
	pair, _, err := f.svc.Login(context.Background(), "victim@example.com", "password123456789")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: f.svc.RefreshCookieName(), Value: pair.RefreshToken}
	rec := serve(f.h, originReq("POST", "/auth/refresh", "", "", "https://evil.example.com", "same-site", cookie))
	if rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("hostile refresh: %d %s", rec.Code, rec.Body)
	}
	if _, err := f.svc.Refresh(context.Background(), pair.RefreshToken); err != nil {
		t.Fatalf("rejected request changed refresh state: %v", err)
	}
}

func TestNativeLogoutAcceptsRefreshBody(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("native", "native@example.com")
	pair, _, err := f.svc.Login(context.Background(), "native@example.com", "password123456789")
	if err != nil {
		t.Fatal(err)
	}
	rec := doJSONReq(t, f.h, "/auth/logout", `{"refresh_token":"`+pair.RefreshToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("native refresh-body logout: %d %s", rec.Code, rec.Body)
	}
	if _, err := f.svc.Refresh(context.Background(), pair.RefreshToken); err == nil {
		t.Fatal("native logout retained usable refresh token")
	}
}

type unavailableLogout struct{ authService }

func (unavailableLogout) Logout(context.Context, string, string) error { return sdk.ErrUnavailable }

func TestLogoutHTTPReportsFailureAndClearsCookies(t *testing.T) {
	f := newAccountFormFixture(t)
	h := handlers{svc: unavailableLogout{f.svc}, views: stubViews{}}
	for _, form := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(""))
		w := httptest.NewRecorder()
		if form {
			h.logoutForm(w, r)
		} else {
			h.logoutJSON(w, r)
		}
		if w.Code < 500 || w.Header().Get("Location") != "" {
			t.Fatalf("logout failure reported success (form=%v): %d %s", form, w.Code, w.Body)
		}
		if c := sessionCookie(w); c == nil || c.MaxAge >= 0 {
			t.Fatal("failed logout did not clear access cookie")
		}
		if c := refreshCookie(w); c == nil || c.MaxAge >= 0 {
			t.Fatal("failed logout did not clear refresh cookie")
		}
	}
}

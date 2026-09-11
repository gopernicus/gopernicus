package authenticationhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticationBudgetsReturnHTTP429(t *testing.T) {
	for _, path := range []string{"/auth/password/forgot", "/auth/step-up/begin", "/auth/step-up/password", "/auth/password/remove/start", "/auth/oauth/google/unlink/start"} {
		t.Run(path, func(t *testing.T) {
			f := newAccountFormFixture(t)
			f.seedLoginUser("budget-user", "budget@example.com")
			cookie := f.login(t, "budget@example.com")
			// Exhaust the shared sensitive-code bucket through step-up. Provider
			// or removal starts must then share the denial and HTTP error mapping.
			exhaustPath := path
			if strings.HasSuffix(path, "/start") {
				exhaustPath = "/auth/step-up/begin"
			}
			post := func(target string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"purpose":"host_action","context":"resource","password":"wrong"}`))
				if target == "/auth/password/forgot" {
					r = httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"email":"unknown@example.com"}`))
				}
				if target == "/auth/step-up/begin" {
					r = httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"purpose":"host_action","context":"resource"}`))
				}
				if strings.HasSuffix(target, "/start") {
					r = httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{}`))
				}
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer "+cookie.Value)
				w := httptest.NewRecorder()
				f.h.ServeHTTP(w, r)
				return w
			}
			count := 5
			if path == "/auth/password/forgot" {
				count = 3
			}
			for i := 0; i < count; i++ {
				w := post(exhaustPath)
				if w.Code >= 500 || w.Code == http.StatusTooManyRequests {
					t.Fatalf("unexpected pre-budget response: %d %s", w.Code, w.Body)
				}
			}
			w := post(path)
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("exhausted budget: %d %s", w.Code, w.Body)
			}
		})
	}
}

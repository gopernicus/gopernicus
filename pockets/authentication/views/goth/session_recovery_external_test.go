package goth_test

import (
	"context"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authgoth "github.com/gopernicus/gopernicus/pockets/authentication/views/goth"
)

func TestSessionRecoveryCustomLogin(t *testing.T) {
	var page strings.Builder
	page.WriteString(`<!doctype html><html><body><form action="/tenant/auth/login" method="post"><input name="email"><input name="password" type="password"><button>Sign in</button></form>`)
	m := &inbound.SessionRecovery{
		CheckURL:   "/tenant/auth/me",
		RefreshURL: "/tenant/auth/refresh",
		ReturnTo:   "/private",
		LockName:   "gopernicus:session-refresh",
	}
	if err := authgoth.SessionRecovery(m, "custom-nonce").Render(context.Background(), &page); err != nil {
		t.Fatalf("render custom login recovery: %v", err)
	}
	page.WriteString("</body></html>")
	body := page.String()
	for _, want := range []string{
		`</form><p id="auth-session-recovery" role="status" aria-live="polite" hidden`,
		`nonce="custom-nonce"`, `data-check-url="/tenant/auth/me"`,
		`data-refresh-url="/tenant/auth/refresh"`, `data-return-to="/private"`,
		`data-lock-name="gopernicus:session-refresh"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("custom login missing %q", want)
		}
	}
	if strings.Contains(body, "auth-main") || strings.Contains(body, "data-slot=") {
		t.Fatal("custom login recovery depends on a Goth layout")
	}
}

func TestSessionRecoveryNil(t *testing.T) {
	var body strings.Builder
	if err := authgoth.SessionRecovery(nil, "custom-nonce").Render(context.Background(), &body); err != nil {
		t.Fatalf("render nil recovery: %v", err)
	}
	if body.Len() != 0 {
		t.Fatalf("nil recovery rendered %q", body.String())
	}
}

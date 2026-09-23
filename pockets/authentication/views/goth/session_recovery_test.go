package goth

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
)

func recoveryPage() inbound.LoginPage {
	return inbound.LoginPage{
		PageContext: ctx(),
		SessionRecovery: &inbound.SessionRecovery{
			CheckURL:   "/tenant/auth/me",
			RefreshURL: "/tenant/auth/refresh",
			ReturnTo:   "/dashboard?tab=sessions&sort=recent",
			LockName:   "gopernicus:session-refresh",
		},
	}
}

func TestLoginSessionRecovery(t *testing.T) {
	body := render(t, newViews(t).Login(recoveryPage()))
	mustContain(t, "session recovery", body,
		`id="auth-session-recovery" role="status" aria-live="polite" hidden`,
		`nonce="nonce-xyz789"`, `data-auth-session-recovery`,
		`data-check-url="/tenant/auth/me"`, `data-refresh-url="/tenant/auth/refresh"`,
		`data-return-to="/dashboard?tab=sessions&amp;sort=recent"`,
		`data-lock-name="gopernicus:session-refresh"`,
		`action="/auth/login"`, `name="csrf_token" value="csrf-abc123"`,
		`name="email"`, `name="password"`,
	)
	if strings.Index(body, "data-auth-session-recovery") < strings.Index(body, "</form>") {
		t.Fatal("recovery script runs before the sign-in form is parsed")
	}
	ordinary := render(t, newViews(t).Login(inbound.LoginPage{PageContext: ctx()}))
	mustNotContain(t, "ordinary login", ordinary, `auth-session-recovery`, `navigator.locks`)
}

func TestLoginSessionRecoveryEscapesConfiguration(t *testing.T) {
	m := recoveryPage()
	m.SessionRecovery.ReturnTo = `/dashboard?q="</script><script>alert(1)</script>`
	body := render(t, newViews(t).Login(m))
	mustNotContain(t, "configuration", body, `</script><script>alert(1)</script>`)
	mustContain(t, "configuration", body, `data-return-to="/dashboard?q=&#34;&lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`)
}

func TestSessionRecoveryBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is unavailable; session recovery JavaScript behavior was not exercised")
	}
	m := recoveryPage()
	body := render(t, SessionRecovery(m.SessionRecovery, m.CSPNonce))
	start := strings.Index(body, "data-auth-session-recovery")
	if start < 0 {
		t.Fatal("missing recovery script")
	}
	start += strings.Index(body[start:], ">") + 1
	end := strings.Index(body[start:], "</script>")
	if end < 0 {
		t.Fatal("missing recovery script closing tag")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/session_recovery.mjs")
	cmd.Stdin = strings.NewReader(body[start : start+end])
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("session recovery JavaScript: %v\n%s", err, output)
	}
}

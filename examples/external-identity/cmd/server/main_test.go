package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/examples/external-identity/integrations/authentication/devissuer"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// lockedBuffer is a log sink the server goroutines and the test share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type host struct {
	t   *testing.T
	url string
	log *lockedBuffer
}

// newHost serves the composed handler on a real loopback listener: the
// verifiers fetch the dev issuer's JWKS from this same server.
func newHost(t *testing.T) *host {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	logs := &lockedBuffer{}
	handler, err := newHandler(context.Background(), origin, 0, environment.ModeDevelopment, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	return &host{t: t, url: origin, log: logs}
}

func (h *host) do(method, path, body string, header http.Header) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.url+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func (h *host) token(body string) string {
	h.t.Helper()
	resp, raw := h.do(http.MethodPost, devissuer.TokenPath, body, nil)
	var out struct{ Token string }
	if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(raw), &out) != nil || out.Token == "" {
		h.t.Fatalf("mint: %d %s", resp.StatusCode, raw)
	}
	return out.Token
}

func (h *host) me(header http.Header) (int, profile) {
	h.t.Helper()
	resp, raw := h.do(http.MethodGet, "/api/me", "", header)
	var p profile
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			h.t.Fatalf("me body %s: %v", raw, err)
		}
	}
	return resp.StatusCode, p
}

type profile struct {
	PrincipalID string `json:"principal_id"`
	Addresses   []struct {
		Kind, Value string
	} `json:"addresses"`
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func TestProductionRefusesTheDevIssuer(t *testing.T) {
	handler, err := newHandler(context.Background(), "https://app.example.com", 0, environment.ModeProduction, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if handler != nil || !errors.Is(err, devissuer.ErrNotDevelopment) {
		t.Fatalf("newHandler(production) = %v, %v; want the dev issuer refusal", handler, err)
	}
}

func TestHeaderTokenMapsToOneLocalUser(t *testing.T) {
	h := newHost(t)
	token := h.token(`{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["mfa"]}`)

	status, first := h.me(bearer(token))
	if status != http.StatusOK || first.PrincipalID == "" || first.PrincipalID == "u1" {
		t.Fatalf("first /api/me = %d %+v; want 200 with an internal id", status, first)
	}
	if len(first.Addresses) != 1 || first.Addresses[0].Value != "a@x.test" {
		t.Fatalf("addresses = %+v; want the verified email", first.Addresses)
	}
	status, second := h.me(bearer(h.token(`{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["mfa"]}`)))
	if status != http.StatusOK || second.PrincipalID != first.PrincipalID {
		t.Fatalf("second /api/me = %d %+v; want the same id %s", status, second, first.PrincipalID)
	}
	if n := strings.Count(h.log.String(), "type=external_subject_provisioned"); n != 1 {
		t.Fatalf("provisioning events = %d; want 1\n%s", n, h.log)
	}
}

func TestUnverifiedEmailIsNotProjected(t *testing.T) {
	h := newHost(t)
	status, p := h.me(bearer(h.token(`{"sub":"u2","email":"b@x.test","email_verified":false,"amr":["mfa"]}`)))
	if status != http.StatusOK || p.PrincipalID == "" || len(p.Addresses) != 0 {
		t.Fatalf("/api/me = %d %+v; want 200 with no address", status, p)
	}
}

func TestDenials(t *testing.T) {
	h := newHost(t)
	if status, _ := h.me(nil); status != http.StatusUnauthorized {
		t.Fatalf("no credential: %d; want 401", status)
	}
	unwired := h.token(`{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["mfa"],"iss":"http://127.0.0.1:1/elsewhere"}`)
	if status, _ := h.me(bearer(unwired)); status != http.StatusUnauthorized {
		t.Fatalf("unwired issuer: %d; want 401", status)
	}
	if status, _ := h.me(bearer("not-a-jwt")); status != http.StatusUnauthorized {
		t.Fatalf("dotless bearer on a host with no API keys: %d; want 401", status)
	}
}

// TestSingleFactorTokenIsRefusedBeforeProvisioning proves the providers'
// RequireMFA floor: a token without an MFA claim is refused with the RFC 9470
// challenge, its denial row carries no user, and nobody is provisioned.
func TestSingleFactorTokenIsRefusedBeforeProvisioning(t *testing.T) {
	h := newHost(t)
	for name, header := range map[string]http.Header{
		"header": bearer(h.token(`{"sub":"u9","email":"z@x.test","email_verified":true}`)),
		"pwd":    bearer(h.token(`{"sub":"u9","email":"z@x.test","email_verified":true,"amr":["pwd"]}`)),
	} {
		resp, raw := h.do(http.MethodGet, "/api/me", "", header)
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="insufficient_user_authentication"`) {
			t.Fatalf("%s: /api/me = %d %q %s; want 401 with the RFC 9470 challenge", name, resp.StatusCode, resp.Header.Get("WWW-Authenticate"), raw)
		}
	}
	resp, raw := h.do(http.MethodPost, devissuer.SessionPath, `{"sub":"u9","email":"z@x.test","email_verified":true}`, nil)
	if resp.StatusCode != http.StatusOK || len(resp.Cookies()) != 1 {
		t.Fatalf("session: %d %s", resp.StatusCode, raw)
	}
	cookie := http.Header{"Cookie": {resp.Cookies()[0].Name + "=" + resp.Cookies()[0].Value}}
	if resp, _ := h.do(http.MethodGet, "/api/me", "", cookie); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("single-factor session cookie: %d; want 401", resp.StatusCode)
	}
	logs := h.log.String()
	if n := strings.Count(logs, `msg="memstore: security event" type=mfa_required_denied status=blocked user_id=""`); n != 3 {
		t.Fatalf("mfa_required_denied lines = %d; want 3 with no user\n%s", n, logs)
	}
	if strings.Contains(logs, "type=external_subject_provisioned") {
		t.Fatalf("a refused token provisioned a user\n%s", logs)
	}
}

func TestSessionCookieReachesTheSameUser(t *testing.T) {
	h := newHost(t)
	_, headerProfile := h.me(bearer(h.token(`{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["mfa"]}`)))

	resp, raw := h.do(http.MethodPost, devissuer.SessionPath, `{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["mfa"]}`, nil)
	cookies := resp.Cookies()
	if resp.StatusCode != http.StatusOK || len(cookies) != 1 {
		t.Fatalf("session: %d %s", resp.StatusCode, raw)
	}
	cookie := http.Header{"Cookie": {cookies[0].Name + "=" + cookies[0].Value}}
	status, cookieProfile := h.me(cookie)
	if status != http.StatusOK || cookieProfile.PrincipalID != headerProfile.PrincipalID {
		t.Fatalf("cookie /api/me = %d %+v; want the header user %s", status, cookieProfile, headerProfile.PrincipalID)
	}

	before := strings.Count(h.log.String(), "memstore: local user read")
	resp, raw = h.do(http.MethodPost, "/api/echo", `{"hello":"world"}`, cookie)
	if resp.StatusCode != http.StatusOK || !strings.Contains(raw, `"hello":"world"`) {
		t.Fatalf("cookie POST /api/echo = %d %s", resp.StatusCode, raw)
	}
	if reads := strings.Count(h.log.String(), "memstore: local user read") - before; reads != 1 {
		t.Fatalf("local status reads on one cookie POST = %d; want 1", reads)
	}

	crossSite := http.Header{"Cookie": cookie["Cookie"], "Sec-Fetch-Site": {"cross-site"}, "Origin": {"https://evil.example"}}
	if resp, _ := h.do(http.MethodPost, "/api/echo", `{}`, crossSite); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site cookie POST = %d; want 403", resp.StatusCode)
	}
}

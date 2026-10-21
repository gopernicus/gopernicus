package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/integrations/authentication/devissuer"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	"github.com/gopernicus/gopernicus/pockets"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

var seededKeyPattern = regexp.MustCompile(`api_key=(\S+)`)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type externalDemoHost struct {
	t      *testing.T
	url    string
	apiKey string
}

// newExternalDemoHost composes auth-cms's AUTH_EXTERNAL_DEMO wiring over a real
// loopback listener: the verifier fetches the dev issuer's JWKS from this server.
func newExternalDemoHost(t *testing.T) *externalDemoHost {
	t.Helper()
	ctx := context.Background()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()

	cfg, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.RequireVerifiedEmail = false

	logs := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	external, err := newExternalDemo(ctx, origin, cfg.RuntimeMode, log)
	if err != nil {
		t.Fatalf("newExternalDemo: %v", err)
	}
	components, err := auth.New(authmem.New().Repositories(), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
		append(cfg.options(), auth.WithExternal(external.config))...)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	router := web.NewWebHandler()
	if err := components.HTTP.Register(pockets.Mount{Router: router, Logger: quietLog()}); err != nil {
		t.Fatal(err)
	}
	router.Handle(http.MethodGet, "/demo/whoami", demoWhoami(), components.HTTP.RequireAccessTokenOrAPIKey())
	if err := external.register(ctx, router, components, log); err != nil {
		t.Fatalf("register: %v", err)
	}
	match := seededKeyPattern.FindStringSubmatch(logs.String())
	if match == nil {
		t.Fatalf("no seeded API key in the log:\n%s", logs)
	}

	srv := httptest.NewUnstartedServer(router)
	srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	return &externalDemoHost{t: t, url: origin, apiKey: match[1]}
}

func (h *externalDemoHost) do(method, path, body string, header http.Header) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.url+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
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

func (h *externalDemoHost) devToken(body string) string {
	h.t.Helper()
	resp, raw := h.do(http.MethodPost, devissuer.TokenPath, body, nil)
	var out struct{ Token string }
	if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(raw), &out) != nil {
		h.t.Fatalf("mint: %d %s", resp.StatusCode, raw)
	}
	return out.Token
}

func bearerHeader(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func TestExternalDemoRefusedOutsideDevelopment(t *testing.T) {
	if demo, err := newExternalDemo(context.Background(), "https://app.example.com", environment.ModeProduction, quietLog()); demo != nil || err == nil {
		t.Fatalf("newExternalDemo(production) = %v, %v; want a refusal", demo, err)
	}
}

func TestExternalDemoHybridRoutes(t *testing.T) {
	h := newExternalDemoHost(t)
	plain := h.devToken(`{"sub":"ext-1","email":"ext@example.com","email_verified":true}`)
	mfa := h.devToken(`{"sub":"ext-1","email":"ext@example.com","email_verified":true,"amr":["pwd","mfa"]}`)

	resp, body := h.do(http.MethodGet, "/demo/external/hybrid", "", bearerHeader(plain))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"principal_type":"user"`) {
		t.Fatalf("hybrid with a dev token = %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodGet, "/demo/external/hybrid", "", bearerHeader(h.apiKey))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"principal_type":"service_account"`) {
		t.Fatalf("hybrid with the seeded API key = %d %s", resp.StatusCode, body)
	}

	resp, _ = h.do(http.MethodGet, "/demo/external/mfa", "", bearerHeader(plain))
	if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), `Bearer error="insufficient_user_authentication"`) {
		t.Fatalf("mfa without amr = %d WWW-Authenticate %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if resp, body = h.do(http.MethodGet, "/demo/external/mfa", "", bearerHeader(mfa)); resp.StatusCode != http.StatusOK {
		t.Fatalf("mfa with amr = %d %s", resp.StatusCode, body)
	}

	if resp, body = h.do(http.MethodPost, "/auth/step-up/begin", `{}`, bearerHeader(plain)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dev token on native step-up = %d %s; want 401", resp.StatusCode, body)
	}
}

func TestExternalDemoRefusesANativeSession(t *testing.T) {
	h := newExternalDemoHost(t)
	if resp, body := h.do(http.MethodPost, "/auth/register", `{"email":"native@example.com","password":"correct horse battery staple","display_name":"Native"}`, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	login, body := h.do(http.MethodPost, "/auth/login", `{"email":"native@example.com","password":"correct horse battery staple"}`, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login = %d %s", login.StatusCode, body)
	}
	var cookies []string
	for _, c := range login.Cookies() {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	session := http.Header{"Cookie": {strings.Join(cookies, "; ")}}
	if resp, body := h.do(http.MethodGet, "/demo/external/hybrid", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("hybrid with no credential = %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(http.MethodGet, "/demo/whoami", "", session); resp.StatusCode != http.StatusOK {
		t.Fatalf("native session on /demo/whoami = %d %s; the session itself must work", resp.StatusCode, body)
	}
	if resp, body := h.do(http.MethodGet, "/demo/external/only", "", session); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("native session on RequireExternalToken = %d %s; want 401", resp.StatusCode, body)
	}
}

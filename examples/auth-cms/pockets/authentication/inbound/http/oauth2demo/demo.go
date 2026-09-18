// Package oauth2demo drives real OAuth HTTP requests from the local proof host.
// Its bounded in-memory client vault is intentionally separate from web sessions.
package oauth2demo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk"
)

const prefix = "/oauth-demo"

type IdentityReader interface {
	CurrentCredential(context.Context) (authlogic.Credential, bool)
	CurrentPrincipal(context.Context) (authlogic.Principal, bool)
}

type Config struct {
	BaseURL, ClientID, ConfidentialClientID, ConfidentialSecret string
}

type Demo struct {
	config      Config
	auth        *authhttp.Adapter
	identity    IdentityReader
	client      *http.Client
	mu          sync.Mutex
	connections map[string]connection
}

type connection struct {
	State, Verifier, CSRF string
	Tokens                oauth2.TokenResponse
	ExpiresAt             time.Time
}

type identityResult struct {
	UserID         string `json:"user_id"`
	SessionID      string `json:"session_id"`
	Audience       string `json:"audience"`
	ClientID       string `json:"client_id"`
	OriginClientID string `json:"origin_client_id,omitempty"`
	ActorID        string `json:"actor_id,omitempty"`
}

func New(config Config, authenticator *authhttp.Adapter, identity IdentityReader) (*Demo, error) {
	u, err := url.Parse(config.BaseURL)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.Host != strings.ToLower(u.Host) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(config.BaseURL, "#") {
		return nil, fmt.Errorf("OAuth demo requires a local HTTP origin: %w", sdk.ErrInvalidInput)
	}
	ip, _ := netip.ParseAddr(u.Hostname())
	if (u.Hostname() != "localhost" && !ip.IsLoopback()) || config.ClientID != config.BaseURL+prefix+"/client.json" ||
		config.ConfidentialClientID == "" || config.ConfidentialSecret == "" || authenticator == nil || identity == nil {
		return nil, fmt.Errorf("OAuth demo requires local client wiring: %w", sdk.ErrInvalidInput)
	}
	return &Demo{config: config, auth: authenticator, identity: identity, connections: make(map[string]connection),
		client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (d *Demo) Register(mount pockets.Mount) {
	r := mount.Router
	r.Handle(http.MethodGet, prefix, d.home)
	r.Handle(http.MethodGet, prefix+"/start", d.start)
	r.Handle(http.MethodGet, prefix+"/callback", d.callback)
	r.Handle(http.MethodGet, prefix+"/client.json", d.metadata)
	r.Handle(http.MethodPost, prefix+"/call", d.call)
	r.Handle(http.MethodPost, prefix+"/refresh", d.refresh)
	r.Handle(http.MethodPost, prefix+"/disconnect", d.disconnect)
	// MCP has only its confidential-client secret. Every call validates via
	// authenticated introspection and obtains its API token through exchange.
	r.Handle(http.MethodGet, prefix+"/mcp", d.mcp)
	r.Handle(http.MethodGet, prefix+"/api", d.api, d.auth.RequirePrincipal(authhttp.Audience(d.config.BaseURL+prefix+"/api"), authhttp.Transports(authhttp.TransportHeader)))
}

func (d *Demo) metadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"client_id": d.config.ClientID, "client_name": "Local MCP demo", "redirect_uris": []string{d.config.BaseURL + prefix + "/callback"},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
}

func (d *Demo) start(w http.ResponseWriter, r *http.Request) {
	slot := r.URL.Query().Get("slot")
	if !validSlot(slot) {
		http.Error(w, "Choose connection A or B.", http.StatusBadRequest)
		return
	}
	state, err := randomToken()
	if err != nil {
		http.Error(w, "Connection unavailable.", http.StatusInternalServerError)
		return
	}
	verifier, err := randomToken()
	if err != nil {
		http.Error(w, "Connection unavailable.", http.StatusInternalServerError)
		return
	}
	id, err := randomToken()
	if err != nil {
		http.Error(w, "Connection unavailable.", http.StatusInternalServerError)
		return
	}
	csrf, err := randomToken()
	if err != nil {
		http.Error(w, "Connection unavailable.", http.StatusInternalServerError)
		return
	}
	d.mu.Lock()
	for key, conn := range d.connections {
		if !time.Now().Before(conn.ExpiresAt) {
			delete(d.connections, key)
		}
	}
	if len(d.connections) >= 64 {
		d.mu.Unlock()
		http.Error(w, "Demo connection limit reached.", http.StatusServiceUnavailable)
		return
	}
	// Replacing a local slot does not revoke any prior server grant. The account
	// connection page remains its owner-visible source of truth.
	d.connections[id] = connection{State: state, Verifier: verifier, CSRF: csrf, ExpiresAt: time.Now().Add(10 * time.Minute)}
	d.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName(slot), Value: id, Path: prefix, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 3600})
	digest := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {d.config.ClientID}, "redirect_uri": {d.config.BaseURL + prefix + "/callback"},
		"resource": {d.config.BaseURL + prefix + "/mcp"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}, "state": {state}}
	http.Redirect(w, r, d.config.BaseURL+"/auth/oauth2/authorize?"+query.Encode(), http.StatusSeeOther)
}

func (d *Demo) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	id, conn := "", connection{}
	d.mu.Lock()
	for _, slot := range []string{"a", "b"} {
		cookie, err := r.Cookie(cookieName(slot))
		if err != nil {
			continue
		}
		candidate, ok := d.connections[cookie.Value]
		if ok && candidate.State != "" && time.Now().Before(candidate.ExpiresAt) && subtle.ConstantTimeCompare([]byte(state), []byte(candidate.State)) == 1 {
			id, conn = cookie.Value, candidate
			candidate.State = ""
			d.connections[id] = candidate
			break
		}
	}
	d.mu.Unlock()
	if id == "" {
		http.Error(w, "Authorization response did not match this browser.", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, prefix, http.StatusSeeOther)
		return
	}
	if r.URL.Query().Get("iss") != d.config.BaseURL {
		http.Error(w, "Authorization issuer mismatch.", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Authorization code missing.", http.StatusBadRequest)
		return
	}
	var tokens oauth2.TokenResponse
	err := d.post(r.Context(), "/auth/oauth2/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {d.config.ClientID},
		"redirect_uri": {d.config.BaseURL + prefix + "/callback"}, "resource": {d.config.BaseURL + prefix + "/mcp"}, "code_verifier": {conn.Verifier}}, false, &tokens)
	if err != nil || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		http.Error(w, "Connection could not be completed.", http.StatusBadGateway)
		return
	}
	conn.State, conn.Verifier, conn.Tokens, conn.ExpiresAt = "", "", tokens, time.Now().Add(time.Hour)
	d.mu.Lock()
	d.connections[id] = conn
	d.mu.Unlock()
	http.Redirect(w, r, prefix, http.StatusSeeOther)
}

func (d *Demo) requestConnection(w http.ResponseWriter, r *http.Request) (string, connection, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return "", connection{}, false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != d.config.BaseURL {
		http.Error(w, "Request origin refused.", http.StatusForbidden)
		return "", connection{}, false
	}
	slot := r.PostForm.Get("slot")
	if !validSlot(slot) {
		http.Error(w, "Invalid connection.", http.StatusBadRequest)
		return "", connection{}, false
	}
	cookie, err := r.Cookie(cookieName(slot))
	if err != nil {
		http.Error(w, "Connect this slot first.", http.StatusUnauthorized)
		return "", connection{}, false
	}
	d.mu.Lock()
	conn, ok := d.connections[cookie.Value]
	d.mu.Unlock()
	if !ok || conn.Tokens.AccessToken == "" || !time.Now().Before(conn.ExpiresAt) || subtle.ConstantTimeCompare([]byte(conn.CSRF), []byte(r.PostForm.Get("csrf"))) != 1 {
		http.Error(w, "Connection or request proof expired.", http.StatusForbidden)
		return "", connection{}, false
	}
	return cookie.Value, conn, true
}

func (d *Demo) call(w http.ResponseWriter, r *http.Request) {
	_, conn, ok := d.requestConnection(w, r)
	if !ok {
		return
	}
	var identity identityResult
	if err := d.get(r.Context(), prefix+"/mcp", conn.Tokens.AccessToken, &identity); err != nil {
		d.render(w, r, "This connection was refused. Reconnect if it was revoked or expired.")
		return
	}
	d.render(w, r, fmt.Sprintf("API call succeeded as %s using connection %s.", identity.UserID, identity.SessionID))
}

func (d *Demo) refresh(w http.ResponseWriter, r *http.Request) {
	id, conn, ok := d.requestConnection(w, r)
	if !ok {
		return
	}
	var tokens oauth2.TokenResponse
	if err := d.post(r.Context(), "/auth/oauth2/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {d.config.ClientID},
		"resource": {d.config.BaseURL + prefix + "/mcp"}, "refresh_token": {conn.Tokens.RefreshToken}}, false, &tokens); err != nil {
		d.render(w, r, "Refresh was refused. Reconnect this slot.")
		return
	}
	conn.Tokens = tokens
	d.mu.Lock()
	d.connections[id] = conn
	d.mu.Unlock()
	d.render(w, r, "Connection refreshed. Its session remains the same.")
}

func (d *Demo) disconnect(w http.ResponseWriter, r *http.Request) {
	id, conn, ok := d.requestConnection(w, r)
	if !ok {
		return
	}
	if err := d.post(r.Context(), "/auth/oauth2/revoke", url.Values{"client_id": {d.config.ClientID}, "token": {conn.Tokens.RefreshToken}, "token_type_hint": {"refresh_token"}}, false, nil); err != nil {
		d.render(w, r, "Disconnect could not be completed. Try again.")
		return
	}
	d.mu.Lock()
	delete(d.connections, id)
	d.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName(r.PostForm.Get("slot")), Path: prefix, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	d.render(w, r, "This connection was disconnected. Your web login and other connection remain independent.")
}

func (d *Demo) mcp(w http.ResponseWriter, r *http.Request) {
	raw, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		http.Error(w, "Bearer token required.", http.StatusUnauthorized)
		return
	}
	var active oauth2.Introspection
	if err := d.post(r.Context(), "/auth/oauth2/introspect", url.Values{"token": {raw}}, true, &active); err != nil {
		http.Error(w, "Token validation unavailable.", http.StatusServiceUnavailable)
		return
	}
	if !active.Active || active.Issuer != d.config.BaseURL || active.Audience != d.config.BaseURL+prefix+"/mcp" || active.UserID == "" || active.SessionID == "" {
		http.Error(w, "Connection refused.", http.StatusUnauthorized)
		return
	}
	var exchanged oauth2.TokenResponse
	if err := d.post(r.Context(), "/auth/oauth2/token", url.Values{"grant_type": {oauth2.GrantTokenExchange}, "subject_token": {raw},
		"subject_token_type": {oauth2.AccessTokenType}, "requested_token_type": {oauth2.AccessTokenType}, "resource": {d.config.BaseURL + prefix + "/api"}}, true, &exchanged); err != nil {
		http.Error(w, "Token exchange refused.", http.StatusUnauthorized)
		return
	}
	var identity identityResult
	if err := d.get(r.Context(), prefix+"/api", exchanged.AccessToken, &identity); err != nil {
		http.Error(w, "API refused this connection.", http.StatusUnauthorized)
		return
	}
	if identity.UserID != active.UserID || identity.SessionID != active.SessionID {
		http.Error(w, "Connection identity mismatch.", http.StatusBadGateway)
		return
	}
	writeJSON(w, identity)
}

func (d *Demo) api(w http.ResponseWriter, r *http.Request) {
	credential, ok := d.identity.CurrentCredential(r.Context())
	principal, identified := d.identity.CurrentPrincipal(r.Context())
	if !ok || !identified || principal.Type != "user" || len(credential.Audiences) != 1 {
		http.Error(w, "User connection required.", http.StatusUnauthorized)
		return
	}
	writeJSON(w, identityResult{UserID: principal.ID, SessionID: credential.SessionID, Audience: credential.Audiences[0], ClientID: credential.ClientID,
		OriginClientID: credential.OriginClientID, ActorID: credential.ActorID})
}

func (d *Demo) post(ctx context.Context, path string, values url.Values, confidential bool, target any) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, d.config.BaseURL+path, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if confidential {
		r.SetBasicAuth(d.config.ConfidentialClientID, d.config.ConfidentialSecret)
	}
	return d.send(r, target)
}

func (d *Demo) get(ctx context.Context, path, token string, target any) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, d.config.BaseURL+path, nil)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	return d.send(r, target)
}

func (d *Demo) send(r *http.Request, target any) error {
	response, err := d.client.Do(r)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OAuth demo upstream status %d", response.StatusCode)
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(target)
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validSlot(slot string) bool    { return slot == "a" || slot == "b" }
func cookieName(slot string) string { return "oauth_demo_" + slot }
func bearer(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1], true
	}
	return "", false
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

type slotView struct {
	Key, Label, CSRF string
	Connected        bool
}

var page = template.Must(template.New("oauth-demo").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>MCP connections demo</title></head><body><main><h1>MCP connections demo</h1><p>Each connection has its own session. Disconnecting one leaves your web login and other connections active.</p><p><a href="/auth/account">Manage account sessions</a></p>{{if .Message}}<p role="status">{{.Message}}</p>{{end}}{{range .Slots}}<section><h2>{{.Label}}</h2>{{if .Connected}}<p>Connected</p><form method="post" action="/oauth-demo/call"><input type="hidden" name="slot" value="{{.Key}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Call API through MCP</button></form><form method="post" action="/oauth-demo/refresh"><input type="hidden" name="slot" value="{{.Key}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Refresh connection</button></form><form method="post" action="/oauth-demo/disconnect"><input type="hidden" name="slot" value="{{.Key}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Disconnect {{.Label}}</button></form>{{else}}<p>Disconnected</p><a href="/oauth-demo/start?slot={{.Key}}">Connect {{.Label}}</a>{{end}}</section>{{end}}</main></body></html>`))

func (d *Demo) home(w http.ResponseWriter, r *http.Request) { d.render(w, r, "") }
func (d *Demo) render(w http.ResponseWriter, r *http.Request, message string) {
	data := struct {
		Message string
		Slots   []slotView
	}{Message: message}
	for _, key := range []string{"a", "b"} {
		view := slotView{Key: key, Label: "Connection " + strings.ToUpper(key)}
		if cookie, err := r.Cookie(cookieName(key)); err == nil {
			d.mu.Lock()
			conn, ok := d.connections[cookie.Value]
			d.mu.Unlock()
			if ok && conn.Tokens.AccessToken != "" && time.Now().Before(conn.ExpiresAt) {
				view.Connected, view.CSRF = true, conn.CSRF
			}
		}
		data.Slots = append(data.Slots, view)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = page.Execute(w, data)
}

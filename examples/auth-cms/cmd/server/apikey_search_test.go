package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// crud-search-upstream T4 — the search capability end to end over real HTTP.
//
// Declaring SearchFields on a store proves nothing on its own: the transport has
// to read `q`, the request has to carry it to the store, and the store has to
// apply it. This drives all three through exported host seams.

// apiKeySearchNames are the key names seeded through the real mint endpoint. They
// include the characters a naive `"%" + term + "%"` implementation would treat as
// wildcards.
var apiKeySearchNames = []string{
	"deploy-bot",
	"Deploy-Admin",
	"ci runner",
	"100% coverage",
	"a_c naming",
	"abc naming",
}

// machineHost is newLinkHost's composition plus the authorization engine whose
// platform-admin gate the machine-identity lifecycle routes run behind. The host's
// run() sets auth.Config.MachineRoutesGate from the same coordinate
// (platform/admin on platform:main, declared in authzSchema); with the gate unset the
// five routes are not mounted at all, so every test below would 404.
type machineHost struct {
	*linkHost
	system *authorization.SystemMutator
}

// newMachineHost boots the in_process host with the REAL platform-admin gate on the
// machine-identity routes, and holds the trusted SystemMutator apart so a test can seed
// the tuple that passes it.
func newMachineHost(t *testing.T) *machineHost {
	t.Helper()

	components, err := newAuthorization()
	if err != nil {
		t.Fatalf("newAuthorization: %v", err)
	}
	gate := components.Service.RequirePermissionFixed(platformResourceType, "admin", platformResourceID)

	return &machineHost{
		linkHost: newLinkHostTuned(t, func(cfg *auth.Config) { cfg.MachineRoutesGate = gate }),
		system:   components.SystemMutator,
	}
}

// mutate issues a lifecycle POST the way a cookie-driven admin UI must: the
// double-submit CSRF token from GET /auth/csrf echoed in X-CSRF-Token, on top of
// the client's allowlisted Origin.
func (c *linkClient) mutate(path, body string) (*http.Response, []byte) {
	c.t.Helper()
	return c.do("POST", path, body, http.Header{"X-CSRF-Token": {c.csrfToken()}})
}

// signUpPlatformAdmin onboards a user and grants it platform:main#admin through the
// trusted SystemMutator — the tuple the gate reads. The grant is DATA, not config: the
// same signUp without it produces a user the gate refuses (see
// TestMachineRoutesGateRefusesANonAdmin).
func (h *machineHost) signUpPlatformAdmin(email string) *linkClient {
	h.t.Helper()
	c := h.signUp(email)
	trustGrant(h.t, h.system, platformResourceType, platformResourceID, "admin", subj("user", h.currentUserID(c)))
	return c
}

// currentUserID reads the signed-in user's id from GET /auth/me — the id the gate
// checks the tuple for, and the id an act-as-self service account is owned by.
func (h *machineHost) currentUserID(c *linkClient) string {
	h.t.Helper()
	resp, body := c.do("GET", "/auth/me", "", nil)
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("GET /auth/me = %d; body=%s", resp.StatusCode, body)
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &me); err != nil || me.ID == "" {
		h.t.Fatalf("decode /auth/me %q: %v", body, err)
	}
	return me.ID
}

type apiKeyListResponse struct {
	Items []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"items"`
	Total *int64 `json:"total"`
}

// TestAPIKeySearchThroughHTTP mints keys through the real endpoint, then searches
// them with `?q=` and compares each result set against crud.MatchesSearch — the
// SAME oracle the store-conformance suite and both SQL dialects are pinned to.
func TestAPIKeySearchThroughHTTP(t *testing.T) {
	host := newMachineHost(t)
	c := host.signUpPlatformAdmin("apikey-search@example.com")

	// A service account to own the keys.
	resp, body := c.mutate("/auth/service-accounts",
		`{"name":"search-fixture","description":"","act_as_user":false}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create service account = %d; body=%s", resp.StatusCode, body)
	}
	var sa struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &sa); err != nil || sa.ID == "" {
		t.Fatalf("decode service account %q: %v", body, err)
	}

	for _, name := range apiKeySearchNames {
		payload, err := json.Marshal(map[string]any{"name": name})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if resp, body := c.mutate("/auth/service-accounts/"+sa.ID+"/keys", string(payload)); resp.StatusCode != http.StatusCreated {
			t.Fatalf("mint key %q = %d; body=%s", name, resp.StatusCode, body)
		}
	}

	list := func(t *testing.T, query string) apiKeyListResponse {
		t.Helper()
		path := "/auth/service-accounts/" + sa.ID + "/keys?limit=50" + query
		resp, body := c.do("GET", path, "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d; body=%s", path, resp.StatusCode, body)
		}
		var out apiKeyListResponse
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode %q: %v", body, err)
		}
		return out
	}

	// No search: every key.
	if all := list(t, ""); len(all.Items) != len(apiKeySearchNames) {
		t.Fatalf("unsearched list returned %d keys, want %d", len(all.Items), len(apiKeySearchNames))
	}

	terms := []string{
		"deploy",
		"DEPLOY",
		"runner",
		"100%",
		"%",
		"a_c",
		"_",
		"nothing-here",
		"  deploy  ",
	}

	for _, term := range terms {
		t.Run("q="+term, func(t *testing.T) {
			got := list(t, "&q="+url.QueryEscape(term))

			var want []string
			for _, name := range apiKeySearchNames {
				if crud.MatchesSearch(name, term) {
					want = append(want, name)
				}
			}

			gotNames := make([]string, 0, len(got.Items))
			for _, k := range got.Items {
				gotNames = append(gotNames, k.Name)
			}
			if len(gotNames) != len(want) {
				t.Fatalf("q=%q returned %v, want %v (the crud.MatchesSearch oracle)", term, gotNames, want)
			}
			set := map[string]bool{}
			for _, n := range gotNames {
				set[n] = true
			}
			for _, n := range want {
				if !set[n] {
					t.Errorf("q=%q missed %q; got %v", term, n, gotNames)
				}
			}
		})
	}
}

// TestAPIKeySearchCountReflectsTheTerm is the fan-out trap over HTTP: `count=true`
// must report the SEARCHED total, not the unfiltered one.
func TestAPIKeySearchCountReflectsTheTerm(t *testing.T) {
	host := newMachineHost(t)
	c := host.signUpPlatformAdmin("apikey-search-count@example.com")

	resp, body := c.mutate("/auth/service-accounts",
		`{"name":"count-fixture","description":"","act_as_user":false}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create service account = %d; body=%s", resp.StatusCode, body)
	}
	var sa struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &sa); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, name := range apiKeySearchNames {
		payload, _ := json.Marshal(map[string]any{"name": name})
		if resp, _ := c.mutate("/auth/service-accounts/"+sa.ID+"/keys", string(payload)); resp.StatusCode != http.StatusCreated {
			t.Fatalf("mint key %q failed", name)
		}
	}

	resp, body = c.do("GET", "/auth/service-accounts/"+sa.ID+"/keys?limit=50&count=true&q=deploy", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d; body=%s", resp.StatusCode, body)
	}
	var out apiKeyListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	want := 0
	for _, name := range apiKeySearchNames {
		if crud.MatchesSearch(name, "deploy") {
			want++
		}
	}
	if len(out.Items) != want {
		t.Fatalf("page has %d items, want %d", len(out.Items), want)
	}
	if out.Total == nil || int(*out.Total) != want {
		t.Errorf("total = %v, want the SEARCHED total %d", out.Total, want)
	}
}

// TestAPIKeySearchDoesNotLeakCredentialMaterial pins the SearchFields choice: only
// `name` is searchable. A searchable key prefix would let a caller probe for a key
// by fragment.
func TestAPIKeySearchDoesNotLeakCredentialMaterial(t *testing.T) {
	host := newMachineHost(t)
	c := host.signUpPlatformAdmin("apikey-search-scope@example.com")

	resp, body := c.mutate("/auth/service-accounts",
		`{"name":"scope-fixture","description":"","act_as_user":false}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create service account = %d; body=%s", resp.StatusCode, body)
	}
	var sa struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &sa); err != nil {
		t.Fatalf("decode: %v", err)
	}

	resp, body = c.mutate("/auth/service-accounts/"+sa.ID+"/keys", `{"name":"scoped"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint = %d; body=%s", resp.StatusCode, body)
	}
	var minted struct {
		Key    string `json:"key"`
		Prefix string `json:"key_prefix"`
	}
	if err := json.Unmarshal(body, &minted); err != nil {
		t.Fatalf("decode mint %q: %v", body, err)
	}

	// Searching by the key's own prefix must find nothing: the prefix is not a
	// searchable column.
	probe := minted.Prefix
	if probe == "" && minted.Key != "" {
		probe, _, _ = strings.Cut(minted.Key, "_")
	}
	if probe == "" {
		t.Skip("the mint response exposes neither a prefix nor a key to probe with")
	}

	resp, body = c.do("GET", "/auth/service-accounts/"+sa.ID+"/keys?limit=50&q="+url.QueryEscape(probe), "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d; body=%s", resp.StatusCode, body)
	}
	var out apiKeyListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Items) != 0 {
		t.Errorf("searching by the key prefix %q matched %d row(s); only `name` is searchable", probe, len(out.Items))
	}
}

// TestMachineRoutesGateRefusesANonAdmin is the reason this proof lives in the host and
// not in features/authentication: the feature cannot import features/authorization
// (guard-feature-no-cross-feature), so its own tests gate on a stub. Here the gate is the
// REAL authorization middleware, and the 403 body is the real FS9 one — a signed-up user
// without the platform:main#admin tuple is refused with code `permission_denied`.
func TestMachineRoutesGateRefusesANonAdmin(t *testing.T) {
	host := newMachineHost(t)
	c := host.signUp("apikey-gate-plain@example.com")

	resp, body := c.do("GET", "/auth/service-accounts?limit=10", "", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /auth/service-accounts as a non-admin = %d, want 403; body=%s", resp.StatusCode, body)
	}
	var fs9 struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(body, &fs9); err != nil {
		t.Fatalf("decode 403 body %q: %v", body, err)
	}
	if fs9.Code != "permission_denied" {
		t.Errorf("403 code = %q, want %q; body=%s", fs9.Code, "permission_denied", body)
	}
	// The message pins WHICH 403 this is: the authorization denial, not the
	// browser-safe gate's CSRF/origin refusal, which carries the same status.
	if fs9.Message != "permission denied" {
		t.Errorf("403 message = %q, want %q; body=%s", fs9.Message, "permission denied", body)
	}

	// The same caller cannot create one either — the gate is on every lifecycle
	// route. It sends the CSRF token, so the 403 is the AUTHORIZATION one.
	if resp, body := c.mutate("/auth/service-accounts", `{"name":"nope","act_as_user":false}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /auth/service-accounts as a non-admin = %d, want 403; body=%s", resp.StatusCode, body)
	}
}

// TestMachineRoutesRequireACredential pins the outermost rung of the gate stack:
// RequireUser runs BEFORE the host gate, so an unauthenticated request is 401, never the
// authorization 403 (which would leak that the route exists to anyone).
func TestMachineRoutesRequireACredential(t *testing.T) {
	host := newMachineHost(t)
	c := host.newClient() // a cookie jar that never signed in

	if resp, body := c.do("GET", "/auth/service-accounts?limit=10", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /auth/service-accounts unauthenticated = %d, want 401; body=%s", resp.StatusCode, body)
	}
}

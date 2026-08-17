package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

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
	host := newLinkHost(t)
	c := host.signUp("apikey-search@example.com")

	// A service account to own the keys.
	resp, body := c.do("POST", "/auth/service-accounts",
		`{"name":"search-fixture","description":"","act_as_user":false,"owner_user_id":""}`, nil)
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
		if resp, body := c.do("POST", "/auth/service-accounts/"+sa.ID+"/keys", string(payload), nil); resp.StatusCode != http.StatusCreated {
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
	host := newLinkHost(t)
	c := host.signUp("apikey-search-count@example.com")

	resp, body := c.do("POST", "/auth/service-accounts",
		`{"name":"count-fixture","description":"","act_as_user":false,"owner_user_id":""}`, nil)
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
		if resp, _ := c.do("POST", "/auth/service-accounts/"+sa.ID+"/keys", string(payload), nil); resp.StatusCode != http.StatusCreated {
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
	host := newLinkHost(t)
	c := host.signUp("apikey-search-scope@example.com")

	resp, body := c.do("POST", "/auth/service-accounts",
		`{"name":"scope-fixture","description":"","act_as_user":false,"owner_user_id":""}`, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create service account = %d; body=%s", resp.StatusCode, body)
	}
	var sa struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &sa); err != nil {
		t.Fatalf("decode: %v", err)
	}

	resp, body = c.do("POST", "/auth/service-accounts/"+sa.ID+"/keys", `{"name":"scoped"}`, nil)
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

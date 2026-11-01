package authenticationhttp

import (
	"net/http"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// RefuseActAsUserKeys narrows a set by one fact the credential kind does not
// carry: whether an API key's account resolves to a human owner. These cases
// pin that it refuses exactly that key, opaquely, in every position the set can
// be evaluated from.

// TestRefuseActAsUserKeysAdmission pins the option's admission table on an
// outermost authenticator: the act-as-user key is refused with the generic 401,
// while a self-acting key and a session pass, and the option leaves a set that
// admits no API keys unchanged.
func TestRefuseActAsUserKeysAdmission(t *testing.T) {
	h := newCredentialHarness(t)
	owner := h.seedUser("owner-1")
	actKey, _, _ := h.mintKey(t, "personal", true, owner, time.Time{})
	selfKey, _, selfSAID := h.mintKey(t, "bot", false, "", time.Time{})

	cases := []struct {
		name       string
		mw         web.Middleware
		header     string
		cookie     string
		wantStatus int
	}{
		{"act-as-user key refused", h.svc.RequirePrincipal(RefuseActAsUserKeys()), actKey, "", http.StatusUnauthorized},
		{"self-acting key admitted", h.svc.RequirePrincipal(RefuseActAsUserKeys()), selfKey, "", http.StatusOK},
		{"session cookie admitted", h.svc.RequirePrincipal(RefuseActAsUserKeys()), "", h.accessJWT, http.StatusOK},
		{"bearer access token admitted", h.svc.RequirePrincipal(RefuseActAsUserKeys()), h.accessJWT, "", http.StatusOK},
		// The header is authoritative: a refused key beside a valid cookie never
		// falls back to the cookie.
		{"act-as-user key beside a valid cookie refused", h.svc.RequirePrincipal(RefuseActAsUserKeys()), actKey, h.accessJWT, http.StatusUnauthorized},
		{"Accept then refuse refuses", h.svc.RequirePrincipal(Accept(CredentialAPIKey), RefuseActAsUserKeys()), actKey, "", http.StatusUnauthorized},
		{"refuse then Accept refuses", h.svc.RequirePrincipal(RefuseActAsUserKeys(), Accept(CredentialAPIKey)), actKey, "", http.StatusUnauthorized},
		{"refuse then Accept admits a self-acting key", h.svc.RequirePrincipal(RefuseActAsUserKeys(), Accept(CredentialAPIKey)), selfKey, "", http.StatusOK},
		{"inert under FirstParty: session admitted", h.svc.RequirePrincipal(FirstParty(), RefuseActAsUserKeys()), "", h.accessJWT, http.StatusOK},
		{"inert under FirstParty: self-acting key still refused", h.svc.RequirePrincipal(FirstParty(), RefuseActAsUserKeys()), selfKey, "", http.StatusUnauthorized},
		{"inert under Accept(access_token): session admitted", h.svc.RequirePrincipal(Accept(CredentialAccessToken), RefuseActAsUserKeys()), h.accessJWT, "", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, got := serveGuarded(h, tc.mw, h.credentialRequest(tc.header, tc.cookie))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantStatus != http.StatusOK && got.reached {
				t.Error("a refused credential reached the handler")
			}
		})
	}

	_, got := serveGuarded(h, h.svc.RequirePrincipal(RefuseActAsUserKeys()), h.credentialRequest(selfKey, ""))
	if got.principal.Type != authlogic.PrincipalServiceAccount || got.principal.ID != selfSAID {
		t.Errorf("self-acting principal = %+v, want {service_account, %s}", got.principal, selfSAID)
	}
}

// TestRefuseActAsUserKeysDenialIsGeneric proves the refusal is the same opaque
// 401 as a kind outside the set: a client cannot tell an act-as-user refusal
// from any other.
func TestRefuseActAsUserKeysDenialIsGeneric(t *testing.T) {
	h := newCredentialHarness(t)
	owner := h.seedUser("owner-1")
	actKey, _, _ := h.mintKey(t, "personal", true, owner, time.Time{})
	selfKey, _, _ := h.mintKey(t, "bot", false, "", time.Time{})

	refused, _ := serveGuarded(h, h.svc.RequirePrincipal(RefuseActAsUserKeys()), h.credentialRequest(actKey, ""))
	outside, _ := serveGuarded(h, h.svc.RequireAccessToken(), h.credentialRequest(selfKey, ""))
	if refused.Code != http.StatusUnauthorized || outside.Code != http.StatusUnauthorized {
		t.Fatalf("statuses = %d / %d, want 401 / 401", refused.Code, outside.Code)
	}
	if refused.Body.String() != outside.Body.String() {
		t.Errorf("refusal body %q differs from the kind-outside-the-set body %q", refused.Body, outside.Body)
	}
}

// TestRefuseActAsUserKeysNestedNarrowing pins the option on a NESTED
// authenticator: an outer default set admits the act-as-user key, and the inner
// refuses it off the stash without re-resolving.
func TestRefuseActAsUserKeysNestedNarrowing(t *testing.T) {
	h := newCredentialHarness(t)
	owner := h.seedUser("owner-1")
	actKey, _, _ := h.mintKey(t, "personal", true, owner, time.Time{})
	selfKey, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	chain := func(next http.Handler) http.Handler {
		return h.svc.RequirePrincipal()(h.svc.RequirePrincipal(RefuseActAsUserKeys())(next))
	}

	before := h.keyLookups()
	rec, got := serveGuarded(h, chain, h.credentialRequest(actKey, ""))
	if rec.Code != http.StatusUnauthorized || got.reached {
		t.Errorf("act-as-user key under a refusing inner = %d reached=%v, want 401 not-reached", rec.Code, got.reached)
	}
	if lookups := h.keyLookups() - before; lookups != 1 {
		t.Errorf("api-key repository resolutions = %d, want 1 (the inner narrows off the stash)", lookups)
	}
	if rec, _ := serveGuarded(h, chain, h.credentialRequest(selfKey, "")); rec.Code != http.StatusOK {
		t.Errorf("self-acting key under a refusing inner = %d, want 200", rec.Code)
	}
}

// TestRefuseActAsUserKeysUnderOptional proves Optional() never launders a
// presented act-as-user key into anonymity: it is a credential outside the set,
// so it denies.
func TestRefuseActAsUserKeysUnderOptional(t *testing.T) {
	h := newCredentialHarness(t)
	owner := h.seedUser("owner-1")
	actKey, _, _ := h.mintKey(t, "personal", true, owner, time.Time{})

	rec, got := serveOptional(h.svc.RequirePrincipal(Optional(), RefuseActAsUserKeys()), h.credentialRequest(actKey, ""), h)
	if rec.Code != http.StatusUnauthorized || got.reached {
		t.Errorf("act-as-user key under Optional() = %d reached=%v, want 401 not-reached", rec.Code, got.reached)
	}
	rec, got = serveOptional(h.svc.RequirePrincipal(Optional(), RefuseActAsUserKeys()), h.credentialRequest("", ""), h)
	if rec.Code != http.StatusOK || !got.reached || got.principalOK {
		t.Errorf("anonymous under Optional() = %d reached=%v principal=%v, want 200 reached anonymous", rec.Code, got.reached, got.principalOK)
	}
}

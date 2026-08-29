package authsvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// The credential × transport × option matrix. RequirePrincipal is the ONE
// authenticator, so this file is where the resolution rules are pinned: the
// header is authoritative and never falls back to the cookie, a credential on an
// un-admitted transport is IGNORED (not denied), a kind outside the set denies, a
// nested authenticator narrows off the stash instead of re-resolving, and Live()
// pays exactly one session lookup per request and fails closed.

// --- harness ---

// credentialHarness wires the full user rail plus the machine rail and keeps the
// handles a matrix row needs: the session store (liveness), the signer (crafting
// an expired or wrongly-signed JWT), the key repositories (call counts) and the
// audit spy (the apikey_auth branches).
type credentialHarness struct {
	svc    *Service
	users  *fakeUsers
	sess   *fakeSessions
	signer *fakeSigner
	sas    *fakeServiceAccounts
	keys   *fakeAPIKeys
	events *spySecurityEvents

	// The registered human and the live session its access JWT is backed by.
	userID    string
	sessionID string
	accessJWT string
}

const credentialPassword = "password123456789"

func newCredentialHarness(t *testing.T) *credentialHarness {
	t.Helper()
	users := newFakeUsers()
	sess := newFakeSessions()
	signer := newFakeSigner()
	sas := newFakeServiceAccounts()
	keys := newFakeAPIKeys()
	events := newSpySecurityEvents()
	svc := NewService(Deps{
		Users:           users,
		Identifiers:     newFakeIdentifiers(users),
		Passwords:       newFakePasswords(),
		Sessions:        sess,
		Challenges:      newFakeChallenges(),
		Protector:       newFakeProtector("k1", "k1"),
		Hasher:          &fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		Cookie:          CookieConfig{},
		ServiceAccounts: sas,
		APIKeys:         keys,
		SecurityEvents:  events,
		TokenSigner:     signer,
	})
	wireSyncDelivery(t, svc, &recordingMailer{}, nil)
	h := &credentialHarness{svc: svc, users: users, sess: sess, signer: signer, sas: sas, keys: keys, events: events}

	ctx := context.Background()
	u, err := svc.Register(ctx, "matrix@example.com", credentialPassword, "Matrix")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	pair, _, err := svc.Login(ctx, "matrix@example.com", credentialPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	h.userID = u.ID
	h.accessJWT = pair.AccessToken
	sess.mu.Lock()
	for id := range sess.m {
		h.sessionID = id
	}
	sess.mu.Unlock()
	if h.sessionID == "" {
		t.Fatal("login stored no session")
	}
	return h
}

// seedUser puts a bare active user in the directory so an act-as-user account has
// a real owner (CreateServiceAccount refuses an unknown one).
func (h *credentialHarness) seedUser(id string) string {
	h.users.mu.Lock()
	defer h.users.mu.Unlock()
	h.users.byID[id] = user.User{ID: id}
	return id
}

// mintKey creates a service account and one key on it, returning the plaintext
// key alongside the coordinates a Credential must name.
func (h *credentialHarness) mintKey(t *testing.T, name string, actAsUser bool, ownerUserID string, expiresAt time.Time) (raw, keyID, serviceAccountID string) {
	t.Helper()
	ctx := context.Background()
	sa, err := h.svc.CreateServiceAccount(ctx, "admin", name, "", actAsUser, ownerUserID)
	if err != nil {
		t.Fatalf("CreateServiceAccount(%s): %v", name, err)
	}
	key, raw, err := h.svc.MintAPIKey(ctx, sa.ID, name, expiresAt)
	if err != nil {
		t.Fatalf("MintAPIKey(%s): %v", name, err)
	}
	return raw, key.ID, sa.ID
}

// expiredAccessJWT signs a well-formed access JWT that expired an hour ago.
func (h *credentialHarness) expiredAccessJWT(t *testing.T) string {
	t.Helper()
	tok, err := h.signer.Sign(map[string]any{tokenClaimUserID: h.userID, tokenClaimSessionID: h.sessionID}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Sign(expired): %v", err)
	}
	return tok
}

// badSignatureAccessJWT signs an unexpired, correctly shaped access JWT under a
// DIFFERENT secret, so only the signature check can reject it.
func (h *credentialHarness) badSignatureAccessJWT(t *testing.T) string {
	t.Helper()
	other := &fakeSigner{secret: []byte("a-different-secret-entirely"), now: time.Now}
	tok, err := other.Sign(map[string]any{tokenClaimUserID: h.userID, tokenClaimSessionID: h.sessionID}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign(bad signature): %v", err)
	}
	return tok
}

// credentialRequest builds the GET a row presents: an optional bearer header and
// an optional access cookie, in whatever combination the transport axis needs.
func (h *credentialHarness) credentialRequest(header, cookie string) *http.Request {
	r := httptest.NewRequest("GET", "/guarded", nil)
	if header != "" {
		r.Header.Set("Authorization", "Bearer "+header)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: cookie})
	}
	return r
}

// observed is what a passing request saw on its context.
type observed struct {
	reached   bool
	principal Principal
	cred      Credential
	sessionID string
	sessionOK bool
}

// serveGuarded runs one request through mw and captures the stash.
func serveGuarded(h *credentialHarness, mw web.Middleware, r *http.Request) (*httptest.ResponseRecorder, observed) {
	var got observed
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.reached = true
		got.principal, _ = h.svc.CurrentPrincipal(r.Context())
		got.cred, _ = h.svc.CurrentCredential(r.Context())
		got.sessionID, got.sessionOK = h.svc.CurrentSessionID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, r)
	return rec, got
}

// --- the matrix ---

// credentialWant is a row's expected outcome. For an admitted request the kind
// and transport are literal; the principal and the credential's coordinates are
// named symbolically ("self" / "act") because the ids are minted at runtime.
type credentialWant struct {
	status    int
	kind      CredentialKind
	transport Transport
	// key names which minted key the Credential must describe: "" for an access
	// token, "self" for the self-acting account's key, "act" for the act-as-user
	// account's key.
	key string
	// liveSession asserts CurrentSessionID carries the proven session id (a Live()
	// access-token row); every other row asserts it is absent.
	liveSession bool
}

type credentialCase struct {
	name   string
	header string // the Authorization bearer value ("" → no header)
	cookie string // the access cookie value ("" → no cookie)
	opts   []PrincipalOption
	want   credentialWant
}

func TestRequirePrincipalCredentialMatrix(t *testing.T) {
	h := newCredentialHarness(t)
	owner := h.seedUser("owner-1")
	selfKey, selfKeyID, selfSAID := h.mintKey(t, "bot", false, "", time.Time{})
	actKey, actKeyID, actSAID := h.mintKey(t, "personal", true, owner, time.Time{})
	revokedKey, revokedKeyID, _ := h.mintKey(t, "revoked", false, "", time.Time{})
	if err := h.svc.RevokeAPIKey(context.Background(), revokedKeyID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	expiredKey, _, _ := h.mintKey(t, "stale", false, "", time.Now().Add(-time.Hour))
	expiredJWT := h.expiredAccessJWT(t)
	badSigJWT := h.badSignatureAccessJWT(t)
	const (
		garbageJWT = "aaa.bbb.ccc"
		unknownKey = "prefix00_deadbeefdeadbeef"
	)

	// keyIDs / accountIDs resolve a row's symbolic key name to the minted ids.
	keyIDs := map[string]string{"self": selfKeyID, "act": actKeyID}
	accountIDs := map[string]string{"self": selfSAID, "act": actSAID}
	actAs := map[string]bool{"self": false, "act": true}
	// principals resolves the effective caller a row expects.
	principals := map[string]Principal{
		"":     {Type: PrincipalUser, ID: h.userID},
		"self": {Type: PrincipalServiceAccount, ID: selfSAID},
		"act":  {Type: PrincipalUser, ID: owner},
	}

	admitted := func(kind CredentialKind, transport Transport, key string) credentialWant {
		return credentialWant{status: http.StatusOK, kind: kind, transport: transport, key: key}
	}
	denied := credentialWant{status: http.StatusUnauthorized}

	cases := []credentialCase{
		// --- the default set: every wired credential, both transports, stateless ---
		{"default/no credential", "", "", nil, denied},
		{"default/access token by header", h.accessJWT, "", nil, admitted(CredentialAccessToken, TransportHeader, "")},
		{"default/access token by cookie", "", h.accessJWT, nil, admitted(CredentialAccessToken, TransportCookie, "")},
		{"default/same access token on both transports", h.accessJWT, h.accessJWT, nil, admitted(CredentialAccessToken, TransportHeader, "")},
		{"default/bad signature by header", badSigJWT, "", nil, denied},
		{"default/bad signature by header never falls back to a valid cookie", badSigJWT, h.accessJWT, nil, denied},
		{"default/expired access token by header", expiredJWT, "", nil, denied},
		{"default/expired access token by cookie", "", expiredJWT, nil, denied},
		{"default/two-dot garbage by header", garbageJWT, "", nil, denied},
		{"default/two-dot garbage by header never falls back to a valid cookie", garbageJWT, h.accessJWT, nil, denied},
		{"default/api key by header", selfKey, "", nil, admitted(CredentialAPIKey, TransportHeader, "self")},
		{"default/act-as-user api key by header", actKey, "", nil, admitted(CredentialAPIKey, TransportHeader, "act")},
		{"default/api key wins over a valid cookie", selfKey, h.accessJWT, nil, admitted(CredentialAPIKey, TransportHeader, "self")},
		{"default/revoked api key", revokedKey, "", nil, denied},
		{"default/expired api key", expiredKey, "", nil, denied},
		{"default/unknown api key", unknownKey, "", nil, denied},
		{"default/api key in the cookie is read as an access token and fails", "", selfKey, nil, denied},

		// --- Accept(access_token): a person, either transport ---
		{"access-token-only/by header", h.accessJWT, "", []PrincipalOption{Accept(CredentialAccessToken)}, admitted(CredentialAccessToken, TransportHeader, "")},
		{"access-token-only/by cookie", "", h.accessJWT, []PrincipalOption{Accept(CredentialAccessToken)}, admitted(CredentialAccessToken, TransportCookie, "")},
		{"access-token-only/api key refused", selfKey, "", []PrincipalOption{Accept(CredentialAccessToken)}, denied},
		// The correction: the removed RequireUser ignored a non-JWT bearer and read
		// the cookie, so a key plus a valid cookie passed as the cookie's user.
		{"access-token-only/api key plus a valid cookie is refused", selfKey, h.accessJWT, []PrincipalOption{Accept(CredentialAccessToken)}, denied},
		{"access-token-only/no credential", "", "", []PrincipalOption{Accept(CredentialAccessToken)}, denied},

		// --- Accept(api_key): machines only ---
		{"api-key-only/by header", selfKey, "", []PrincipalOption{Accept(CredentialAPIKey)}, admitted(CredentialAPIKey, TransportHeader, "self")},
		{"api-key-only/access token by header refused", h.accessJWT, "", []PrincipalOption{Accept(CredentialAPIKey)}, denied},
		{"api-key-only/access token by cookie refused", "", h.accessJWT, []PrincipalOption{Accept(CredentialAPIKey)}, denied},
		{"api-key-only/no credential", "", "", []PrincipalOption{Accept(CredentialAPIKey)}, denied},

		// --- Transports(cookie): the browser-app posture, headers never consulted ---
		{"cookie-only/access token by cookie", "", h.accessJWT, []PrincipalOption{Transports(TransportCookie)}, admitted(CredentialAccessToken, TransportCookie, "")},
		{"cookie-only/access token by header alone", h.accessJWT, "", []PrincipalOption{Transports(TransportCookie)}, denied},
		// Ignore-on-un-admitted-transport: a header this surface never reads cannot
		// deny the cookie it does read.
		{"cookie-only/garbage header is ignored beside a valid cookie", garbageJWT, h.accessJWT, []PrincipalOption{Transports(TransportCookie)}, admitted(CredentialAccessToken, TransportCookie, "")},
		{"cookie-only/api key header is ignored beside a valid cookie", selfKey, h.accessJWT, []PrincipalOption{Transports(TransportCookie)}, admitted(CredentialAccessToken, TransportCookie, "")},
		{"cookie-only/api key header alone", selfKey, "", []PrincipalOption{Transports(TransportCookie)}, denied},

		// --- Transports(header): the API-host posture, the cookie never consulted ---
		{"header-only/access token by header", h.accessJWT, "", []PrincipalOption{Transports(TransportHeader)}, admitted(CredentialAccessToken, TransportHeader, "")},
		{"header-only/access token by cookie alone", "", h.accessJWT, []PrincipalOption{Transports(TransportHeader)}, denied},
		{"header-only/api key by header", selfKey, "", []PrincipalOption{Transports(TransportHeader)}, admitted(CredentialAPIKey, TransportHeader, "self")},
		{"header-only/api key beside a valid cookie", selfKey, h.accessJWT, []PrincipalOption{Transports(TransportHeader)}, admitted(CredentialAPIKey, TransportHeader, "self")},

		// --- Accept(access_token) + Transports(cookie): RequireAccessTokenCookie ---
		{"access-token-cookie/valid cookie", "", h.accessJWT, []PrincipalOption{Accept(CredentialAccessToken), Transports(TransportCookie)}, admitted(CredentialAccessToken, TransportCookie, "")},
		{"access-token-cookie/garbage header is ignored beside a valid cookie", garbageJWT, h.accessJWT, []PrincipalOption{Accept(CredentialAccessToken), Transports(TransportCookie)}, admitted(CredentialAccessToken, TransportCookie, "")},
		{"access-token-cookie/api key header is ignored beside a valid cookie", selfKey, h.accessJWT, []PrincipalOption{Accept(CredentialAccessToken), Transports(TransportCookie)}, admitted(CredentialAccessToken, TransportCookie, "")},
		{"access-token-cookie/access token by header alone", h.accessJWT, "", []PrincipalOption{Accept(CredentialAccessToken), Transports(TransportCookie)}, denied},
		{"access-token-cookie/api key in the cookie", "", selfKey, []PrincipalOption{Accept(CredentialAccessToken), Transports(TransportCookie)}, denied},

		// --- the same sets at the Live() tier (the session row exists) ---
		{"default+live/access token by header", h.accessJWT, "", []PrincipalOption{Live()}, credentialWant{status: http.StatusOK, kind: CredentialAccessToken, transport: TransportHeader, liveSession: true}},
		{"default+live/access token by cookie", "", h.accessJWT, []PrincipalOption{Live()}, credentialWant{status: http.StatusOK, kind: CredentialAccessToken, transport: TransportCookie, liveSession: true}},
		// A key owns no session row: it passes the tier and stashes no session id.
		{"default+live/api key", selfKey, "", []PrincipalOption{Live()}, admitted(CredentialAPIKey, TransportHeader, "self")},
		{"default+live/act-as-user api key", actKey, "", []PrincipalOption{Live()}, admitted(CredentialAPIKey, TransportHeader, "act")},
		{"default+live/expired access token", expiredJWT, "", []PrincipalOption{Live()}, denied},
		{"access-token-only+live/by header", h.accessJWT, "", []PrincipalOption{Accept(CredentialAccessToken), Live()}, credentialWant{status: http.StatusOK, kind: CredentialAccessToken, transport: TransportHeader, liveSession: true}},
		{"access-token-only+live/api key refused", selfKey, "", []PrincipalOption{Accept(CredentialAccessToken), Live()}, denied},
		{"api-key-only+live/by header", selfKey, "", []PrincipalOption{Accept(CredentialAPIKey), Live()}, admitted(CredentialAPIKey, TransportHeader, "self")},
		{"cookie-only+live/valid cookie", "", h.accessJWT, []PrincipalOption{Transports(TransportCookie), Live()}, credentialWant{status: http.StatusOK, kind: CredentialAccessToken, transport: TransportCookie, liveSession: true}},
		{"header-only+live/valid header", h.accessJWT, "", []PrincipalOption{Transports(TransportHeader), Live()}, credentialWant{status: http.StatusOK, kind: CredentialAccessToken, transport: TransportHeader, liveSession: true}},
		{"access-token-cookie+live/valid cookie", "", h.accessJWT, []PrincipalOption{Accept(CredentialAccessToken), Transports(TransportCookie), Live()}, credentialWant{status: http.StatusOK, kind: CredentialAccessToken, transport: TransportCookie, liveSession: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, got := serveGuarded(h, h.svc.RequirePrincipal(tc.opts...), h.credentialRequest(tc.header, tc.cookie))
			if rec.Code != tc.want.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want.status, rec.Body)
			}
			if tc.want.status != http.StatusOK {
				if got.reached {
					t.Fatal("a denied request reached the handler")
				}
				return
			}
			if !got.reached {
				t.Fatal("an admitted request never reached the handler")
			}
			if got.cred.Kind != tc.want.kind || got.cred.Transport != tc.want.transport {
				t.Errorf("credential = %s/%s, want %s/%s", got.cred.Kind, got.cred.Transport, tc.want.kind, tc.want.transport)
			}
			if want := principals[tc.want.key]; got.principal != want {
				t.Errorf("principal = %+v, want %+v", got.principal, want)
			}
			switch tc.want.kind {
			case CredentialAccessToken:
				if got.cred.SessionID != h.sessionID {
					t.Errorf("SessionID = %q, want the access JWT's claim %q", got.cred.SessionID, h.sessionID)
				}
				if got.cred.APIKeyID != "" || got.cred.ServiceAccountID != "" || got.cred.ActAsUser {
					t.Errorf("access-token credential carries key coordinates: %+v", got.cred)
				}
			case CredentialAPIKey:
				if got.cred.APIKeyID != keyIDs[tc.want.key] || got.cred.ServiceAccountID != accountIDs[tc.want.key] {
					t.Errorf("key coordinates = %s/%s, want %s/%s", got.cred.APIKeyID, got.cred.ServiceAccountID, keyIDs[tc.want.key], accountIDs[tc.want.key])
				}
				if got.cred.ActAsUser != actAs[tc.want.key] {
					t.Errorf("ActAsUser = %v, want %v", got.cred.ActAsUser, actAs[tc.want.key])
				}
				if got.cred.SessionID != "" {
					t.Errorf("api-key credential carries a session id: %q", got.cred.SessionID)
				}
			}
			if tc.want.liveSession {
				if !got.sessionOK || got.sessionID != h.sessionID {
					t.Errorf("CurrentSessionID = (%q, %v), want the proven session %q", got.sessionID, got.sessionOK, h.sessionID)
				}
			} else if got.sessionOK {
				t.Errorf("CurrentSessionID reported %q on a request that proved no session", got.sessionID)
			}
		})
	}
}

// --- the Live() tier's own rules ---

// TestRequirePrincipalLiveTier pins the immediate-revocation tier: a deleted
// session denies while the stateless authenticator still admits the same
// unexpired JWT (the documented staleness window), a repository error fails
// CLOSED, and an API key passes the tier without a lookup.
func TestRequirePrincipalLiveTier(t *testing.T) {
	t.Run("deleted session denies while the stateless authenticator admits", func(t *testing.T) {
		h := newCredentialHarness(t)
		if err := h.sess.Delete(context.Background(), h.sessionID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		rec, _ := serveGuarded(h, h.svc.RequirePrincipal(), h.credentialRequest(h.accessJWT, ""))
		if rec.Code != http.StatusOK {
			t.Errorf("stateless status = %d, want 200 (the documented staleness window)", rec.Code)
		}
		recLive, gotLive := serveGuarded(h, h.svc.RequirePrincipal(Live()), h.credentialRequest(h.accessJWT, ""))
		if recLive.Code != http.StatusUnauthorized || gotLive.reached {
			t.Errorf("live status = %d reached=%v, want 401 not-reached", recLive.Code, gotLive.reached)
		}
	})

	t.Run("repository error fails closed", func(t *testing.T) {
		h := newCredentialHarness(t)
		h.sess.getErr = errors.New("session store unavailable")
		rec, _ := serveGuarded(h, h.svc.RequirePrincipal(Live()), h.credentialRequest(h.accessJWT, ""))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("unreadable session store status = %d, want 401 (fail closed)", rec.Code)
		}
	})

	t.Run("api key passes without a session lookup", func(t *testing.T) {
		h := newCredentialHarness(t)
		raw, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
		before := h.sessionGets()
		rec, got := serveGuarded(h, h.svc.RequirePrincipal(Live()), h.credentialRequest(raw, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		if got.sessionOK {
			t.Errorf("CurrentSessionID reported %q for a machine caller", got.sessionID)
		}
		if after := h.sessionGets(); after != before {
			t.Errorf("session lookups = %d, want %d (a key owns no session row)", after, before)
		}
	})
}

// sessionGets reads the session store's Get counter.
func (h *credentialHarness) sessionGets() int {
	h.sess.mu.Lock()
	defer h.sess.mu.Unlock()
	return h.sess.gets
}

// --- nesting: an inner authenticator narrows, it never re-resolves ---

// TestRequirePrincipalNestedNarrowing runs every named helper INSIDE a default
// outer authenticator: the inner one reads the stash, so it either narrows to the
// same credential or denies, and the API-key repository is resolved exactly once
// no matter how many authenticators the chain carries.
func TestRequirePrincipalNestedNarrowing(t *testing.T) {
	h := newCredentialHarness(t)
	selfKey, _, selfSAID := h.mintKey(t, "bot", false, "", time.Time{})

	cases := []struct {
		name       string
		inner      func() web.Middleware
		header     string
		cookie     string
		wantStatus int
		wantKind   CredentialKind
	}{
		{"RequireAccessTokenOrAPIKey admits the stashed access token", h.svc.RequireAccessTokenOrAPIKey, h.accessJWT, "", http.StatusOK, CredentialAccessToken},
		{"RequireAccessTokenOrAPIKey admits the stashed key", h.svc.RequireAccessTokenOrAPIKey, selfKey, "", http.StatusOK, CredentialAPIKey},
		{"RequireAccessTokenOrAPIKeyLive admits the stashed access token", h.svc.RequireAccessTokenOrAPIKeyLive, h.accessJWT, "", http.StatusOK, CredentialAccessToken},
		{"RequireAccessTokenOrAPIKeyLive admits the stashed key", h.svc.RequireAccessTokenOrAPIKeyLive, selfKey, "", http.StatusOK, CredentialAPIKey},
		{"RequireAccessToken admits the stashed access token", h.svc.RequireAccessToken, h.accessJWT, "", http.StatusOK, CredentialAccessToken},
		{"RequireAccessToken refuses the stashed key", h.svc.RequireAccessToken, selfKey, "", http.StatusUnauthorized, ""},
		{"RequireAccessTokenLive admits the stashed access token", h.svc.RequireAccessTokenLive, h.accessJWT, "", http.StatusOK, CredentialAccessToken},
		{"RequireAccessTokenLive refuses the stashed key", h.svc.RequireAccessTokenLive, selfKey, "", http.StatusUnauthorized, ""},
		// The stash says the credential rode the HEADER, so a cookie-only inner
		// denies WITHOUT ever reading the cookie the request also carries.
		{"RequireAccessTokenCookie refuses a header-borne access token", h.svc.RequireAccessTokenCookie, h.accessJWT, h.accessJWT, http.StatusUnauthorized, ""},
		{"RequireAccessTokenCookie admits a cookie-borne access token", h.svc.RequireAccessTokenCookie, "", h.accessJWT, http.StatusOK, CredentialAccessToken},
		{"RequireAPIKey admits the stashed key", h.svc.RequireAPIKey, selfKey, "", http.StatusOK, CredentialAPIKey},
		{"RequireAPIKey refuses the stashed access token", h.svc.RequireAPIKey, h.accessJWT, "", http.StatusUnauthorized, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := h.keyLookups()
			outer := h.svc.RequirePrincipal()
			chain := func(next http.Handler) http.Handler { return outer(tc.inner()(next)) }
			rec, got := serveGuarded(h, chain, h.credentialRequest(tc.header, tc.cookie))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantStatus == http.StatusOK && got.cred.Kind != tc.wantKind {
				t.Errorf("credential kind = %q, want %q", got.cred.Kind, tc.wantKind)
			}
			if lookups := h.keyLookups() - before; lookups > 1 {
				t.Errorf("api-key repository resolutions = %d, want at most 1 (a nested authenticator narrows off the stash)", lookups)
			}
		})
	}

	// A self-acting key's principal survives the narrowing intact.
	rec, got := serveGuarded(h, func(next http.Handler) http.Handler {
		return h.svc.RequirePrincipal()(h.svc.RequireAPIKey()(next))
	}, h.credentialRequest(selfKey, ""))
	if rec.Code != http.StatusOK || got.principal.ID != selfSAID {
		t.Errorf("nested api-key principal = %+v status=%d, want {service_account, %s} 200", got.principal, rec.Code, selfSAID)
	}
}

// keyLookups reads the API-key repository's GetByHash counter.
func (h *credentialHarness) keyLookups() int {
	h.keys.mu.Lock()
	defer h.keys.mu.Unlock()
	return h.keys.lookups
}

// TestRequirePrincipalNestedWideningDenies proves the set can only ever narrow:
// an inner Accept(api_key) under an Accept(access_token) outer names a kind the
// stash can never hold, so it is a runtime 401 rather than a widening.
func TestRequirePrincipalNestedWideningDenies(t *testing.T) {
	h := newCredentialHarness(t)
	selfKey, _, _ := h.mintKey(t, "bot", false, "", time.Time{})

	chain := func(next http.Handler) http.Handler {
		return h.svc.RequirePrincipal(Accept(CredentialAccessToken))(h.svc.RequirePrincipal(Accept(CredentialAPIKey))(next))
	}
	// The access token clears the outer set and is then refused by the inner one.
	rec, got := serveGuarded(h, chain, h.credentialRequest(h.accessJWT, ""))
	if rec.Code != http.StatusUnauthorized || got.reached {
		t.Errorf("access token under a widening inner = %d reached=%v, want 401 not-reached", rec.Code, got.reached)
	}
	// The key never clears the outer set at all.
	recKey, gotKey := serveGuarded(h, chain, h.credentialRequest(selfKey, ""))
	if recKey.Code != http.StatusUnauthorized || gotKey.reached {
		t.Errorf("api key under an access-token-only outer = %d reached=%v, want 401 not-reached", recKey.Code, gotKey.reached)
	}
}

// TestRequirePrincipalNestedLiveRunsOneLookup pins rule 2's second half: a Live()
// inner under a Live() outer reads the already-proven CurrentSessionID and passes
// without a second session lookup.
func TestRequirePrincipalNestedLiveRunsOneLookup(t *testing.T) {
	h := newCredentialHarness(t)
	before := h.sessionGets()
	chain := func(next http.Handler) http.Handler {
		return h.svc.RequirePrincipal(Live())(h.svc.RequireAccessTokenLive()(next))
	}
	rec, got := serveGuarded(h, chain, h.credentialRequest(h.accessJWT, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if !got.sessionOK || got.sessionID != h.sessionID {
		t.Errorf("CurrentSessionID = (%q, %v), want %q", got.sessionID, got.sessionOK, h.sessionID)
	}
	if lookups := h.sessionGets() - before; lookups != 1 {
		t.Errorf("session lookups = %d, want 1 for two nested Live() authenticators", lookups)
	}
}

// --- the API-key credential path: one resolution, one event, one touch ---

// TestAPIKeyCredentialAuditRail pins the audit branches and the side-effect
// budget of the ONE raw-key verification path: a success records exactly one
// apikey_auth success row and one best-effort touch off a single repository
// resolution, a revoked key records `blocked`, an expired one `failure`, and an
// unknown one nothing at all.
func TestAPIKeyCredentialAuditRail(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newCredentialHarness(t)
		raw, _, saID := h.mintKey(t, "bot", false, "", time.Time{})
		h.events.mu.Lock()
		h.events.events = nil
		h.events.mu.Unlock()
		before, touchedBefore := h.keyLookups(), h.keyTouches()

		rec, got := serveGuarded(h, h.svc.RequireAPIKey(), h.credentialRequest(raw, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		if got.principal.ID != saID {
			t.Errorf("principal = %+v, want the service account %s", got.principal, saID)
		}
		if lookups := h.keyLookups() - before; lookups != 1 {
			t.Errorf("repository resolutions = %d, want 1", lookups)
		}
		if touches := h.keyTouches() - touchedBefore; touches != 1 {
			t.Errorf("TouchLastUsed calls = %d, want 1", touches)
		}
		e := requireEvent(t, h.events, securityevent.TypeAPIKeyAuth, securityevent.StatusSuccess)
		if e.Actor.Type != PrincipalServiceAccount || e.Actor.ID != saID {
			t.Errorf("actor = %+v, want {service_account, %s}", e.Actor, saID)
		}
		if apiKeyAuthRows(h) != 1 {
			t.Errorf("apikey_auth rows = %d, want exactly 1 per authentication", apiKeyAuthRows(h))
		}
	})

	t.Run("revoked records blocked", func(t *testing.T) {
		h := newCredentialHarness(t)
		raw, keyID, saID := h.mintKey(t, "bot", false, "", time.Time{})
		if err := h.svc.RevokeAPIKey(context.Background(), keyID); err != nil {
			t.Fatalf("RevokeAPIKey: %v", err)
		}
		h.events.mu.Lock()
		h.events.events = nil
		h.events.mu.Unlock()
		rec, _ := serveGuarded(h, h.svc.RequireAPIKey(), h.credentialRequest(raw, ""))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		e := requireEvent(t, h.events, securityevent.TypeAPIKeyAuth, securityevent.StatusBlocked)
		if e.Actor.Type != PrincipalServiceAccount || e.Actor.ID != saID {
			t.Errorf("actor = %+v, want the owning account {service_account, %s}", e.Actor, saID)
		}
	})

	t.Run("expired records failure", func(t *testing.T) {
		h := newCredentialHarness(t)
		raw, _, _ := h.mintKey(t, "stale", false, "", time.Now().Add(-time.Hour))
		h.events.mu.Lock()
		h.events.events = nil
		h.events.mu.Unlock()
		rec, _ := serveGuarded(h, h.svc.RequireAPIKey(), h.credentialRequest(raw, ""))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		requireEvent(t, h.events, securityevent.TypeAPIKeyAuth, securityevent.StatusFailure)
	})

	t.Run("unknown records nothing", func(t *testing.T) {
		h := newCredentialHarness(t)
		h.events.mu.Lock()
		h.events.events = nil
		h.events.mu.Unlock()
		rec, _ := serveGuarded(h, h.svc.RequireAPIKey(), h.credentialRequest("prefix00_deadbeefdeadbeef", ""))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if apiKeyAuthRows(h) != 0 {
			t.Errorf("apikey_auth rows = %d, want 0 for a key that matched nothing", apiKeyAuthRows(h))
		}
	})
}

// keyTouches reads the API-key repository's TouchLastUsed counter.
func (h *credentialHarness) keyTouches() int {
	h.keys.mu.Lock()
	defer h.keys.mu.Unlock()
	return h.keys.touched
}

// apiKeyAuthRows counts the recorded apikey_auth events.
func apiKeyAuthRows(h *credentialHarness) int {
	n := 0
	for _, e := range h.events.recorded() {
		if e.EventType == securityevent.TypeAPIKeyAuth {
			n++
		}
	}
	return n
}

// TestRequirePrincipalDenialIsGeneric proves a denial can never distinguish WHY:
// no credential, an unknown key, a revoked key, an expired key, and a bad
// signature all produce the byte-identical 401 body.
func TestRequirePrincipalDenialIsGeneric(t *testing.T) {
	h := newCredentialHarness(t)
	revokedKey, revokedKeyID, _ := h.mintKey(t, "revoked", false, "", time.Time{})
	if err := h.svc.RevokeAPIKey(context.Background(), revokedKeyID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	expired, _, _ := h.mintKey(t, "stale", false, "", time.Now().Add(-time.Hour))

	bodies := map[string]string{}
	for name, header := range map[string]string{
		"none":          "",
		"unknown key":   "prefix00_deadbeefdeadbeef",
		"revoked key":   revokedKey,
		"expired key":   expired,
		"bad signature": h.badSignatureAccessJWT(t),
	} {
		rec, _ := serveGuarded(h, h.svc.RequirePrincipal(), h.credentialRequest(header, ""))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", name, rec.Code)
		}
		bodies[name] = rec.Body.String()
	}
	want := bodies["none"]
	for name, body := range bodies {
		if body != want {
			t.Errorf("%s denial body = %q, want the generic %q", name, body, want)
		}
	}
}

// --- construction-time programming errors ---

// TestPrincipalOptionEmptySetPanics pins rule 4: a set that admits nothing is a
// programming error caught at wiring time, not a posture served at runtime. An
// unknown member is the same class of mistake.
func TestPrincipalOptionEmptySetPanics(t *testing.T) {
	cases := []struct {
		name string
		call func()
	}{
		{"Accept with no kinds", func() { Accept() }},
		{"Transports with no transports", func() { Transports() }},
		{"Accept with an unknown kind", func() { Accept(CredentialKind("password")) }},
		{"Transports with an unknown transport", func() { Transports(Transport("mtls")) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic")
				}
			}()
			tc.call()
		})
	}
}

// TestAcceptAPIKeyWithoutMachineSubsystem proves deny-by-absence survives an
// explicit Accept: naming api_key on a host with no machine repositories cannot
// conjure the subsystem, so the bearer denies without a lookup.
func TestAcceptAPIKeyWithoutMachineSubsystem(t *testing.T) {
	svc := NewService(Deps{
		Users:       newFakeUsers(),
		Passwords:   newFakePasswords(),
		Sessions:    newFakeSessions(),
		Hasher:      &fakeHasher{},
		Limiter:     ratelimiter.NewMemory(),
		TokenSigner: newFakeSigner(),
	})
	if svc.MachineEnabled() {
		t.Fatal("MachineEnabled reported true with no machine repos")
	}
	rec := httptest.NewRecorder()
	reached := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
	req := httptest.NewRequest("GET", "/guarded", nil)
	req.Header.Set("Authorization", "Bearer prefix00_deadbeef")
	svc.RequirePrincipal(Accept(CredentialAPIKey))(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || reached {
		t.Errorf("status = %d reached=%v, want 401 not-reached", rec.Code, reached)
	}
}

// --- the named helpers ---

// TestNamedHelperPostures pins each helper as literally the call it names, over
// the credentials that distinguish them — including the two rules a host is most
// likely to get wrong: RequireAccessToken() refuses an API key even beside a
// valid cookie (the correction), and RequireAccessTokenCookie() ignores an
// un-admitted header rather than denying on it.
func TestNamedHelperPostures(t *testing.T) {
	h := newCredentialHarness(t)
	selfKey, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
	const garbageJWT = "aaa.bbb.ccc"

	cases := []struct {
		name       string
		helper     func() web.Middleware
		header     string
		cookie     string
		wantStatus int
		wantKind   CredentialKind
		wantLive   bool
	}{
		{"RequireAccessTokenOrAPIKey/access token", h.svc.RequireAccessTokenOrAPIKey, h.accessJWT, "", http.StatusOK, CredentialAccessToken, false},
		{"RequireAccessTokenOrAPIKey/api key", h.svc.RequireAccessTokenOrAPIKey, selfKey, "", http.StatusOK, CredentialAPIKey, false},
		{"RequireAccessTokenOrAPIKeyLive/access token", h.svc.RequireAccessTokenOrAPIKeyLive, h.accessJWT, "", http.StatusOK, CredentialAccessToken, true},
		{"RequireAccessTokenOrAPIKeyLive/api key", h.svc.RequireAccessTokenOrAPIKeyLive, selfKey, "", http.StatusOK, CredentialAPIKey, false},
		{"RequireAccessToken/header", h.svc.RequireAccessToken, h.accessJWT, "", http.StatusOK, CredentialAccessToken, false},
		{"RequireAccessToken/cookie", h.svc.RequireAccessToken, "", h.accessJWT, http.StatusOK, CredentialAccessToken, false},
		{"RequireAccessToken/api key beside a valid cookie", h.svc.RequireAccessToken, selfKey, h.accessJWT, http.StatusUnauthorized, "", false},
		{"RequireAccessTokenLive/header", h.svc.RequireAccessTokenLive, h.accessJWT, "", http.StatusOK, CredentialAccessToken, true},
		{"RequireAccessTokenLive/api key", h.svc.RequireAccessTokenLive, selfKey, "", http.StatusUnauthorized, "", false},
		{"RequireAccessTokenCookie/cookie", h.svc.RequireAccessTokenCookie, "", h.accessJWT, http.StatusOK, CredentialAccessToken, false},
		{"RequireAccessTokenCookie/garbage header beside a valid cookie", h.svc.RequireAccessTokenCookie, garbageJWT, h.accessJWT, http.StatusOK, CredentialAccessToken, false},
		{"RequireAccessTokenCookie/header alone", h.svc.RequireAccessTokenCookie, h.accessJWT, "", http.StatusUnauthorized, "", false},
		{"RequireAPIKey/api key", h.svc.RequireAPIKey, selfKey, "", http.StatusOK, CredentialAPIKey, false},
		{"RequireAPIKey/access token", h.svc.RequireAPIKey, h.accessJWT, "", http.StatusUnauthorized, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, got := serveGuarded(h, tc.helper(), h.credentialRequest(tc.header, tc.cookie))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			if got.cred.Kind != tc.wantKind {
				t.Errorf("credential kind = %q, want %q", got.cred.Kind, tc.wantKind)
			}
			if tc.wantLive != got.sessionOK {
				t.Errorf("CurrentSessionID present = %v, want %v", got.sessionOK, tc.wantLive)
			}
		})
	}
}

// TestCurrentCredentialAbsent proves the read is honest on an ungated request:
// no authenticator ran, so there is no credential to report.
func TestCurrentCredentialAbsent(t *testing.T) {
	h := newCredentialHarness(t)
	if cred, ok := h.svc.CurrentCredential(context.Background()); ok {
		t.Errorf("CurrentCredential reported %+v on a bare context", cred)
	}
	// A principal stashed by something OTHER than RequirePrincipal (a host's own
	// middleware) still reports no credential: the pocket never invents one.
	ctx := identity.WithPrincipal(context.Background(), Principal{Type: PrincipalUser, ID: "u1"})
	if _, ok := h.svc.CurrentCredential(ctx); ok {
		t.Error("CurrentCredential reported a credential for a principal it did not resolve")
	}
}

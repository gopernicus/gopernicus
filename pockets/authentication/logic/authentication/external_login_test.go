package authentication

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

var _ cacher.Storer = (*clockedStore)(nil)

// clockedStore is a cacher.Storer that expires entries against a test clock and
// remembers the TTL of the last Set.
type clockedStore struct {
	mu      sync.Mutex
	now     func() time.Time
	expires map[string]time.Time
	lastTTL time.Duration
}

func newClockedStore(now func() time.Time) *clockedStore {
	return &clockedStore{now: now, expires: map[string]time.Time{}}
}

func (s *clockedStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.expires[key]
	if !ok || (!exp.IsZero() && !s.now().Before(exp)) {
		delete(s.expires, key)
		return nil, false, nil
	}
	return []byte{1}, true, nil
}

func (s *clockedStore) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, key := range keys {
		if value, ok, _ := s.Get(ctx, key); ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *clockedStore) Set(_ context.Context, key string, _ []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTTL = ttl
	var exp time.Time
	if ttl > 0 {
		exp = s.now().Add(ttl)
	}
	s.expires[key] = exp
	return nil
}

func (s *clockedStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.expires, key)
	return nil
}

func (s *clockedStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.expires)
}

func (s *clockedStore) ttl() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTTL
}

// distinctToken is token with a jti claim, so one subject can hold several
// distinct raw credentials.
func (h *extHarness) distinctToken(p *extProvider, id sdkauth.Identity, jti string) string {
	id.Issuer = p.issuer
	if id.AuthenticatedAt.IsZero() {
		id.AuthenticatedAt = h.clock.Now().Add(-time.Minute)
	}
	body, _ := json.Marshal(map[string]any{"iss": p.issuer, "sub": id.Subject, "jti": jti})
	raw := "e30." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
	p.identities[raw] = id
	return raw
}

// verifyTimes verifies raw n times, failing the test on any error.
func (h *extHarness) verifyTimes(t *testing.T, raw string, n int) {
	t.Helper()
	for range n {
		if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
}

func TestExternalLoginRecordedOncePerToken(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", Email: "login-0451@example.test", ExpiresAt: h.clock.Now().Add(time.Hour)})

	h.verifyTimes(t, raw, 3)
	logins := h.eventsOf(securityevent.TypeExternalLogin)
	if len(logins) != 1 {
		t.Fatalf("external_login rows = %d across three verifications of one token, want 1", len(logins))
	}
	evt := logins[0]
	if evt.EventStatus != securityevent.StatusSuccess || evt.UserID != "u1" || evt.Details["provider"] != "alpha" || len(evt.Details) != 1 {
		t.Fatalf("external_login row = %+v", evt)
	}
	sum := sha256.Sum256([]byte(raw))
	assertNoSecretInDetails(t, h.events.recorded(), raw, hex.EncodeToString(sum[:]), "login-0451@example.test")

	other := h.distinctToken(h.providerA, sdkauth.Identity{Subject: "s1", ExpiresAt: h.clock.Now().Add(2 * time.Hour)}, "second")
	h.verifyTimes(t, other, 1)
	if got := len(h.eventsOf(securityevent.TypeExternalLogin)); got != 2 {
		t.Fatalf("external_login rows = %d after a second token, want 2", got)
	}
}

func TestExternalLoginRecordedAgainAfterTTL(t *testing.T) {
	var logins *clockedStore
	h := newExtHarness(t, func(c *ExternalConfig) {
		logins = newClockedStore(time.Now)
		c.LoginRecordCache = logins
	})
	logins.now = h.clock.Now
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", ExpiresAt: h.clock.Now().Add(10 * time.Minute)})

	h.verifyTimes(t, raw, 2)
	if got := len(h.eventsOf(securityevent.TypeExternalLogin)); got != 1 {
		t.Fatalf("external_login rows = %d before the TTL, want 1", got)
	}
	if got := logins.ttl(); got != 10*time.Minute {
		t.Fatalf("login record TTL = %v, want the token's remaining 10m", got)
	}
	h.clock.advance(11 * time.Minute)
	h.verifyTimes(t, raw, 1)
	if got := len(h.eventsOf(securityevent.TypeExternalLogin)); got != 2 {
		t.Fatalf("external_login rows = %d after the TTL, want 2", got)
	}
}

func TestExternalLoginRecordTTLBounds(t *testing.T) {
	for name, tc := range map[string]struct {
		expiresIn time.Duration
		want      time.Duration
	}{
		"capped at 24h":       {expiresIn: 72 * time.Hour, want: 24 * time.Hour},
		"floored at 1m":       {expiresIn: 10 * time.Second, want: time.Minute},
		"already expired":     {expiresIn: -time.Hour, want: time.Minute},
		"remaining lifetime":  {expiresIn: 3 * time.Hour, want: 3 * time.Hour},
		"exactly the ceiling": {expiresIn: 24 * time.Hour, want: 24 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			var logins *clockedStore
			h := newExtHarness(t, func(c *ExternalConfig) {
				logins = newClockedStore(time.Now)
				c.LoginRecordCache = logins
			})
			logins.now = h.clock.Now
			h.store.addUser("u1", "alpha", "s1", user.StatusActive)
			h.verifyTimes(t, h.token(h.providerA, sdkauth.Identity{Subject: "s1", ExpiresAt: h.clock.Now().Add(tc.expiresIn)}), 1)
			if got := logins.ttl(); got != tc.want {
				t.Fatalf("login record TTL = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExternalLoginRecordedAgainAfterEviction(t *testing.T) {
	h := newExtHarness(t, func(c *ExternalConfig) { c.LoginRecordCacheEntries = 1 })
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	h.store.addUser("u2", "alpha", "s2", user.StatusActive)
	first := h.token(h.providerA, sdkauth.Identity{Subject: "s1", ExpiresAt: h.clock.Now().Add(time.Hour)})
	second := h.token(h.providerA, sdkauth.Identity{Subject: "s2", ExpiresAt: h.clock.Now().Add(time.Hour)})

	h.verifyTimes(t, first, 2)
	h.verifyTimes(t, second, 1) // evicts first's entry from the one-entry cache
	h.verifyTimes(t, first, 1)
	logins := h.eventsOf(securityevent.TypeExternalLogin)
	if len(logins) != 3 || logins[0].UserID != "u1" || logins[1].UserID != "u2" || logins[2].UserID != "u1" {
		t.Fatalf("external_login rows = %+v, want u1, u2, then u1 again after its eviction", logins)
	}
}

func TestExternalLoginCacheErrorRecords(t *testing.T) {
	h := newExtHarness(t, func(c *ExternalConfig) { c.LoginRecordCache = failingCache{} })
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", ExpiresAt: h.clock.Now().Add(time.Hour)})
	h.verifyTimes(t, raw, 3)
	if got := len(h.eventsOf(securityevent.TypeExternalLogin)); got != 3 {
		t.Fatalf("external_login rows = %d with the login cache down, want one per verification", got)
	}
}

func TestExternalLoginLeavesTheRevocationCacheAlone(t *testing.T) {
	var revocation *clockedStore
	h := newExtHarness(t, func(c *ExternalConfig) {
		revocation = newClockedStore(time.Now)
		c.RevocationCache = revocation
	})
	revocation.now = h.clock.Now
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	h.revocation.set("s1", sdkauth.RevocationFacts{}, nil)
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", ExpiresAt: h.clock.Now().Add(time.Hour)})
	if _, err := h.engine.RequireLive(h.authenticate(t, raw), LivenessOnMutation, true); err != nil {
		t.Fatalf("RequireLive: %v", err)
	}
	before := revocation.len()
	if before != 1 {
		t.Fatalf("revocation cache entries = %d after one re-check, want 1", before)
	}
	for i := range 5 {
		h.verifyTimes(t, h.distinctToken(h.providerA, sdkauth.Identity{Subject: "s1", ExpiresAt: h.clock.Now().Add(time.Hour)}, fmt.Sprint("login-", i)), 1)
	}
	if got := len(h.eventsOf(securityevent.TypeExternalLogin)); got != 6 {
		t.Fatalf("external_login rows = %d, want one per distinct token", got)
	}
	if after := revocation.len(); after != before {
		t.Fatalf("revocation cache entries = %d after login records, want %d", after, before)
	}
}

func TestExternalLoginRecordCacheConfiguration(t *testing.T) {
	repos := ExternalRepositories{Users: extUsers{newExtStore()}, Identifiers: extIdentifiers{newExtStore()}, ExternalSubjects: extSubjects{newExtStore()}}
	provider := []ExternalProvider{{Name: "alpha", Provider: &extProvider{issuer: extIssuerA}}}
	var typedNil *clockedStore
	for name, cfg := range map[string]ExternalConfig{
		"negative entries": {Providers: provider, LoginRecordCacheEntries: -1},
		"typed nil cache":  {Providers: provider, LoginRecordCache: typedNil},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewExternalAuthenticator(repos, cfg, environment.ModeDevelopment); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("NewExternalAuthenticator = %v, want invalid input", err)
			}
		})
	}
}

package oauth2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

type graceSpent struct {
	sessionID string
	rotatedAt time.Time
	cipher    string
}

// graceStore models the delegated refresh rail: current hashes on sessions,
// spent hashes retained in history, and the RefreshGraceRepository columns.
// stale makes GetByRefreshHash report a spent hash as current, so a test can
// replay the loser of a concurrent rotation deterministically.
type graceStore struct {
	Repository
	mu       sync.Mutex
	sessions map[string]session.Session
	history  map[string]graceSpent
	stale    map[string]string
	probeErr error
}

func newGraceStore() *graceStore {
	return &graceStore{sessions: map[string]session.Session{}, history: map[string]graceSpent{}, stale: map[string]string{}}
}

func (g *graceStore) rotate(in Refresh, successor string, grace bool) (session.Session, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if spent, ok := g.history[in.Hash]; ok {
		sess, live := g.sessions[spent.sessionID]
		if !live || sess.Delegation.ClientID != in.ClientID || sess.Delegation.Resource != in.Resource {
			return session.Session{}, ErrInvalidGrant
		}
		if grace {
			return session.Session{}, ErrRefreshSpent
		}
		delete(g.sessions, spent.sessionID)
		return session.Session{}, ErrRefreshReuse
	}
	for id, sess := range g.sessions {
		if sess.RefreshTokenHash != in.Hash {
			continue
		}
		if in.NewHash == "" || sess.Delegation.ClientID != in.ClientID || sess.Delegation.Resource != in.Resource {
			return session.Session{}, ErrInvalidGrant
		}
		entry := graceSpent{sessionID: id}
		if grace {
			entry.rotatedAt, entry.cipher = in.Now, successor
		}
		g.history[in.Hash] = entry
		sess.RefreshTokenHash = in.NewHash
		g.sessions[id] = sess
		return sess, nil
	}
	return session.Session{}, ErrInvalidGrant
}

func (g *graceStore) RotateRefresh(_ context.Context, in Refresh) (session.Session, error) {
	return g.rotate(in, "", false)
}

func (g *graceStore) RotateRefreshWithSuccessor(_ context.Context, in Refresh, successor string) (session.Session, error) {
	return g.rotate(in, successor, true)
}

func (g *graceStore) GetRefreshSuccessor(_ context.Context, hash string) (RefreshSuccessor, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	spent, ok := g.history[hash]
	if !ok || spent.cipher == "" || spent.rotatedAt.IsZero() {
		return RefreshSuccessor{}, sdk.ErrNotFound
	}
	return RefreshSuccessor{SessionID: spent.sessionID, RotatedAt: spent.rotatedAt, Ciphertext: spent.cipher}, nil
}

func (g *graceStore) ProbeRefreshGraceSchema(context.Context) error { return g.probeErr }

func (g *graceStore) Get(_ context.Context, id string) (session.Session, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	sess, ok := g.sessions[id]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	return sess, nil
}

func (g *graceStore) GetByRefreshHash(_ context.Context, hash string) (session.Session, session.RefreshMatch, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id, ok := g.stale[hash]; ok {
		if sess, live := g.sessions[id]; live {
			sess.RefreshTokenHash = hash
			return sess, session.RefreshMatch(0), nil
		}
	}
	for _, sess := range g.sessions {
		if sess.RefreshTokenHash == hash {
			return sess, session.RefreshMatch(0), nil
		}
	}
	return session.Session{}, session.RefreshMatch(0), sdk.ErrNotFound
}

type graceSessions struct{ *graceStore }

type graceHarness struct {
	store *graceStore
	svc   *Service
	now   time.Time
	first string // the refresh token the connection started with
}

func newGraceHarness(t *testing.T, grace time.Duration) *graceHarness {
	t.Helper()
	enc, err := cryptids.NewAESGCM([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	h := &graceHarness{store: newGraceStore(), now: time.Now().UTC()}
	cfg := protocolConfig()
	cfg.RefreshReuseGrace, cfg.RefreshReuseEncrypter = grace, enc
	resolver := clientResolverFunc(func(_ context.Context, id string) (Client, error) {
		return Client{ID: id, Name: "Client", RedirectURIs: []string{denialRedirectURI}}, nil
	})
	signer := &assuranceSigner{issued: map[string]map[string]any{}}
	h.svc, err = New(Repositories{OAuth2: h.store, Users: assuranceUsers{}, Sessions: graceSessions{h.store}}, signer, resolver, assuranceVerifier{signer},
		environment.ModeProduction, cfg, WithClock(func() time.Time { return h.now }))
	if err != nil {
		t.Fatal(err)
	}
	h.first = session.NewDelegatedRefreshToken()
	h.store.sessions["d-1"] = session.Session{ID: "d-1", UserID: "u-1", Profile: session.ProfileDelegated, RefreshTokenHash: hashSecret(h.first), ExpiresAt: h.now.Add(24 * time.Hour),
		Delegation: session.Delegation{Issuer: cfg.Issuer, ClientID: denialClientID, Resource: cfg.MCPResource, ExchangeResource: cfg.APIResource, ExchangeClientID: cfg.ConfidentialClientID}}
	return h
}

func (h *graceHarness) refresh(t *testing.T, raw string) (TokenResponse, error) {
	t.Helper()
	return h.svc.Refresh(t.Context(), RefreshRequest{RefreshToken: raw, ClientID: denialClientID, Resource: h.svc.config.MCPResource})
}

func (h *graceHarness) connected() bool {
	_, err := h.store.Get(context.Background(), "d-1")
	return err == nil
}

// TestRefreshGraceReturnsTheIssuedSuccessor pins #381: a second refresh of the
// same token inside the window, whether it arrives after the rotation or lost
// a concurrent one, gets the successor already issued, and the connection
// survives and keeps refreshing from it.
func TestRefreshGraceReturnsTheIssuedSuccessor(t *testing.T) {
	for name, lostRace := range map[string]bool{"after the rotation": false, "lost a concurrent rotation": true} {
		t.Run(name, func(t *testing.T) {
			h := newGraceHarness(t, 10*time.Second)
			if lostRace {
				// The loser read the token while it was still current.
				h.store.stale[hashSecret(h.first)] = "d-1"
			}
			winner, err := h.refresh(t, h.first)
			if err != nil {
				t.Fatal(err)
			}
			h.now = h.now.Add(5 * time.Second)
			loser, err := h.refresh(t, h.first)
			if err != nil {
				t.Fatalf("reuse inside the window: %v", err)
			}
			if loser.RefreshToken != winner.RefreshToken || loser.AccessToken == "" {
				t.Fatal("the grace answer minted a different successor")
			}
			if !h.connected() {
				t.Fatal("the connection was revoked")
			}
			if _, err := h.refresh(t, winner.RefreshToken); err != nil {
				t.Fatalf("the successor no longer refreshes: %v", err)
			}
		})
	}
}

func TestRefreshGraceRevokesOutsideItsCase(t *testing.T) {
	t.Run("after the window", func(t *testing.T) {
		h := newGraceHarness(t, 10*time.Second)
		if _, err := h.refresh(t, h.first); err != nil {
			t.Fatal(err)
		}
		h.now = h.now.Add(10 * time.Second)
		if _, err := h.refresh(t, h.first); !errors.Is(err, ErrRefreshReuse) || h.connected() {
			t.Fatalf("reuse after the window: err=%v connected=%v, want refresh reuse (invalid_grant) and revoked", err, h.connected())
		}
	})
	t.Run("grandparent", func(t *testing.T) {
		h := newGraceHarness(t, 10*time.Second)
		second, err := h.refresh(t, h.first)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.refresh(t, second.RefreshToken); err != nil {
			t.Fatal(err)
		}
		if _, err := h.refresh(t, h.first); !errors.Is(err, ErrRefreshReuse) || h.connected() {
			t.Fatalf("grandparent inside the window: err=%v connected=%v, want revoked", err, h.connected())
		}
	})
	t.Run("revoked connection", func(t *testing.T) {
		h := newGraceHarness(t, 10*time.Second)
		if _, err := h.refresh(t, h.first); err != nil {
			t.Fatal(err)
		}
		delete(h.store.sessions, "d-1")
		if _, err := h.refresh(t, h.first); !errors.Is(err, ErrInvalidGrant) {
			t.Fatalf("reuse after revoke: err=%v, want invalid_grant", err)
		}
	})
	t.Run("another client", func(t *testing.T) {
		h := newGraceHarness(t, 10*time.Second)
		if _, err := h.refresh(t, h.first); err != nil {
			t.Fatal(err)
		}
		_, err := h.svc.Refresh(t.Context(), RefreshRequest{RefreshToken: h.first, ClientID: "https://other.example/metadata", Resource: h.svc.config.MCPResource})
		if err == nil {
			t.Fatal("another client got the successor")
		}
	})
}

// TestRefreshGraceOffIsStrict pins that a zero grace is today's behaviour:
// the first reuse revokes, and nothing is written for a window.
func TestRefreshGraceOffIsStrict(t *testing.T) {
	h := newGraceHarness(t, 0)
	if h.svc.grace != nil {
		t.Fatal("grace wired at 0")
	}
	if _, err := h.refresh(t, h.first); err != nil {
		t.Fatal(err)
	}
	if spent := h.store.history[hashSecret(h.first)]; spent.cipher != "" || !spent.rotatedAt.IsZero() {
		t.Fatal("grace 0 recorded a successor")
	}
	if _, err := h.refresh(t, h.first); !errors.Is(err, ErrRefreshReuse) || h.connected() {
		t.Fatalf("reuse at grace 0: err=%v connected=%v, want revoked", err, h.connected())
	}
}

func TestRefreshGraceBootRefusals(t *testing.T) {
	enc, _ := cryptids.NewAESGCM([]byte(strings.Repeat("k", 32)))
	build := func(repo Repository, grace time.Duration, encrypter cryptids.Encrypter) error {
		cfg := protocolConfig()
		cfg.RefreshReuseGrace, cfg.RefreshReuseEncrypter = grace, encrypter
		_, err := New(Repositories{OAuth2: repo, Users: &constructorUsers{}, Sessions: &constructorSessions{}}, &constructorSigner{},
			clientResolverFunc(func(context.Context, string) (Client, error) { return Client{}, ErrInvalidClient }), &constructorVerifier{}, environment.ModeProduction, cfg)
		return err
	}
	missing := newGraceStore()
	missing.probeErr = fmt.Errorf("column missing: %w", sdk.ErrNotFound)
	down := newGraceStore()
	down.probeErr = errors.New("dial tcp: connection refused")
	for name, tc := range map[string]struct {
		err  error
		want error
	}{
		"below a second":  {build(newGraceStore(), 500*time.Millisecond, enc), ErrRefreshReuseGraceInvalid},
		"above the bound": {build(newGraceStore(), MaxRefreshReuseGrace+time.Second, enc), ErrRefreshReuseGraceInvalid},
		"negative":        {build(newGraceStore(), -time.Second, enc), ErrRefreshReuseGraceInvalid},
		"no encrypter":    {build(newGraceStore(), 10*time.Second, nil), ErrRefreshReuseGraceRequires},
		"plain store":     {build(&constructorRepository{}, 10*time.Second, enc), ErrRefreshReuseGraceRequires},
		"unmigrated":      {build(missing, 10*time.Second, enc), ErrRefreshReuseGraceSchemaMissing},
		"store down":      {build(down, 10*time.Second, enc), sdk.ErrUnavailable},
	} {
		if !errors.Is(tc.err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, tc.err, tc.want)
		}
	}
	if err := build(&constructorRepository{}, 0, nil); err != nil {
		t.Fatalf("grace 0 on a plain store: %v", err)
	}
	if err := build(newGraceStore(), MaxRefreshReuseGrace, enc); err != nil {
		t.Fatalf("grace at the bound: %v", err)
	}
}

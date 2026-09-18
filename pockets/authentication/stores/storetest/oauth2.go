package storetest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// RunOAuth2 exercises the optional authorization-server transaction authority.
// Supply fresh disposable repositories for each invocation of newRepos.
func RunOAuth2(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("OAuth2", func(t *testing.T) {
		if r := newRepos(t); r.OAuth2 == nil || r.SessionManagement == nil {
			t.Skip("OAuth2/SessionManagement not wired — delegated persistence NOT verified")
		}
		for name, test := range map[string]func(*testing.T, auth.Repositories){
			"RoundTripAndIndependentBrowser":         testOAuth2RoundTrip,
			"BindingsAndSingleUse":                   testOAuth2CodeBindings,
			"GlobalRevisionFence":                    testOAuth2GlobalFence,
			"ConcurrentGlobalFence":                  testOAuth2ConcurrentGlobalFence,
			"RefreshHistoryAndIndependentRevocation": testOAuth2Refresh,
			"ConcurrentRefreshReuse":                 testOAuth2ConcurrentRefresh,
			"InventoryAndOwnerDeletion":              testOAuth2Inventory,
			"MetadataCopies":                         testOAuth2Clients,
		} {
			t.Run(name, func(t *testing.T) { test(t, newRepos(t)) })
		}
	})
}

type oauthFixture struct {
	r   auth.Repositories
	u   user.User
	w   session.Session
	now time.Time
}

func newOAuthFixture(t *testing.T, r auth.Repositories) oauthFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	u := seedDirectoryUser(t, r, "oauth-owner@example.test", now, now)
	w, _ := session.NewSession(u.ID, time.Hour, now)
	w.RefreshTokenHash = "web-hash-" + w.ID
	if _, err := r.Sessions.Create(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	return oauthFixture{r, u, w, now}
}

func (f oauthFixture) code(t *testing.T) oauth2.Code {
	t.Helper()
	id := (sdk.IDGenerator{}).MustGenerate()
	c := oauth2.Code{Hash: "code-hash-" + id, UserID: f.u.ID, AuthRevision: f.u.AuthRevision,
		Delegation:  session.Delegation{AuthRevision: f.u.AuthRevision, Issuer: "https://auth.example.test", ClientID: "https://client.example.test/metadata.json", Resource: "https://mcp.example.test", ExchangeResource: "https://api.example.test", ExchangeClientID: "mcp-server"},
		RedirectURI: "https://client.example.test/callback", CodeChallenge: "01234567890123456789012345678901234567890123456", CreatedAt: f.now, ExpiresAt: f.now.Add(time.Minute),
	}
	if err := f.r.OAuth2.CreateCode(context.Background(), c, f.w.ID); err != nil {
		t.Fatal(err)
	}
	return c
}

func (f oauthFixture) proposed(c oauth2.Code) session.Session {
	s, _ := session.NewSession(f.u.ID, time.Hour, f.now)
	s.Profile = session.ProfileDelegated
	s.Delegation = c.Delegation
	s.RefreshTokenHash = "refresh-hash-" + s.ID
	return s
}

func (f oauthFixture) grant(t *testing.T) session.Session {
	t.Helper()
	c := f.code(t)
	s, err := f.r.OAuth2.RedeemCode(context.Background(), c, f.proposed(c), f.now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testOAuth2RoundTrip(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	c := f.code(t)
	got, err := r.OAuth2.GetCode(context.Background(), c.Hash)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("code roundtrip: %+v %v", got, err)
	}
	if err := r.SessionManagement.DeleteForUser(context.Background(), f.w.ID, f.u.ID); err != nil {
		t.Fatal(err)
	}
	s := f.proposed(c)
	if _, err := r.OAuth2.RedeemCode(context.Background(), c, s, f.now); err != nil {
		t.Fatalf("ordinary browser logout revoked approval: %v", err)
	}
	stored, err := r.Sessions.Get(context.Background(), s.ID)
	if err != nil || stored.Profile != session.ProfileDelegated || stored.Delegation != c.Delegation || stored.RefreshTokenHash != s.RefreshTokenHash {
		t.Fatalf("grant binding lost: %+v %v", stored, err)
	}
	byHash, _, err := r.Sessions.GetByRefreshHash(context.Background(), s.RefreshTokenHash)
	if err != nil || byHash.Delegation != c.Delegation {
		t.Fatalf("refresh lookup lost profile: %+v %v", byHash, err)
	}
}

func testOAuth2CodeBindings(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	c := f.code(t)
	s := f.proposed(c)
	for _, change := range []func(*oauth2.Code, *session.Session){
		func(c *oauth2.Code, _ *session.Session) { c.RedirectURI = "https://attacker.test" },
		func(c *oauth2.Code, _ *session.Session) { c.CodeChallenge = "wrong" },
		func(_ *oauth2.Code, s *session.Session) { s.Profile = session.ProfileFirstParty },
		func(_ *oauth2.Code, s *session.Session) { s.UserID = "foreign" },
		func(_ *oauth2.Code, s *session.Session) { s.Delegation.Resource = "https://foreign.test" },
	} {
		badC, badS := c, s
		change(&badC, &badS)
		if _, err := r.OAuth2.RedeemCode(context.Background(), badC, badS, f.now); !errors.Is(err, oauth2.ErrInvalidGrant) {
			t.Fatalf("invalid binding: %v", err)
		}
	}
	if _, err := r.OAuth2.RedeemCode(context.Background(), c, s, f.now); err != nil {
		t.Fatalf("rejection consumed code: %v", err)
	}
	if _, err := r.OAuth2.RedeemCode(context.Background(), c, f.proposed(c), f.now); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("code replay: %v", err)
	}
}

func testOAuth2GlobalFence(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	a := f.grant(t)
	c := f.code(t)
	if err := r.SessionManagement.RevokeAllForUser(context.Background(), f.u.ID, f.now); err != nil {
		t.Fatal(err)
	}
	u, err := r.Users.Get(context.Background(), f.u.ID)
	if err != nil || u.AuthRevision != f.u.AuthRevision+1 {
		t.Fatalf("revision fence missing: %+v %v", u, err)
	}
	if _, err := r.OAuth2.RedeemCode(context.Background(), c, f.proposed(c), f.now); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("global revoke resurrected approval: %v", err)
	}
	for _, id := range []string{f.w.ID, a.ID} {
		if _, err := r.Sessions.Get(context.Background(), id); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("session survived global revoke: %v", err)
		}
	}
	c.Hash += "-later"
	c.AuthRevision = u.AuthRevision
	c.Delegation.AuthRevision = u.AuthRevision
	if err := r.OAuth2.CreateCode(context.Background(), c, f.w.ID); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("revoked browser approved new code: %v", err)
	}
}

func testOAuth2ConcurrentGlobalFence(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	c := f.code(t)
	s := f.proposed(c)
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; _, err := r.OAuth2.RedeemCode(context.Background(), c, s, f.now); errs <- err }()
	go func() { <-start; errs <- r.SessionManagement.RevokeAllForUser(context.Background(), f.u.ID, f.now) }()
	close(start)
	for range 2 {
		if err := <-errs; err != nil && !errors.Is(err, oauth2.ErrInvalidGrant) {
			t.Fatalf("race: %v", err)
		}
	}
	if _, err := r.Sessions.Get(context.Background(), s.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("session survived serialized global revoke: %v", err)
	}
}

func testOAuth2Refresh(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	a := f.grant(t)
	b := f.grant(t)
	ctx := context.Background()
	original := a.RefreshTokenHash
	for i := range 3 {
		next := a.RefreshTokenHash + "n"
		var err error
		a, err = r.OAuth2.RotateRefresh(ctx, oauth2.Refresh{Hash: a.RefreshTokenHash, NewHash: next, ClientID: a.Delegation.ClientID, Resource: a.Delegation.Resource, Now: f.now})
		if err != nil || a.RotationCount != i+1 || !a.ExpiresAt.Equal(f.now.Add(time.Hour)) {
			t.Fatalf("rotation: %+v %v", a, err)
		}
	}
	if err := r.OAuth2.Prune(ctx, f.now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.OAuth2.RotateRefresh(ctx, oauth2.Refresh{Hash: original, NewHash: "wrong-client-hash", ClientID: "foreign", Resource: a.Delegation.Resource, Now: f.now}); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("wrong client: %v", err)
	}
	if _, err := r.Sessions.Get(ctx, a.ID); err != nil {
		t.Fatalf("wrong client revoked A: %v", err)
	}
	if _, err := r.OAuth2.RotateRefresh(ctx, oauth2.Refresh{Hash: original, NewHash: "reuse-hash", ClientID: a.Delegation.ClientID, Resource: a.Delegation.Resource, Now: f.now}); !errors.Is(err, oauth2.ErrRefreshReuse) {
		t.Fatalf("old generation not detected: %v", err)
	}
	if _, err := r.Sessions.Get(ctx, a.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("reuse revocation rolled back: %v", err)
	}
	for _, id := range []string{f.w.ID, b.ID} {
		if _, err := r.Sessions.Get(ctx, id); err != nil {
			t.Fatalf("reuse crossed session boundary: %v", err)
		}
	}
	if err := r.OAuth2.RevokeRefresh(ctx, b.RefreshTokenHash, "foreign"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Sessions.Get(ctx, b.ID); err != nil {
		t.Fatal("foreign client revoked B")
	}
	if err := r.OAuth2.RevokeRefresh(ctx, b.RefreshTokenHash, b.Delegation.ClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Sessions.Get(ctx, b.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatal("B survived revoke")
	}
}

func testOAuth2ConcurrentRefresh(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	a := f.grant(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, suffix := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := r.OAuth2.RotateRefresh(context.Background(), oauth2.Refresh{Hash: a.RefreshTokenHash, NewHash: a.RefreshTokenHash + suffix, ClientID: a.Delegation.ClientID, Resource: a.Delegation.Resource, Now: f.now})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	success, reuse := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, oauth2.ErrRefreshReuse) {
			reuse++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || reuse != 1 {
		t.Fatalf("rotation results success=%d reuse=%d", success, reuse)
	}
	if _, err := r.Sessions.Get(context.Background(), a.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("concurrent reuse left session active: %v", err)
	}
}

func testOAuth2Inventory(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	a := f.grant(t)
	b := f.grant(t)
	ctx := context.Background()
	if err := r.SessionManagement.DeleteForUser(ctx, a.ID, "foreign"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("foreign deletion: %v", err)
	}
	p, err := r.SessionManagement.ListByUser(ctx, f.u.ID, list.Request{Limit: 1, WithCount: true})
	if err != nil || len(p.Items) != 1 || !p.HasMore || p.Total == nil || *p.Total != 3 {
		t.Fatalf("inventory: %+v %v", p, err)
	}
	p2, err := r.SessionManagement.ListByUser(ctx, f.u.ID, list.Request{Limit: 1, Cursor: p.NextCursor})
	if err != nil || len(p2.Items) != 1 || p.Items[0].ID == p2.Items[0].ID {
		t.Fatalf("inventory cursor: %+v %v", p2, err)
	}
	foreign, err := r.SessionManagement.ListByUser(ctx, "foreign", list.Request{})
	if err != nil || len(foreign.Items) != 0 {
		t.Fatalf("foreign inventory: %+v %v", foreign, err)
	}
	if err := r.SessionManagement.DeleteForUser(ctx, a.ID, f.u.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f.w.ID, b.ID} {
		if _, err := r.Sessions.Get(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	u, err := r.Users.Get(ctx, f.u.ID)
	if err != nil || u.AuthRevision != f.u.AuthRevision {
		t.Fatalf("single deletion advanced global revision: %+v %v", u, err)
	}
}

func testOAuth2Clients(t *testing.T, r auth.Repositories) {
	c := oauth2.Client{ID: "https://client.test/metadata.json", Name: "Client", RedirectURIs: []string{"https://client.test/callback"}, ExpiresAt: time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)}
	if err := r.OAuth2.PutClient(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	c.RedirectURIs[0] = "https://attacker.test"
	got, err := r.OAuth2.GetClient(context.Background(), c.ID)
	if err != nil || got.RedirectURIs[0] != "https://client.test/callback" {
		t.Fatalf("mutable metadata: %+v %v", got, err)
	}
	got.RedirectURIs[0] = "changed"
	got, err = r.OAuth2.GetClient(context.Background(), c.ID)
	if err != nil || got.RedirectURIs[0] != "https://client.test/callback" {
		t.Fatal("metadata read aliases stored state")
	}
}

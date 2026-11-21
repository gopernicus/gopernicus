package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// RunOAuth2RefreshGrace exercises the OPTIONAL refresh-reuse grace capability
// (oauth2.RefreshGraceRepository, migration 0031) over a MIGRATED store: a
// rotation records its time and successor ciphertext on the spent row; a
// spent presentation through RotateRefreshWithSuccessor commits nothing and
// reports ErrRefreshSpent, while the plain revoking path still works on the
// same rows; only the newest rotation keeps a ciphertext; a plain rotation
// records nothing; and Prune clears ciphertexts older than the maximum window
// without forgetting the spent hash. It runs outside Run and skips when the
// store does not implement the interface. The stores' own tests prove
// ProbeRefreshGraceSchema reports sdk.ErrNotFound on an unmigrated schema.
func RunOAuth2RefreshGrace(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("OAuth2RefreshGrace", func(t *testing.T) {
		r := newRepos(t)
		if r.OAuth2 == nil || r.SessionManagement == nil {
			t.Skip("OAuth2/SessionManagement not wired — refresh-reuse grace NOT verified")
		}
		if _, ok := r.OAuth2.(oauth2.RefreshGraceRepository); !ok {
			t.Skip("OAuth2 does not implement oauth2.RefreshGraceRepository — refresh-reuse grace NOT verified")
		}
		for name, test := range map[string]func(*testing.T, auth.Repositories){
			"ProbeMigrated":               testRefreshGraceProbe,
			"RecordsTheSuccessor":         testRefreshGraceRecords,
			"SpentCommitsNothing":         testRefreshGraceSpent,
			"OnlyTheNewestKeepsACipher":   testRefreshGraceNewestOnly,
			"PlainRotationRecordsNothing": testRefreshGracePlain,
			"PruneClearsOldCiphertexts":   testRefreshGracePrune,
		} {
			t.Run(name, func(t *testing.T) { test(t, newRepos(t)) })
		}
	})
}

func refreshGrace(r auth.Repositories) oauth2.RefreshGraceRepository {
	return r.OAuth2.(oauth2.RefreshGraceRepository)
}

// rotateWith rotates s's current token to next through the grace method,
// recording cipher at now.
func rotateWith(t *testing.T, r auth.Repositories, s session.Session, next, cipher string, now time.Time) session.Session {
	t.Helper()
	out, err := refreshGrace(r).RotateRefreshWithSuccessor(context.Background(),
		oauth2.Refresh{Hash: s.RefreshTokenHash, NewHash: next, ClientID: s.Delegation.ClientID, Resource: s.Delegation.Resource, Now: now}, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if out.RefreshTokenHash != next {
		t.Fatalf("rotated hash = %q, want %q", out.RefreshTokenHash, next)
	}
	return out
}

func testRefreshGraceProbe(t *testing.T, r auth.Repositories) {
	if err := refreshGrace(r).ProbeRefreshGraceSchema(context.Background()); err != nil {
		t.Fatalf("ProbeRefreshGraceSchema on a migrated store = %v, want nil", err)
	}
}

func testRefreshGraceRecords(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	a := f.grant(t)
	original := a.RefreshTokenHash
	rotateWith(t, r, a, "grace-next-"+a.ID, "cipher-1", f.now)
	got, err := refreshGrace(r).GetRefreshSuccessor(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != a.ID || !got.RotatedAt.Equal(f.now) || got.Ciphertext != "cipher-1" {
		t.Fatalf("successor = %+v, want session %s at %s with cipher-1", got, a.ID, f.now)
	}
	if _, err := refreshGrace(r).GetRefreshSuccessor(context.Background(), "grace-unknown-hash"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("unknown hash: err = %v, want ErrNotFound", err)
	}
	if _, err := refreshGrace(r).GetRefreshSuccessor(context.Background(), "grace-next-"+a.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("current hash: err = %v, want ErrNotFound", err)
	}
}

func testRefreshGraceSpent(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	f := newOAuthFixture(t, r)
	a := f.grant(t)
	original := a.RefreshTokenHash
	rotateWith(t, r, a, "grace-next-"+a.ID, "cipher-1", f.now)
	spent := oauth2.Refresh{Hash: original, NewHash: "grace-fork-" + a.ID, ClientID: a.Delegation.ClientID, Resource: a.Delegation.Resource, Now: f.now}
	if _, err := refreshGrace(r).RotateRefreshWithSuccessor(ctx, spent, "cipher-fork"); !errors.Is(err, oauth2.ErrRefreshSpent) {
		t.Fatalf("spent presentation: err = %v, want ErrRefreshSpent", err)
	}
	if got, err := r.Sessions.Get(ctx, a.ID); err != nil || got.RefreshTokenHash != "grace-next-"+a.ID {
		t.Fatalf("spent presentation changed the session: %+v %v", got, err)
	}
	wrong := spent
	wrong.ClientID = "foreign"
	if _, err := refreshGrace(r).RotateRefreshWithSuccessor(ctx, wrong, "cipher-fork"); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("spent presentation from another client: err = %v, want ErrInvalidGrant", err)
	}
	// The plain path still revokes on a row the grace method wrote.
	if _, err := r.OAuth2.RotateRefresh(ctx, oauth2.Refresh{Hash: original, ClientID: a.Delegation.ClientID, Resource: a.Delegation.Resource, Now: f.now}); !errors.Is(err, oauth2.ErrRefreshReuse) {
		t.Fatalf("plain reuse: err = %v, want ErrRefreshReuse", err)
	}
	if _, err := r.Sessions.Get(ctx, a.ID); err == nil {
		t.Fatal("plain reuse did not revoke the session")
	}
}

func testRefreshGraceNewestOnly(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	a := f.grant(t)
	first := a.RefreshTokenHash
	b := rotateWith(t, r, a, "grace-second-"+a.ID, "cipher-1", f.now)
	rotateWith(t, r, b, "grace-third-"+a.ID, "cipher-2", f.now.Add(time.Second))
	if _, err := refreshGrace(r).GetRefreshSuccessor(context.Background(), first); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("older rotation kept its ciphertext: err = %v", err)
	}
	got, err := refreshGrace(r).GetRefreshSuccessor(context.Background(), "grace-second-"+a.ID)
	if err != nil || got.Ciphertext != "cipher-2" {
		t.Fatalf("newest rotation = %+v %v, want cipher-2", got, err)
	}
}

func testRefreshGracePlain(t *testing.T, r auth.Repositories) {
	f := newOAuthFixture(t, r)
	a := f.grant(t)
	original := a.RefreshTokenHash
	if _, err := r.OAuth2.RotateRefresh(context.Background(), oauth2.Refresh{Hash: original, NewHash: "plain-next-" + a.ID, ClientID: a.Delegation.ClientID, Resource: a.Delegation.Resource, Now: f.now}); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshGrace(r).GetRefreshSuccessor(context.Background(), original); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("plain rotation recorded a successor: err = %v", err)
	}
}

func testRefreshGracePrune(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	f := newOAuthFixture(t, r)
	a := f.grant(t)
	original := a.RefreshTokenHash
	rotateWith(t, r, a, "grace-next-"+a.ID, "cipher-1", f.now)
	if err := r.OAuth2.Prune(ctx, f.now.Add(oauth2.MaxRefreshReuseGrace-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshGrace(r).GetRefreshSuccessor(ctx, original); err != nil {
		t.Fatalf("Prune inside the window cleared the ciphertext: %v", err)
	}
	if err := r.OAuth2.Prune(ctx, f.now.Add(oauth2.MaxRefreshReuseGrace)); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshGrace(r).GetRefreshSuccessor(ctx, original); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Prune past the window kept the ciphertext: err = %v", err)
	}
	if _, err := r.OAuth2.RotateRefresh(ctx, oauth2.Refresh{Hash: original, ClientID: a.Delegation.ClientID, Resource: a.Delegation.Resource, Now: f.now}); !errors.Is(err, oauth2.ErrRefreshReuse) {
		t.Fatalf("Prune forgot the spent hash: err = %v, want ErrRefreshReuse", err)
	}
}

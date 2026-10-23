package pgx

import (
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

func TestPersisted(t *testing.T) {
	nano := time.Date(2026, 10, 23, 12, 0, 0, 123456750, time.FixedZone("UTC-7", -7*3600))
	in := session.Session{CreatedAt: nano, ExpiresAt: nano.Add(time.Hour), Authentication: session.AuthenticationMetadata{AuthenticatedAt: nano}}

	got := persisted(in)
	for name, ts := range map[string]time.Time{"CreatedAt": got.CreatedAt, "ExpiresAt": got.ExpiresAt, "AuthenticatedAt": got.Authentication.AuthenticatedAt} {
		if ts.Nanosecond()%1000 != 0 || ts.Location() != time.UTC {
			t.Errorf("%s = %v, want UTC at microsecond precision", name, ts)
		}
	}
	if want := nano.Truncate(time.Microsecond); !got.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v (truncated, never rounded)", got.CreatedAt, want)
	}
	if got := persisted(session.Session{}); !got.Authentication.AuthenticatedAt.IsZero() {
		t.Errorf("zero AuthenticatedAt became %v, want zero", got.Authentication.AuthenticatedAt)
	}

	args, err := sessionArgs(in)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, _ := args["authenticated_at"].(*time.Time)
	for name, ts := range map[string]time.Time{"created_at": args["created_at"].(time.Time), "expires_at": args["expires_at"].(time.Time), "authenticated_at": *authenticated} {
		if ts.Nanosecond()%1000 != 0 {
			t.Errorf("sessionArgs %s = %v, want the truncated value encoded", name, ts)
		}
	}
}

// TestLive_SessionInsertsReturnStoredPrecision proves each insert path returns
// timestamps equal to what Get reads back for a sub-microsecond input.
func TestLive_SessionInsertsReturnStoredPrecision(t *testing.T) {
	_, repos, web, delegated := oauthPostgresFixture(t)
	ctx := t.Context()
	now := time.Now().Truncate(time.Microsecond).Add(750 * time.Nanosecond)

	check := func(path string, returned session.Session) {
		t.Helper()
		got, err := repos.Sessions.Get(ctx, returned.ID)
		if err != nil {
			t.Fatalf("%s: Get: %v", path, err)
		}
		if !returned.CreatedAt.Equal(got.CreatedAt) || !returned.ExpiresAt.Equal(got.ExpiresAt) || !returned.Authentication.AuthenticatedAt.Equal(got.Authentication.AuthenticatedAt) {
			t.Errorf("%s returned created=%v expires=%v authenticated=%v; Get read created=%v expires=%v authenticated=%v", path,
				returned.CreatedAt, returned.ExpiresAt, returned.Authentication.AuthenticatedAt,
				got.CreatedAt, got.ExpiresAt, got.Authentication.AuthenticatedAt)
		}
	}

	plain, _ := session.NewSession(web.UserID, time.Hour, now)
	plain.RefreshTokenHash = "precision-create"
	plain.Authentication.AuthenticatedAt = now
	created, err := repos.Sessions.Create(ctx, plain)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	check("Create", created)

	owner, err := repos.Users.Get(ctx, web.UserID)
	if err != nil {
		t.Fatal(err)
	}
	minted, _ := session.NewSession(owner.ID, time.Hour, now)
	minted.RefreshTokenHash = "precision-mint"
	minted.Authentication.AuthenticatedAt = now
	created, err = repos.ActiveSessions.CreateForActiveUser(ctx, minted, owner.AuthRevision)
	if err != nil {
		t.Fatalf("CreateForActiveUser: %v", err)
	}
	check("CreateForActiveUser", created)

	binding := delegated.Delegation
	binding.AuthRevision = owner.AuthRevision
	issued := now.Truncate(time.Microsecond)
	code := oauth2.Code{Hash: "precision-code", UserID: owner.ID, AuthRevision: owner.AuthRevision, Delegation: binding, RedirectURI: "https://client.example/callback", CodeChallenge: "s256-proof", CreatedAt: issued, ExpiresAt: issued.Add(time.Minute)}
	if err := repos.OAuth2.CreateCode(ctx, code, web.ID); err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
	proposed := delegated
	proposed.ID = "precision-redeemed"
	proposed.RefreshTokenHash = "precision-redeem-refresh"
	proposed.Delegation = binding
	proposed.CreatedAt, proposed.ExpiresAt = now, now.Add(time.Hour)
	proposed.Authentication.AuthenticatedAt = now
	redeemed, err := repos.OAuth2.RedeemCode(ctx, code, proposed, now)
	if err != nil {
		t.Fatalf("RedeemCode: %v", err)
	}
	check("RedeemCode", redeemed)
}

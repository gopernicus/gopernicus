//go:build live

package firebase

import (
	"context"
	"os"
	"testing"
	"time"

	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestLiveProject verifies one real ID token from one real Firebase project in
// production mode: signature against Google's keys, a minted session cookie,
// and revocation facts. Run with:
//
//	FIREBASE_PROJECT_ID=… GOOGLE_APPLICATION_CREDENTIALS=… FIREBASE_LIVE_ID_TOKEN=… go test -tags=live -run TestLiveProject ./...
//
// FIREBASE_LIVE_REVOKE=1 additionally calls RevokeRefreshTokens on the token's
// user (signing it out everywhere) and proves the token reads revoked.
func TestLiveProject(t *testing.T) {
	project := os.Getenv("FIREBASE_PROJECT_ID")
	token := os.Getenv("FIREBASE_LIVE_ID_TOKEN")
	if project == "" || token == "" || os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		t.Skip("SKIP (live Firebase project NOT verified): set FIREBASE_PROJECT_ID, GOOGLE_APPLICATION_CREDENTIALS and FIREBASE_LIVE_ID_TOKEN")
	}
	if os.Getenv(EmulatorHostEnv) != "" {
		t.Fatalf("%s is set; the live leg must talk to the real project", EmulatorHostEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := NewClient(ctx, ClientConfig{ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{ProjectID: project, Mode: environment.ModeProduction}
	idp, err := New(ctx, client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := NewSessionCookie(ctx, client, cfg)
	if err != nil {
		t.Fatal(err)
	}

	id, err := idp.Authenticate(ctx, sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: token})
	if err != nil {
		t.Fatalf("Authenticate(ID token): %v", err)
	}
	t.Logf("verified: iss=%s sub=%s auth_time=%v email_verified=%v", id.Issuer, id.Subject, id.AuthenticatedAt, id.EmailVerified)

	cookie, err := idp.MintSessionCookie(ctx, token, MinSessionCookieTTL)
	if err != nil {
		t.Fatalf("MintSessionCookie: %v", err)
	}
	cid, err := cookies.Authenticate(ctx, sdkauth.Credential{Transport: sdkauth.TransportCookie, Raw: cookie})
	if err != nil || cid.Subject != id.Subject {
		t.Fatalf("Authenticate(session cookie): id=%+v err=%v", cid, err)
	}

	facts, err := idp.CheckRevocation(ctx, idp.Issuer(), id.Subject)
	if err != nil {
		t.Fatalf("CheckRevocation: %v", err)
	}
	t.Logf("facts: disabled=%v valid_after=%v", facts.Disabled, facts.ValidAfter)

	if os.Getenv("FIREBASE_LIVE_REVOKE") != "1" {
		t.Log("SKIP revocation step (NOT verified live): set FIREBASE_LIVE_REVOKE=1 to revoke the user's refresh tokens")
		return
	}
	time.Sleep(1100 * time.Millisecond)
	if err := client.RevokeRefreshTokens(ctx, id.Subject); err != nil {
		t.Fatalf("RevokeRefreshTokens: %v", err)
	}
	facts, err = idp.CheckRevocation(ctx, idp.Issuer(), id.Subject)
	if err != nil {
		t.Fatal(err)
	}
	if !id.AuthenticatedAt.Before(facts.ValidAfter) {
		t.Fatalf("token minted before revocation not revoked by facts: auth %v, ValidAfter %v", id.AuthenticatedAt, facts.ValidAfter)
	}
}

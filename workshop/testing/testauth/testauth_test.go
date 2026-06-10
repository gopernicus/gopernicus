package testauth

import (
	"context"
	"testing"
)

func TestAuthenticatorRejectsMalformedAcceptsMinted(t *testing.T) {
	auth, signer := Authenticator("testapp")
	ctx := context.Background()

	if _, err := auth.AuthenticateJWT(ctx, "not-a-real-token"); err == nil {
		t.Fatal("malformed token must be rejected")
	}

	token := MintAccessToken(signer, "user-123")
	claims, err := auth.AuthenticateJWT(ctx, token)
	if err != nil {
		t.Fatalf("minted token must verify: %v", err)
	}
	if claims.UserID != "user-123" {
		t.Fatalf("user id = %q, want user-123", claims.UserID)
	}

	if got := auth.SessionTokenName(); got != "testapp_session" {
		t.Fatalf("session token name = %q, want testapp_session", got)
	}
}

func TestAuthorizerConstructs(t *testing.T) {
	if Authorizer() == nil {
		t.Fatal("Authorizer() returned nil")
	}
}

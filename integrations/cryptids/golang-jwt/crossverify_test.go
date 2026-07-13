package golangjwt_test

import (
	"testing"
	"time"

	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// These tests pin wire-format compatibility between the stdlib HS256 signer
// shipped in sdk/foundation/cryptids and the golang-jwt-backed connector. They
// live here, not in sdk, because sdk stays dependency-free even in tests: only
// this module may import golang-jwt.

// TestCrossVerifyGolangJWTVerifiesSDKMinted mints with the sdk HS256 signer and
// verifies with golang-jwt.
func TestCrossVerifyGolangJWTVerifiesSDKMinted(t *testing.T) {
	sdkSigner, err := cryptids.NewHS256([]byte(testSecret))
	if err != nil {
		t.Fatalf("NewHS256: %v", err)
	}
	gj := newSigner(t) // golang-jwt HS256, same secret

	expiresAt := time.Now().Add(time.Hour)
	token, err := sdkSigner.Sign(map[string]any{"user_id": "u123", "session_id": "s456"}, expiresAt)
	if err != nil {
		t.Fatalf("sdk Sign: %v", err)
	}

	claims, err := gj.Verify(token)
	if err != nil {
		t.Fatalf("golang-jwt Verify of sdk-minted token: %v", err)
	}
	if claims["user_id"] != "u123" {
		t.Errorf("user_id = %v, want u123", claims["user_id"])
	}
	if claims["session_id"] != "s456" {
		t.Errorf("session_id = %v, want s456", claims["session_id"])
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatal("exp claim missing or not a number")
	}
	if int64(exp) != expiresAt.Unix() {
		t.Errorf("exp = %d, want %d", int64(exp), expiresAt.Unix())
	}
}

// TestCrossVerifySDKVerifiesGolangJWTMinted mints with golang-jwt and verifies
// with the sdk HS256 signer.
func TestCrossVerifySDKVerifiesGolangJWTMinted(t *testing.T) {
	gj := newSigner(t) // golang-jwt HS256
	sdkSigner, err := cryptids.NewHS256([]byte(testSecret))
	if err != nil {
		t.Fatalf("NewHS256: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	token, err := gj.Sign(map[string]any{"user_id": "u789", "session_id": "s012"}, expiresAt)
	if err != nil {
		t.Fatalf("golang-jwt Sign: %v", err)
	}

	claims, err := sdkSigner.Verify(token)
	if err != nil {
		t.Fatalf("sdk Verify of golang-jwt-minted token: %v", err)
	}
	if claims["user_id"] != "u789" {
		t.Errorf("user_id = %v, want u789", claims["user_id"])
	}
	if claims["session_id"] != "s012" {
		t.Errorf("session_id = %v, want s012", claims["session_id"])
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatal("exp claim missing or not a number")
	}
	if int64(exp) != expiresAt.Unix() {
		t.Errorf("exp = %d, want %d", int64(exp), expiresAt.Unix())
	}
}

// TestCrossVerifyWrongSecretRejected confirms the two signers do not
// cross-verify under mismatched secrets — the operational truth behind §1.6
// (multi-instance hosts must share the secret).
func TestCrossVerifyWrongSecretRejected(t *testing.T) {
	const otherSecret = "a-completely-different-secret-32-chars"

	sdkSigner, err := cryptids.NewHS256([]byte(testSecret))
	if err != nil {
		t.Fatalf("NewHS256: %v", err)
	}
	gjOther, err := golangjwt.New(otherSecret)
	if err != nil {
		t.Fatalf("golangjwt.New: %v", err)
	}

	token, err := sdkSigner.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("sdk Sign: %v", err)
	}
	if _, err := gjOther.Verify(token); err == nil {
		t.Fatal("golang-jwt verified an sdk token signed with a different secret")
	}

	sdkOther, err := cryptids.NewHS256([]byte(otherSecret))
	if err != nil {
		t.Fatalf("NewHS256 other: %v", err)
	}
	gjToken, err := newSigner(t).Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("golang-jwt Sign: %v", err)
	}
	if _, err := sdkOther.Verify(gjToken); err == nil {
		t.Fatal("sdk verified a golang-jwt token signed with a different secret")
	}
}

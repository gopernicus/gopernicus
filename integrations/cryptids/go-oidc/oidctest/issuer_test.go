package oidctest_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/cryptids/go-oidc/oidctest"
)

type jwks struct {
	Keys []map[string]string `json:"keys"`
}

func start(t *testing.T, opts ...oidctest.Option) *oidctest.Issuer {
	t.Helper()
	iss, err := oidctest.Start(opts...)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(iss.Close)
	return iss
}

func getJSON(t *testing.T, url string, into any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if into != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

func decode(t *testing.T, token string) (header, payload map[string]any) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments", len(parts))
	}
	for i, into := range []*map[string]any{&header, &payload} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
	}
	return header, payload
}

// verifyWithJWKS checks the signature against the served key set using only
// the standard library, independent of the verifier under test.
func verifyWithJWKS(t *testing.T, iss *oidctest.Issuer, token string) bool {
	t.Helper()
	var set jwks
	getJSON(t, iss.JWKSURL(), &set)
	header, _ := decode(t, token)
	parts := strings.Split(token, ".")
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	b := func(s string) []byte { v, _ := base64.RawURLEncoding.DecodeString(s); return v }
	for _, k := range set.Keys {
		if k["kid"] != header["kid"] {
			continue
		}
		switch k["kty"] {
		case "RSA":
			pub := &rsa.PublicKey{N: new(big.Int).SetBytes(b(k["n"])), E: int(new(big.Int).SetBytes(b(k["e"])).Int64())}
			return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig) == nil
		case "EC":
			point := append([]byte{4}, append(b(k["x"]), b(k["y"])...)...)
			pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
			if err != nil || len(sig) != 64 {
				return false
			}
			return ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
		}
	}
	return false
}

func TestDiscoveryAndJWKS(t *testing.T) {
	iss := start(t)
	var doc map[string]any
	if code := getJSON(t, iss.Issuer()+oidctest.DiscoveryPath, &doc); code != http.StatusOK {
		t.Fatalf("discovery status = %d", code)
	}
	if doc["issuer"] != iss.Issuer() || doc["jwks_uri"] != iss.JWKSURL() {
		t.Fatalf("discovery = %v", doc)
	}
	var set jwks
	getJSON(t, iss.JWKSURL(), &set)
	if len(set.Keys) != 1 || set.Keys[0]["kid"] != iss.KeyID() || set.Keys[0]["alg"] != "RS256" {
		t.Fatalf("jwks = %v", set)
	}
	for _, k := range set.Keys {
		if _, private := k["d"]; private {
			t.Fatal("JWKS publishes a private exponent")
		}
	}
	if iss.JWKSRequests() != 1 {
		t.Fatalf("JWKSRequests = %d, want 1", iss.JWKSRequests())
	}
}

func TestMintDefaultsNeverOverride(t *testing.T) {
	iss := start(t)
	token, err := iss.Mint(map[string]any{"sub": "s"})
	if err != nil {
		t.Fatal(err)
	}
	header, claims := decode(t, token)
	if header["alg"] != "RS256" || header["kid"] != iss.KeyID() {
		t.Fatalf("header = %v", header)
	}
	if claims["iss"] != iss.Issuer() || claims["aud"] != oidctest.DefaultAudience || claims["iat"] == nil || claims["exp"] == nil {
		t.Fatalf("claims = %v", claims)
	}
	if !verifyWithJWKS(t, iss, token) {
		t.Fatal("signature does not verify against the served JWKS")
	}

	set := map[string]any{"iss": "https://other.test", "aud": []string{"x"}, "exp": 5, "iat": 4}
	token, _ = iss.Mint(set)
	_, claims = decode(t, token)
	if claims["iss"] != "https://other.test" || claims["exp"] != float64(5) || claims["iat"] != float64(4) {
		t.Fatalf("Mint overrode caller claims: %v", claims)
	}
	if _, mutated := set["sub"]; mutated || len(set) != 4 {
		t.Fatalf("Mint mutated the caller's map: %v", set)
	}

	token, _ = iss.Mint(map[string]any{"exp": oidctest.Omit, "aud": oidctest.Omit})
	_, claims = decode(t, token)
	if _, ok := claims["exp"]; ok {
		t.Fatalf("Omit left exp: %v", claims)
	}
	if _, ok := claims["aud"]; ok {
		t.Fatalf("Omit left aud: %v", claims)
	}
}

func TestMintAsSecondIssuer(t *testing.T) {
	iss := start(t)
	token, err := iss.MintAs("https://session.example.test/project", map[string]any{"sub": "s"})
	if err != nil {
		t.Fatal(err)
	}
	_, claims := decode(t, token)
	if claims["iss"] != "https://session.example.test/project" {
		t.Fatalf("iss = %v", claims["iss"])
	}
	if !verifyWithJWKS(t, iss, token) {
		t.Fatal("second-issuer token is not signed by the published key")
	}
}

func TestES256(t *testing.T) {
	iss := start(t, oidctest.WithAlgorithm("ES256"), oidctest.WithAudience("api"))
	token, err := iss.Mint(map[string]any{"sub": "s"})
	if err != nil {
		t.Fatal(err)
	}
	header, claims := decode(t, token)
	if header["alg"] != "ES256" || claims["aud"] != "api" {
		t.Fatalf("header = %v claims = %v", header, claims)
	}
	if !verifyWithJWKS(t, iss, token) {
		t.Fatal("ES256 signature does not verify")
	}
	if _, err := oidctest.New("https://x.test", oidctest.WithAlgorithm("HS256")); err == nil {
		t.Fatal("HS256 accepted")
	}
	if _, err := oidctest.New(""); err == nil {
		t.Fatal("empty issuer accepted")
	}
}

func TestRotateRetireUnpublished(t *testing.T) {
	iss := start(t)
	first := iss.KeyID()
	second, err := iss.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if iss.KeyID() != second || second == first {
		t.Fatalf("KeyID = %s after Rotate, want %s", iss.KeyID(), second)
	}
	var set jwks
	getJSON(t, iss.JWKSURL(), &set)
	if len(set.Keys) != 2 {
		t.Fatalf("published keys = %d, want 2", len(set.Keys))
	}
	if err := iss.Retire(second); err == nil {
		t.Fatal("retiring the current key succeeded")
	}
	if err := iss.Retire(first); err != nil {
		t.Fatal(err)
	}
	set = jwks{}
	getJSON(t, iss.JWKSURL(), &set)
	if len(set.Keys) != 1 || set.Keys[0]["kid"] != second {
		t.Fatalf("jwks after Retire = %v", set)
	}

	unpublished, err := iss.MintWithUnpublishedKey(second, map[string]any{"sub": "s"})
	if err != nil {
		t.Fatal(err)
	}
	if verifyWithJWKS(t, iss, unpublished) {
		t.Fatal("an unpublished key's token verifies against the JWKS")
	}
}

func TestUnsignedAndOutage(t *testing.T) {
	iss := start(t)
	token, err := iss.MintUnsigned(map[string]any{"sub": "s"})
	if err != nil {
		t.Fatal(err)
	}
	header, _ := decode(t, token)
	if header["alg"] != "none" || !strings.HasSuffix(token, ".") {
		t.Fatalf("unsigned token = %s", token)
	}

	iss.SetJWKSAvailable(false)
	if code := getJSON(t, iss.JWKSURL(), nil); code != http.StatusServiceUnavailable {
		t.Fatalf("JWKS status during outage = %d", code)
	}
	iss.SetJWKSAvailable(true)
	if code := getJSON(t, iss.JWKSURL(), nil); code != http.StatusOK {
		t.Fatalf("JWKS status after outage = %d", code)
	}
	if iss.JWKSRequests() != 2 {
		t.Fatalf("JWKSRequests = %d, want 2", iss.JWKSRequests())
	}
}

func TestNewServesUnderCallerMux(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	iss, err := oidctest.New(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/", iss.Handler())
	iss.Close()
	if iss.Issuer() != srv.URL {
		t.Fatalf("Issuer = %q, want the trailing slash trimmed", iss.Issuer())
	}
	var set jwks
	if code := getJSON(t, iss.JWKSURL(), &set); code != http.StatusOK || len(set.Keys) != 1 {
		t.Fatalf("JWKS via caller mux: status %d keys %d", code, len(set.Keys))
	}
}

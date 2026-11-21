package oauth2

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// loopbackService is a production-mode service whose one public client
// registers the port-less loopback redirects a native CIMD client declares.
func loopbackService(t *testing.T, h *assuranceHarness, registered ...string) *Service {
	t.Helper()
	resolver := clientResolverFunc(func(_ context.Context, id string) (Client, error) {
		return Client{ID: id, Name: "Native client", RedirectURIs: registered}, nil
	})
	svc, err := New(Repositories{OAuth2: plainCodes{st: h.st}, Users: assuranceUsers{}, Sessions: assuranceSessions{h.st}},
		h.signer, resolver, assuranceVerifier{h.signer}, environment.ModeProduction, protocolConfig())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func loopbackRequest(svc *Service, redirect string) (AuthorizationRequest, string) {
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	return AuthorizationRequest{ResponseType: "code", ClientID: denialClientID, RedirectURI: redirect, Resource: svc.config.MCPResource,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]), CodeChallengeMethod: "S256"}, verifier
}

// TestLoopbackRedirectAnyPortInProduction pins RFC 8252 §7.3 (#379): a
// registered loopback http redirect is accepted in production and matches any
// request port, while scheme, host, path and query stay exact, and the token
// request stays bound to the exact authorize value, port included.
func TestLoopbackRedirectAnyPortInProduction(t *testing.T) {
	h := newAssuranceHarness(aal2Approval())
	svc := loopbackService(t, h, "http://localhost/callback", "http://127.0.0.1/callback", "http://[::1]/callback")

	for _, redirect := range []string{"http://localhost:54321/callback", "http://localhost/callback", "http://127.0.0.1:3118/callback", "http://[::1]:8080/callback"} {
		req, _ := loopbackRequest(svc, redirect)
		if _, err := svc.validateAuthorization(t.Context(), req); err != nil {
			t.Fatalf("%s refused: %v", redirect, err)
		}
	}
	for _, redirect := range []string{
		"http://localhost:54321/callbackx",    // path
		"http://localhost:54321/callback/",    // path
		"https://localhost:54321/callback",    // scheme
		"http://localhost:54321/callback?x=1", // query
		"http://localhost:54321/callback?",    // empty query marker
		"http://localhost:0/callback",         // port 0
		"http://localhost:65536/callback",     // port range
		"http://localhost:08080/callback",     // leading zero
		"http://LOCALHOST:5000/callback",      // case
		"http://example.com:5000/callback",    // not loopback
		"http://localhost:5000/callback#f",    // fragment
		"http://user@localhost:5000/callback", // userinfo
	} {
		req, _ := loopbackRequest(svc, redirect)
		if _, err := svc.validateAuthorization(t.Context(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s: err=%v, want ErrInvalidRequest", redirect, err)
		}
	}

	// localhost and the IP literals are different hosts; never resolved.
	only127 := loopbackService(t, newAssuranceHarness(aal2Approval()), "http://127.0.0.1/callback")
	if req, _ := loopbackRequest(only127, "http://localhost:5000/callback"); func() bool { _, err := only127.validateAuthorization(t.Context(), req); return err == nil }() {
		t.Fatal("localhost matched a 127.0.0.1 registration")
	}

	// Full flow: the code binds the exact authorize redirect, port included.
	authorized := "http://localhost:54321/callback"
	req, verifier := loopbackRequest(svc, authorized)
	prepared, err := svc.PrepareAuthorization(t.Context(), req, h.web.UserID, h.web.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RedirectURI != authorized {
		t.Fatalf("consent carries %q, want the requested redirect %q", prepared.RedirectURI, authorized)
	}
	approved, err := svc.Approve(t.Context(), prepared.ConsentToken, h.web.UserID, h.web.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.RedirectURI != authorized {
		t.Fatalf("approval redirects to %q, want %q", approved.RedirectURI, authorized)
	}
	redeem := CodeRequest{Code: approved.Code, ClientID: denialClientID, RedirectURI: "http://localhost:54322/callback", Resource: svc.config.MCPResource, CodeVerifier: verifier}
	if _, err := svc.RedeemCode(t.Context(), redeem); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("redeem on another port: err=%v, want ErrInvalidGrant", err)
	}
	redeem.RedirectURI = authorized
	tokens, err := svc.RedeemCode(t.Context(), redeem)
	if err != nil || tokens.RefreshToken == "" {
		t.Fatalf("redeem on the authorize port: %v", err)
	}
}

// TestHTTPSRedirectStillExact pins that the any-port rule is loopback-only:
// an https registration matches byte for byte, port included, and a
// non-loopback http registration is refused in production.
func TestHTTPSRedirectStillExact(t *testing.T) {
	h := newAssuranceHarness(aal2Approval())
	svc := loopbackService(t, h, "https://client.example:8443/callback")
	for redirect, ok := range map[string]bool{
		"https://client.example:8443/callback": true,
		"https://client.example/callback":      false,
		"https://client.example:9443/callback": false,
	} {
		req, _ := loopbackRequest(svc, redirect)
		if _, err := svc.validateAuthorization(t.Context(), req); (err == nil) != ok {
			t.Fatalf("%s: err=%v, want ok=%v", redirect, err, ok)
		}
	}
	plain := loopbackService(t, newAssuranceHarness(aal2Approval()), "http://client.example/callback")
	req, _ := loopbackRequest(plain, "http://client.example/callback")
	if _, err := plain.validateAuthorization(t.Context(), req); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("non-loopback http registration: err=%v, want ErrInvalidClient", err)
	}
}

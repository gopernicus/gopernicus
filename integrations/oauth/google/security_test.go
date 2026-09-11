package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
)

type auditTransport func(*http.Request) (*http.Response, error)

func (f auditTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResponseValidationAndSafeErrors(t *testing.T) {
	p, _ := newFakeGoogle(t, nil)
	for _, test := range []struct {
		name, body string
		status     int
		token      bool
	}{
		{"missing token", `{}`, 200, true},
		{"missing identity", `{}`, 200, false},
		{"oauth failure in success", `{"error":"invalid_grant","error_description":"SECRET-MARKER"}`, 200, true},
		{"upstream failure", `SECRET-MARKER`, 500, true},
		{"redirect", `SECRET-MARKER`, 307, true},
		{"overflow after valid JSON", `{"access_token":"tok","token_type":"Bearer"}` + strings.Repeat(" ", maxResponseBody), 200, true},
		{"negative expiry", `{"access_token":"tok","token_type":"Bearer","expires_in":-1}`, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p.client.Transport = auditTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), Request: r}, nil
			})
			var err error
			if test.token {
				_, err = p.ExchangeCode(context.Background(), "code", strings.Repeat("v", 43), "https://app.example/callback")
			} else {
				_, err = p.GetUserInfo(context.Background(), "token")
			}
			if err == nil || strings.Contains(err.Error(), "SECRET-MARKER") {
				t.Fatalf("expected safe failure, got %v", err)
			}
			if test.status != 200 {
				var response *oauth.Error
				if !errors.As(err, &response) || response.StatusCode != test.status {
					t.Fatalf("HTTP status unavailable: %v", err)
				}
			}
		})
	}
}

func TestCredentialRequestsNeverFollowRedirects(t *testing.T) {
	p, _ := newFakeGoogle(t, nil)
	calls := 0
	p.client.Transport = auditTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"https://foreign.example/steal"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	_, err := p.ExchangeCode(context.Background(), "code", strings.Repeat("v", 43), "https://app.example/callback")
	if err == nil || calls != 1 {
		t.Fatalf("redirect followed: calls=%d err=%v", calls, err)
	}
}

func TestTransportCancellationPreserved(t *testing.T) {
	p, _ := newFakeGoogle(t, nil)
	p.client.Transport = auditTransport(func(*http.Request) (*http.Response, error) { return nil, context.Canceled })
	_, err := p.ExchangeCode(context.Background(), "code", strings.Repeat("v", 43), "https://app.example/callback")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestSignedIdentityAssurance(t *testing.T) {
	p, fake := newFakeGoogle(t, nil)
	for _, test := range []struct {
		name, sub, email, hd          string
		verified, authoritative, fail bool
	}{
		{name: "missing subject", email: "user@gmail.com", verified: true, fail: true},
		{name: "linked subject without email", sub: "stable"},
		{name: "third party email", sub: "stable", email: "user@example.com", verified: true},
		{name: "Gmail", sub: "stable", email: "user@gmail.com", verified: true, authoritative: true},
		{name: "Workspace", sub: "stable", email: "user@example.com", hd: "example.com", verified: true, authoritative: true},
		{name: "unverified", sub: "stable", email: "user@gmail.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"iss": fake.server.URL, "aud": testClientID, "sub": test.sub, "exp": time.Now().Add(time.Hour).Unix(), "email": test.email, "email_verified": test.verified, "hd": test.hd, "nonce": "nonce"})
			token := fake.signIDToken(t, string(raw))
			claims, err := p.ValidateIDToken(context.Background(), token, "nonce")
			if test.fail {
				if err == nil {
					t.Fatal("missing subject accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if claims.EmailVerified != test.verified || claims.EmailAuthoritative != test.authoritative {
				t.Fatalf("incorrect assurance: %+v", claims)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := p.ValidateIDToken(ctx, token, "nonce"); !errors.Is(err, context.Canceled) {
				t.Fatalf("cached verifier ignored cancellation: %v", err)
			}
		})
	}
}

func TestDiscoveryResponseOverflow(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]string{"issuer": server.URL, "jwks_uri": server.URL + "/keys", "ignored": "SECRET-MARKER"})
		w.Write(body)
		io.WriteString(w, strings.Repeat(" ", maxResponseBody))
	}))
	defer server.Close()
	_, err := newProvider(context.Background(), Config{ClientID: testClientID, HTTPClient: server.Client()}, endpoints{issuer: server.URL})
	if err == nil || strings.Contains(err.Error(), "SECRET-MARKER") {
		t.Fatalf("oversized discovery accepted or disclosed: %v", err)
	}
	var failure *oauth.Error
	if !errors.As(err, &failure) || failure.Operation != "OIDC discovery" {
		t.Fatalf("discovery cause unavailable: %v", err)
	}
}

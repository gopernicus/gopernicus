package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
)

type auditTransport func(*http.Request) (*http.Response, error)

func (f auditTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResponseValidationAndSafeErrors(t *testing.T) {
	p, _ := newFakeGitHub(t, nil)
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
	p, _ := newFakeGitHub(t, nil)
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
	p, _ := newFakeGitHub(t, nil)
	p.client.Transport = auditTransport(func(*http.Request) (*http.Response, error) { return nil, context.Canceled })
	_, err := p.ExchangeCode(context.Background(), "code", strings.Repeat("v", 43), "https://app.example/callback")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

type failingEmailBody struct{}

func (failingEmailBody) Read([]byte) (int, error) { return 0, context.Canceled }
func (failingEmailBody) Close() error             { return nil }

func TestOptionalEmailPermissionKeepsTransportFailures(t *testing.T) {
	p, _ := newFakeGitHub(t, nil)
	for _, test := range []struct {
		name      string
		body      io.ReadCloser
		wantError bool
	}{
		{"permission denied", io.NopCloser(strings.NewReader(`{"message":"permission denied"}`)), false},
		{"read canceled", failingEmailBody{}, true},
		{"oversized denial", io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBody+1))), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p.client.Transport = auditTransport(func(r *http.Request) (*http.Response, error) {
				status := 403
				body := test.body
				if r.URL.Path == "/user" {
					status = 200
					body = io.NopCloser(strings.NewReader(`{"id":123,"email":"unverified@example.com"}`))
				}
				return &http.Response{StatusCode: status, Body: body, Header: make(http.Header), Request: r}, nil
			})
			info, err := p.GetUserInfo(context.Background(), "token")
			if (err != nil) != test.wantError {
				t.Fatalf("info=%+v err=%v", info, err)
			}
			if test.name == "read canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if err == nil && (info.ProviderUserID != "123" || info.EmailVerified) {
				t.Fatalf("optional email changed stable identity/assurance: %+v", info)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bcrypt "github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
	"github.com/gopernicus/gopernicus/sdk"
)

// Exercise the host configuration, actual hashing/signing adapters, JSON routes,
// and authentication middleware together over HTTP with in-memory repositories.
func TestHostPasswordPolicyAndJWTOverHTTP(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", strings.Repeat("j", 32)) // Disposable fixture key.
	const password = "host-choice"                       // Below the default length.
	tooLong := strings.Repeat("🔑", 19)                   // 19 code points, 76 bytes.
	svc := bootInProcess(t, &recordingSender{}, func(cfg *authenticationConfig) {
		cfg.Hasher = bcrypt.New(bcrypt.WithCost(4))
		cfg.RequireVerifiedEmail = false
		cfg.AllowedOrigins = []string{"http://test.local"}
		cfg.ValidatePassword = func(_ context.Context, candidate string) error {
			if candidate == password || candidate == tooLong {
				return nil
			}
			return fmt.Errorf("host password rule: %w", sdk.ErrInvalidInput)
		}
	})
	mux := http.NewServeMux()
	mux.Handle("/", mountInProcess(t, svc))
	mux.Handle("/probe", svc.HTTP.RequireAccessToken()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := sdk.PrincipalFromContext(r.Context())
		if !ok || principal.Type != sdk.PrincipalTypeUser || principal.ID == "" {
			http.Error(w, "missing caller", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := server.Client()
	request := func(method, path string, body map[string]string, bearer string) *http.Response {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://test.local")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	registered := request(http.MethodPost, "/auth/register", map[string]string{
		"email": "host@example.com", "password": password, "display_name": "Host User",
	}, "")
	if registered.StatusCode != http.StatusCreated {
		t.Fatalf("host-approved registration status = %d, want 201", registered.StatusCode)
	}
	issued := request(http.MethodPost, "/auth/token", map[string]string{
		"email": "host@example.com", "password": password,
	}, "")
	if issued.StatusCode != http.StatusOK {
		t.Fatalf("token issuance status = %d, want 200", issued.StatusCode)
	}
	var pair struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(issued.Body).Decode(&pair); err != nil || pair.AccessToken == "" {
		t.Fatalf("decode token response: %v", err)
	}
	if response := request(http.MethodGet, "/probe", nil, pair.AccessToken); response.StatusCode != http.StatusNoContent {
		t.Fatalf("signed-token authentication status = %d, want 204", response.StatusCode)
	}
	if response := request(http.MethodGet, "/probe", nil, pair.AccessToken+"x"); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered-token status = %d, want 401", response.StatusCode)
	}
	for name, candidate := range map[string]string{
		"host rejection": "default-valid-but-host-refused",
		"bcrypt limit":   tooLong,
	} {
		response := request(http.MethodPost, "/auth/register", map[string]string{
			"email": "rejected@example.com", "password": candidate, "display_name": "Rejected",
		}, "")
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", name, response.StatusCode)
		}
	}
}

package turso

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestConfiguredCredentialReachesDriverUnchanged(t *testing.T) {
	for _, query := range []string{"", "?authToken=old", "?auth_token=old", "?jwt=old", "?authToken=first&authToken=second"} {
		t.Run(query, func(t *testing.T) {
			const token = "new+token&with=reserved%characters#here"
			headers := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers <- r.Header.Get("Authorization")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"fixture refusal"}`))
			}))
			defer server.Close()
			db, err := Open(context.Background(), Config{URL: server.URL + query, AuthToken: token})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(context.Background(), "SELECT 1"); err == nil {
				t.Fatal("expected the fixture's refusal")
			}
			select {
			case got := <-headers:
				if got != "Bearer "+token {
					t.Fatalf("driver sent %q, want exact configured token", got)
				}
			default:
				t.Fatal("driver made no HTTP request")
			}
		})
	}
}

func TestOpenRejectsMalformedCredentialURL(t *testing.T) {
	for _, url := range []string{
		"libsql://example.test?authToken=secret%zz",
		"libsql://example.test?authToken=secret;invalid",
		"libsql://example.test#fragment",
		"libsql://user:secret@%zz",
	} {
		cfg := Config{URL: url, AuthToken: "configured"}
		db, err := Open(context.Background(), cfg)
		if db != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Open = %v, %v, want invalid input", db, err)
		}
		if cfg.Redacted() != redactedDSN {
			t.Fatal("malformed configuration must be fully redacted")
		}
	}
}

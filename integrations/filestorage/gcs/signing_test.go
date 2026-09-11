package gcs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/sdk"
)

type tokenProviderFunc func(context.Context) (*auth.Token, error)

func (fn tokenProviderFunc) Token(ctx context.Context) (*auth.Token, error) { return fn(ctx) }

func TestSignedURLIAMUsesCallerContextAndExplicitAccount(t *testing.T) {
	type requestKey struct{}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), requestKey{}, "request"), time.Minute)
	defer cancel()
	calls := 0
	st := testStore(t, Config{Prefix: "tenant", SigningServiceAccount: "signer@example.invalid"}, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Host != "iamcredentials.googleapis.com" || r.URL.Path != "/v1/projects/-/serviceAccounts/signer@example.invalid:signBlob" {
			t.Errorf("IAM target: %s %s", r.Method, r.URL)
		}
		deadline, _ := r.Context().Deadline()
		want, _ := ctx.Deadline()
		if deadline != want || r.Context().Value(requestKey{}) != "request" {
			t.Error("IAM lost caller context")
		}
		var body struct {
			Payload string `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		payload, err := base64.StdEncoding.DecodeString(body.Payload)
		if err != nil || !strings.HasPrefix(string(payload), "GOOG4-RSA-SHA256\n") {
			t.Errorf("payload=%q err=%v", payload, err)
		}
		return response(r, 200, `{"signedBlob":"AQID"}`), nil
	})
	signed, err := st.SignedURL(ctx, "dir/object", time.Minute)
	if err != nil || !strings.Contains(signed, "/test-bucket/tenant/dir/object") || !strings.Contains(signed, "X-Goog-Signature=010203") {
		t.Fatalf("signed URL=%q err=%v", signed, err)
	}
	if calls != 1 {
		t.Errorf("IAM calls=%d", calls)
	}
}

func TestSignedURLIAMCancellationAndProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
		status int
		body   string
	}{
		{"canceled success", true, 200, `{"signedBlob":"AQID"}`},
		{"forbidden", false, 403, `{"error":{"code":403,"message":"forbidden"}}`},
		{"empty signature", false, 200, `{}`},
		{"invalid signature", false, 200, `{"signedBlob":"not base64"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st := testStore(t, Config{SigningServiceAccount: "explicit@example.invalid"}, func(r *http.Request) (*http.Response, error) {
				if tc.cancel {
					cancel()
				}
				return response(r, tc.status, tc.body), nil
			})
			signed, err := st.SignedURL(ctx, "key", time.Second)
			if err == nil || signed != "" {
				t.Fatalf("signed=%q err=%v", signed, err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("context lost: %v", err)
			}
			if tc.status == 403 {
				var provider *googleapi.Error
				if !errors.As(err, &provider) || provider.Code != 403 {
					t.Fatalf("provider lost: %v", err)
				}
			}
		})
	}
}

func TestSignedURLCredentialRefreshUsesCallerContext(t *testing.T) {
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	called := false
	creds := auth.NewCredentials(&auth.CredentialsOptions{TokenProvider: tokenProviderFunc(func(tokenCtx context.Context) (*auth.Token, error) {
		called = true
		deadline, ok := tokenCtx.Deadline()
		want, _ := ctx.Deadline()
		if !ok || deadline != want {
			t.Error("credential refresh lost signing deadline")
		}
		cancel()
		// Return before any HTTP request can reach the network. These credentials
		// are synthetic and deliberately never produce a usable access token.
		return nil, tokenCtx.Err()
	})})
	st, err := Open(context.Background(), Config{Bucket: "bucket", SigningServiceAccount: "signer@example.invalid"}, WithClientOption(option.WithAuthCredentials(creds)))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if called {
		t.Fatal("Open unexpectedly requested an access token")
	}
	signed, err := st.SignedURL(ctx, "key", time.Second)
	if !called || !errors.Is(err, context.Canceled) || signed != "" {
		t.Fatalf("called=%v signed=%q err=%v", called, signed, err)
	}
}

func TestSignedURLRequiresExplicitSigningConfiguration(t *testing.T) {
	st := testStore(t, Config{}, func(*http.Request) (*http.Response, error) {
		t.Error("unexpected identity lookup")
		return nil, errors.New("unexpected network")
	})
	if _, err := st.SignedURL(context.Background(), "key", time.Second); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing signing config: %v", err)
	}
}

// The conformance leg hits a live Google Cloud Storage API. Run it against a
// throwaway fake-gcs-server (no real credentials, no cloud spend):
//
//	docker run --rm -d -p 4443:4443 fsouza/fake-gcs-server -scheme http -public-host localhost:4443
//	GCS_TEST_BUCKET=conformance \
//	  GCS_TEST_ENDPOINT=http://localhost:4443/storage/v1/ \
//	  go test ./...
//
// or against real GCS with a bucket you own:
//
//	GCS_TEST_BUCKET=my-bucket \
//	  GCS_TEST_CREDENTIALS_JSON="$(cat sa-key.json)" \
//	  go test ./...
//
// Absent GCS_TEST_BUCKET the conformance leg skips LOUDLY — a silent green here
// would claim filestorage.Storer conformance with nothing verified — so
// `make check` stays hermetic while the env-gated leg proves the live contract.
package gcs_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/integrations/filestorage/gcs"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage/filestoragetest"
)

// Store honors the core port plus both optional capabilities GCS can back.
var (
	_ filestorage.Storer            = (*gcs.Store)(nil)
	_ filestorage.ResumableUploader = (*gcs.Store)(nil)
	_ filestorage.SignedURLer       = (*gcs.Store)(nil)
)

// TestCapabilities asserts, at run time as well as compile time, that a GCS
// Store advertises both optional protocols. Disk's tests assert their absence.
func TestCapabilities(t *testing.T) {
	var s filestorage.Storer = &gcs.Store{}
	if _, ok := s.(filestorage.ResumableUploader); !ok {
		t.Error("*gcs.Store does not implement filestorage.ResumableUploader")
	}
	if _, ok := s.(filestorage.SignedURLer); !ok {
		t.Error("*gcs.Store does not implement filestorage.SignedURLer")
	}
}

// TestSignedURLHermetic proves the SignedURLer path signs locally with a private
// key and no network round trip: it constructs a Store from a self-generated
// service-account key (a real RSA key, a fake project) and checks the minted V4
// URL. This runs offline in `make check`; it never touches GCS.
func TestSignedURLHermetic(t *testing.T) {
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	saJSON := generateServiceAccountJSON(t)
	noNetwork := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		t.Error("local signing attempted a network request")
		return nil, errors.New("unexpected network")
	})}

	st, err := gcs.Open(context.Background(), gcs.Config{
		Bucket:          "test-bucket",
		CredentialsJSON: saJSON,
	}, gcs.WithClientOption(option.WithoutAuthentication(), option.WithHTTPClient(noNetwork)))
	if err != nil {
		t.Fatalf("Open with generated credentials: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	url, err := st.SignedURL(context.Background(), "dir/object.txt", time.Hour)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}

	for _, want := range []string{
		"test-bucket/dir/object.txt",
		"X-Goog-Algorithm=GOOG4-RSA-SHA256",
		"X-Goog-Signature=",
		"X-Goog-Expires=",
	} {
		if !strings.Contains(url, want) {
			t.Errorf("signed URL missing %q\ngot: %s", want, url)
		}
	}

	t.Run("vendor credentials need explicit signing config", func(t *testing.T) {
		st, err := gcs.Open(context.Background(), gcs.Config{Bucket: "bucket"}, gcs.WithClientOption(
			option.WithCredentialsJSON([]byte(saJSON)), option.WithoutAuthentication(), option.WithHTTPClient(noNetwork)))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.SignedURL(context.Background(), "key", time.Second); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("vendor-only signing: %v", err)
		}
	})
	t.Run("explicit IAM wins over local private key", func(t *testing.T) {
		calls := 0
		hc := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if !strings.HasSuffix(r.URL.Path, "/selected@example.invalid:signBlob") {
				t.Errorf("IAM target=%s", r.URL)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"signedBlob":"AQID"}`)), Request: r}, nil
		})}
		st, err := gcs.Open(context.Background(), gcs.Config{Bucket: "bucket", CredentialsJSON: saJSON, SigningServiceAccount: "selected@example.invalid"},
			gcs.WithClientOption(option.WithoutAuthentication(), option.WithHTTPClient(hc)))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		signed, err := st.SignedURL(context.Background(), "key", time.Second)
		if err != nil || calls != 1 || !strings.Contains(signed, "X-Goog-Signature=010203") {
			t.Fatalf("calls=%d signed=%q err=%v", calls, signed, err)
		}
	})
}

type testTransport func(*http.Request) (*http.Response, error)

func (fn testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

// TestConformance_GCS runs the shared filestorage.Storer conformance suite
// against a live GCS API (fake-gcs-server or real). Each newStorer gets a unique
// key prefix so a subtest's objects never collide with another's on the shared
// bucket.
func TestConformance_GCS(t *testing.T) {
	bucket := os.Getenv("GCS_TEST_BUCKET")
	if bucket == "" {
		t.Skip("GCS_TEST_BUCKET not set — gcs filestorage conformance NOT verified (make check stays hermetic; see this file's header to run it)")
	}
	endpoint := os.Getenv("GCS_TEST_ENDPOINT")
	creds := os.Getenv("GCS_TEST_CREDENTIALS_JSON")

	ctx := context.Background()

	// Emulator mode: create the bucket up front (idempotent). Real GCS expects a
	// pre-existing bucket the operator owns, so creation is emulator-only.
	if endpoint != "" {
		ensureEmulatorBucket(t, ctx, bucket, endpoint)
	}

	filestoragetest.Run(t, func(t *testing.T) filestorage.Storer {
		prefix := "conformance/" + rand.Text() + "/"
		st, err := gcs.Open(ctx, gcs.Config{
			Bucket:          bucket,
			Prefix:          prefix,
			Endpoint:        endpoint,
			CredentialsJSON: creds,
		})
		if err != nil {
			t.Fatalf("gcs.Open(bucket=%s): %v", bucket, err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	})
}

// ensureEmulatorBucket creates the conformance bucket on a fake-gcs-server,
// tolerating an already-existing bucket. It fails loudly (never skips) once the
// emulator endpoint is set.
func ensureEmulatorBucket(t *testing.T, ctx context.Context, bucket, endpoint string) {
	t.Helper()
	project := os.Getenv("GCS_TEST_PROJECT")
	if project == "" {
		project = "test-project"
	}
	client, err := gcsstorage.NewClient(ctx,
		option.WithEndpoint(endpoint),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("emulator client: %v", err)
	}
	defer client.Close()

	err = client.Bucket(bucket).Create(ctx, project, nil)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "exist") {
		t.Fatalf("create emulator bucket %q: %v", bucket, err)
	}
}

// generateServiceAccountJSON builds a minimal but structurally valid
// service-account key with a freshly generated RSA private key, enough for local
// V4 signing.
func generateServiceAccountJSON(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	sa := map[string]string{
		"type":         "service_account",
		"project_id":   "test-project",
		"private_key":  string(pemBytes),
		"client_email": "signer@test-project.iam.gserviceaccount.com",
		"client_id":    "1234567890",
		"token_uri":    "https://oauth2.googleapis.com/token",
	}
	out, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal sa json: %v", err)
	}
	return string(out)
}

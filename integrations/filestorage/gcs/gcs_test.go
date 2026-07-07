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
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/integrations/filestorage/gcs"
	"github.com/gopernicus/gopernicus/sdk/filestorage"
	"github.com/gopernicus/gopernicus/sdk/filestorage/filestoragetest"
)

// Store honors the core port plus both optional capabilities GCS can back.
var (
	_ filestorage.Storer            = (*gcs.Store)(nil)
	_ filestorage.ResumableUploader = (*gcs.Store)(nil)
	_ filestorage.SignedURLer       = (*gcs.Store)(nil)
)

// TestCapabilities asserts, at run time as well as compile time, that a GCS
// Store advertises the two optional capabilities — the mirror image of the sdk
// suite's RunOptionalCapabilityAbsent (which is only for backends like Disk that
// implement neither).
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
	saJSON := generateServiceAccountJSON(t)

	st, err := gcs.Open(context.Background(), gcs.Config{
		Bucket:          "test-bucket",
		CredentialsJSON: saJSON,
	})
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
}

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
		prefix := fmt.Sprintf("conformance/%d/", time.Now().UnixNano())
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

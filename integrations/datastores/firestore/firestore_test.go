package firestore_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestOpenRejectsEmptyProject proves the wiring bug is caught at the composition
// root, with a sentinel a host can match, not a driver error later at query time.
func TestOpenRejectsEmptyProject(t *testing.T) {
	db, err := firestore.Open(context.Background(), firestore.Config{})
	if db != nil {
		t.Fatalf("Open returned a DB for an empty project id")
	}
	if !errors.Is(err, firestore.ErrNoProjectID) {
		t.Errorf("Open error = %v, want ErrNoProjectID", err)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("Open error = %v, want it to wrap sdk.ErrInvalidInput", err)
	}
}

// TestRedacted pins the two invariants of the log-safe target string: it names
// the project and database, and it never leaks credential bytes.
func TestRedacted(t *testing.T) {
	cases := []struct {
		name string
		cfg  firestore.Config
		want string
	}{
		{
			name: "default database",
			cfg:  firestore.Config{ProjectID: "gopernicus-test"},
			want: "projects/gopernicus-test/databases/(default)",
		},
		{
			name: "named database",
			cfg:  firestore.Config{ProjectID: "gopernicus-test", DatabaseID: "ci-42"},
			want: "projects/gopernicus-test/databases/ci-42",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Redacted(); got != tc.want {
				t.Errorf("Redacted() = %q, want %q", got, tc.want)
			}
		})
	}

	secret := "super-secret-private-key-material"
	cfg := firestore.Config{
		ProjectID:       "gopernicus-test",
		CredentialsJSON: []byte(`{"private_key":"` + secret + `"}`),
	}
	if got := cfg.Redacted(); strings.Contains(got, secret) {
		t.Errorf("Redacted() leaked credential material: %q", got)
	}
}

// TestOpenWithCredentialsIsNotEmulated builds a client from a self-generated
// service-account key: hermetic (client construction issues no RPC, the zero
// Retry skips boot validation) and it proves Emulated() is false off the
// emulator — the branch the index probe depends on.
func TestOpenWithCredentialsIsNotEmulated(t *testing.T) {
	// Cleared explicitly: the emulator leg exports this for the whole run, and
	// an empty value is "unset" to the vendor client.
	t.Setenv("FIRESTORE_EMULATOR_HOST", "")

	db, err := firestore.Open(context.Background(), firestore.Config{
		ProjectID:       "gopernicus-test",
		DatabaseID:      "ci-42",
		CredentialsJSON: serviceAccountJSON(t),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if db.Emulated() {
		t.Error("Emulated() = true without FIRESTORE_EMULATOR_HOST")
	}
	if got, want := db.Target(), "projects/gopernicus-test/databases/ci-42"; got != want {
		t.Errorf("Target() = %q, want %q", got, want)
	}
	if got, want := db.Collection("users").Path, "projects/gopernicus-test/databases/ci-42/documents/users"; got != want {
		t.Errorf("Collection path = %q, want %q", got, want)
	}
	if got, want := db.Doc("users", "u1").Path, "projects/gopernicus-test/databases/ci-42/documents/users/u1"; got != want {
		t.Errorf("Doc path = %q, want %q", got, want)
	}
}

// TestOpenEmulatedReportsEmulated proves Emulated() observes the environment the
// vendor client acted on. The address is never dialed (gRPC dials lazily and the
// zero Retry skips boot validation), so this stays hermetic.
func TestOpenEmulatedReportsEmulated(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")

	db, err := firestore.Open(context.Background(), firestore.Config{ProjectID: "gopernicus-test"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if !db.Emulated() {
		t.Error("Emulated() = false under FIRESTORE_EMULATOR_HOST")
	}
}

// TestConfigStringIsRedacted is the C8 fold of "a Config must not be printable
// into a log". Redacted() only helps a caller who remembers to call it; %v,
// %+v, and slog's attribute rendering all reach for String() first, and without
// one they print every field — including CredentialsJSON's bytes.
func TestConfigStringIsRedacted(t *testing.T) {
	const secret = "-----BEGIN PRIVATE KEY-----super-secret-key-material"
	cfg := firestore.Config{
		ProjectID:       "gopernicus-test",
		DatabaseID:      "ci-42",
		CredentialsJSON: []byte(`{"private_key":"` + secret + `"}`),
	}

	for _, format := range []string{"%v", "%+v", "%s"} {
		got := fmt.Sprintf(format, cfg)
		if strings.Contains(got, secret) {
			t.Errorf("fmt.Sprintf(%q, cfg) leaked the key: %q", format, got)
		}
		if strings.Contains(got, "CredentialsJSON") {
			t.Errorf("fmt.Sprintf(%q, cfg) = %q, want Redacted()'s target string — a field dump is one struct change away from leaking", format, got)
		}
		if got != cfg.Redacted() {
			t.Errorf("fmt.Sprintf(%q, cfg) = %q, want %q", format, got, cfg.Redacted())
		}
	}

	// The same rule through a wrapped error, which is how a boot failure is
	// actually written.
	if got := fmt.Errorf("verifying %+v", cfg).Error(); strings.Contains(got, secret) {
		t.Errorf("a wrapped error leaked the key: %q", got)
	}

	// slog reaches for String() through its Any value, the shape a host uses.
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("boot", "cfg", cfg)
	if strings.Contains(buf.String(), secret) {
		t.Errorf("slog leaked the key: %q", buf.String())
	}
}

// TestCollectionGroupBuildsAGroupQuery proves the C8 addition is the
// collection-GROUP scope (every collection with that id, at any depth) and not
// a second spelling of Collection. Hermetic: building a reference is no I/O,
// and the vendor's own serialization is the only handle on a Query's private
// clauses.
func TestCollectionGroupBuildsAGroupQuery(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")

	db, err := firestore.Open(context.Background(), firestore.Config{ProjectID: "gopernicus-test"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	group := db.CollectionGroup("sessions")
	if group == nil {
		t.Fatal("CollectionGroup returned nil")
	}

	groupBytes, err := group.Where("user_id", "==", "u1").Serialize()
	if err != nil {
		t.Fatalf("serializing the group query: %v", err)
	}
	topBytes, err := db.Collection("sessions").Where("user_id", "==", "u1").Serialize()
	if err != nil {
		t.Fatalf("serializing the collection query: %v", err)
	}
	if bytes.Equal(groupBytes, topBytes) {
		t.Error("the collection-group query serialized identically to the top-level one — it is not group-scoped")
	}
	if !bytes.Contains(groupBytes, []byte("sessions")) {
		t.Error("the collection-group query does not name the collection id")
	}
}

// TestStatusCheckClassifiesFailures proves a failing health check hands the host
// an sdk-classified error rather than a raw gRPC status: a health endpoint and
// Open's boot validation both branch on those sentinels.
func TestStatusCheckClassifiesFailures(t *testing.T) {
	// Port 1 refuses the connection, so the round trip fails for real without a
	// server. The zero Retry keeps Open itself from making the trip.
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")

	db, err := firestore.Open(context.Background(), firestore.Config{ProjectID: "gopernicus-test"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = firestore.StatusCheck(ctx, db)
	if err == nil {
		t.Fatal("StatusCheck against an unreachable endpoint returned nil")
	}
	if !sdk.IsExpected(err) {
		t.Fatalf("StatusCheck error = %v (%T), want an sdk-classified error", err, err)
	}
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Errorf("StatusCheck error = %v, want sdk.ErrUnavailable — an unreachable database is a transport failure", err)
	}
}

// serviceAccountJSON mints a real RSA key in a service-account key document, so
// credential parsing runs for real without a network round trip or a checked-in
// secret (the posture integrations/filestorage/gcs takes in its hermetic leg).
func serviceAccountJSON(t *testing.T) []byte {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	doc, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "gopernicus-test",
		"private_key":  string(pemBytes),
		"client_email": "test@gopernicus-test.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshaling service account: %v", err)
	}
	return doc
}

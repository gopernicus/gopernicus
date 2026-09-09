// Package firestoretest opens Firestore test databases for the connector and
// for the pocket stores built on it. It is a TEST-ONLY helper package that
// compiles without a build tag — the tagged tests that call it live in other
// packages and modules (net/http/httptest's posture), so gating this package
// itself would make those files unbuildable.
//
// There are two factories, deliberately separate, and neither can turn into the
// other:
//
//   - Open/OpenDatabase/Reset target the EMULATOR. They require
//     FIRESTORE_EMULATOR_HOST and skip loudly without it. Reset clears the whole
//     selected database through the emulator's DELETE endpoint and REFUSES to
//     run against anything that is not the emulator.
//
//   - OpenLive/ResetLive target a REAL Firestore database
//     (FIRESTORE_LIVE_PROJECT_ID + FIRESTORE_LIVE_DATABASE_ID). They refuse an
//     emulator endpoint and refuse the default database, and ResetLive deletes
//     only the collections it is named, through the connector — it never calls
//     the emulator endpoint, which does not exist in production and whose
//     production analogue would be "delete everything".
//
// Missing live configuration SKIPS an ordinary run loudly and FAILS it when
// FIRESTORE_LIVE_REQUIRED=1 — the release gate, so a release train cannot pass
// by silently skipping the only leg that proves production behavior.
//
// # The emulator isolation contract (read this before writing a store harness)
//
// Reset clears the WHOLE selected database — not a collection, not a fixture.
// Two suites sharing a database therefore delete each other's rows, and the
// failure is a flake ("a document that existed a moment ago is gone"), not an
// error anyone can read. Two rules follow, and neither is optional:
//
//  1. Tests that call Open or Reset must NOT run in parallel with each other
//     across packages. One emulator, one database, one clock — and go test runs
//     different PACKAGES concurrently by default. Either give the package its
//     own database (rule 2) or run those packages with -p 1.
//  2. Every pocket store train opens its OWN database:
//     OpenDatabase(t, "authorization") in pockets/authorization/stores/firestore,
//     OpenDatabase(t, "authentication") in its authentication twin. The
//     emulator serves named databases side by side and the clear endpoint is
//     scoped to one of them, so the two store suites and this connector's own
//     suite cannot clobber one another even when they run at the same time.
//
// Open itself stays on the database named by FIRESTORE_DATABASE_ID (the default
// database when it is unset), which is what lets a CI job point a whole run at
// one named database without editing a test.
package firestoretest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// The environment this package reads. They are exported so a CI workflow and a
// store's harness name the same strings the code does.
const (
	// EmulatorHostEnv is the vendor client's own emulator switch: setting it
	// routes every client to the emulator with insecure credentials.
	EmulatorHostEnv = "FIRESTORE_EMULATOR_HOST"
	// ProjectEnv overrides the emulator project id.
	ProjectEnv = "FIRESTORE_PROJECT_ID"
	// DatabaseEnv selects the emulator database Open uses. Unset means the
	// default database. It moves a whole run onto one named database without
	// touching a test; OpenDatabase's explicit argument ignores it, because a
	// suite that named its own database means it.
	DatabaseEnv = "FIRESTORE_DATABASE_ID"
	// LiveProjectEnv names the Google Cloud project holding the live test database.
	LiveProjectEnv = "FIRESTORE_LIVE_PROJECT_ID"
	// LiveDatabaseEnv names the run-owned database inside that project.
	LiveDatabaseEnv = "FIRESTORE_LIVE_DATABASE_ID"
	// LiveRequiredEnv, set to "1", turns a missing/unusable live configuration
	// from a skip into a failure.
	LiveRequiredEnv = "FIRESTORE_LIVE_REQUIRED"
)

// DefaultProject is the emulator project id used when ProjectEnv is unset. It
// matches the project the plan's emulator container starts with.
const DefaultProject = "gopernicus-test"

// openTimeout and resetTimeout bound the factory's own round trips.
const (
	openTimeout  = 15 * time.Second
	resetTimeout = 30 * time.Second
)

// resetPageSize is how many documents ResetLive deletes per query page.
const resetPageSize = 500

// skipNoEmulator is the loud skip message. A silent green here would claim
// emulator conformance with nothing verified.
const skipNoEmulator = EmulatorHostEnv + " not set — Firestore emulator conformance NOT verified"

// EmulatorHost returns the configured emulator endpoint with any scheme
// stripped (the shape the emulator's REST endpoints want), and whether one is
// configured at all.
func EmulatorHost() (string, bool) {
	return emulatorHost(os.Getenv(EmulatorHostEnv))
}

// Open opens the emulator database named by FIRESTORE_DATABASE_ID — the
// default database when that is unset — skipping loudly when no emulator is
// configured. The returned DB is closed by t.Cleanup.
//
// Everything Open returns SHARES one database with every other Open in the run,
// so a suite that calls Reset must own its database instead: see the isolation
// contract on the package, and call OpenDatabase(t, "<module-name>").
func Open(t testing.TB) *firestore.DB {
	t.Helper()
	return OpenDatabase(t, EmulatorDatabase())
}

// EmulatorDatabase returns the database id Open selects: DatabaseEnv, or ""
// (the default database) when it is unset.
func EmulatorDatabase() string {
	return emulatorDatabase(os.Getenv(DatabaseEnv))
}

// OpenDatabase opens a named database on the emulator, skipping loudly when no
// emulator is configured. An empty databaseID selects the default database, and
// FIRESTORE_DATABASE_ID is NOT consulted — a caller that named a database is
// asking for that one.
//
// This is the isolation seam: the emulator serves named databases side by side
// and Reset's clear endpoint is scoped to one of them, so every pocket store
// train opens its own — OpenDatabase(t, "authorization"),
// OpenDatabase(t, "authentication") — and no suite can clear another's rows.
func OpenDatabase(t testing.TB, databaseID string) *firestore.DB {
	t.Helper()

	if _, ok := EmulatorHost(); !ok {
		t.Skip(skipNoEmulator)
	}

	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()

	db, err := firestore.Open(ctx, firestore.Config{
		ProjectID:      EmulatorProject(),
		DatabaseID:     databaseID,
		ConnectTimeout: openTimeout,
		// Attempts > 1 makes Open validate the connection for real, so a
		// stopped emulator fails HERE, naming the endpoint, instead of in the
		// first assertion of some unrelated test.
		Retry: firestore.RetryPolicy{Attempts: 3, MinBackoff: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("firestoretest: opening the emulator at %s: %v", os.Getenv(EmulatorHostEnv), err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// EmulatorProject returns the emulator project id: ProjectEnv, or DefaultProject.
func EmulatorProject() string {
	return emulatorProject(os.Getenv(ProjectEnv))
}

// Reset clears every document in db's database through the emulator's clear
// endpoint (DELETE /emulator/v1/projects/{p}/databases/{d}/documents), scoped to
// the database db is bound to. It is the emulator's own bulk delete: no
// collection list to keep in sync, and no risk of a half-cleared fixture.
//
// WHOLE DATABASE. Not this test's collections, not this package's — every
// document the database holds. A suite that resets shares that blast radius
// with anything else pointed at the same database, so it either opens its own
// (OpenDatabase(t, "<module-name>")) or runs without package-level parallelism.
// See the isolation contract on the package.
//
// It REFUSES, loudly, if db is not an emulator client. That refusal is the
// whole safety story of this function: the same call shape against a real
// project would be "delete the database's contents".
func Reset(t testing.TB, db *firestore.DB) {
	t.Helper()

	host, ok := EmulatorHost()
	if !ok {
		t.Fatalf("firestoretest: Reset called with %s unset", EmulatorHostEnv)
	}
	if reason := resetGuard(db.Emulated(), db.Target()); reason != "" {
		t.Fatal(reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()

	url := "http://" + host + "/emulator/v1/" + db.Target() + "/documents"
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("firestoretest: building the reset request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("firestoretest: clearing %s: %v", db.Target(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("firestoretest: clearing %s returned %s", db.Target(), resp.Status)
	}
}

// emulatorHost strips any scheme from a FIRESTORE_EMULATOR_HOST value, exactly
// as the vendor client does, and reports whether one was configured.
func emulatorHost(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if _, rest, found := strings.Cut(value, "://"); found {
		value = rest
	}
	return strings.TrimSuffix(value, "/"), value != ""
}

// emulatorDatabase resolves the emulator database id from DatabaseEnv's value:
// blank (or whitespace) means the default database, which firestore.Config
// spells as an empty DatabaseID. Pure, so the selection is testable without an
// emulator.
func emulatorDatabase(value string) string {
	return strings.TrimSpace(value)
}

// emulatorProject resolves the emulator project id from ProjectEnv's value.
func emulatorProject(value string) string {
	if value == "" {
		return DefaultProject
	}
	return value
}

// resetGuard returns the refusal message for an emulator Reset, or "" when the
// target is safe to clear wholesale. Pure, so the refusal is testable without a
// live client.
func resetGuard(emulated bool, target string) string {
	if !emulated {
		return fmt.Sprintf("firestoretest: Reset refuses %s — it is not an emulator client, and clearing a real database is never a test's job", target)
	}
	return ""
}

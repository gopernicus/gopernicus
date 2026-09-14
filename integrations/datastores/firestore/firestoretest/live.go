package firestoretest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// liveResetTimeout bounds one ResetLive sweep over all its collections.
const liveResetTimeout = 5 * time.Minute

// OpenLive opens the live Firestore test database named by LiveProjectEnv and
// LiveDatabaseEnv, using Application Default Credentials. The returned DB is
// closed by t.Cleanup.
//
// It refuses every unsafe target rather than degrading: an emulator endpoint in
// the environment (which would silently make a "live" run prove nothing) and
// the project's default database (which is a real host's database, not a
// disposable one) are both errors, never a fallback.
//
// When the configuration is missing or refused, the run SKIPS loudly — unless
// FIRESTORE_LIVE_REQUIRED=1, which turns it into a failure naming exactly what
// is missing. That is the release gate: a train cannot go out because the only
// leg that proves production behavior quietly did not run.
func OpenLive(t testing.TB) *firestore.DB {
	t.Helper()

	target, reason := liveTarget(os.Getenv)
	if reason != "" {
		if liveRequired(os.Getenv) {
			t.Fatalf("firestoretest: %s is set and the live database is unusable: %s", LiveRequiredEnv, reason)
		}
		t.Skipf("firestoretest: %s — live Firestore behavior NOT verified", reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()

	db, err := firestore.Open(ctx, firestore.Config{
		ProjectID:      target.project,
		DatabaseID:     target.database,
		ConnectTimeout: openTimeout,
		Retry:          firestore.RetryPolicy{Attempts: 3, MinBackoff: 250 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("firestoretest: opening the live database projects/%s/databases/%s: %v", target.project, target.database, err)
	}
	t.Cleanup(func() { db.Close() })

	// Belt to the environment's suspenders: whatever the environment said, the
	// client itself must not be an emulator client.
	if db.Emulated() {
		t.Fatalf("firestoretest: OpenLive produced an EMULATOR client for %s — refusing to call it a live run", db.Target())
	}
	return db
}

// ResetLive deletes every document in the named collections of a live test
// database, through the connector (query a page, delete it, repeat). It is the
// live counterpart of Reset, and it is deliberately NOT the same mechanism:
//
//   - it never calls the emulator's clear endpoint, which has no production
//     analogue short of emptying the database;
//   - it touches ONLY the collections it is named, so a shared project cannot be
//     collaterally cleared;
//   - it refuses an emulator client and the default database.
//
// Naming no collection is an error: a reset that clears nothing before a
// conformance run is a false green waiting to happen.
func ResetLive(t testing.TB, db *firestore.DB, collections ...string) {
	t.Helper()

	if reason := resetLiveGuard(db.Emulated(), db.Target(), collections); reason != "" {
		t.Fatal(reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), liveResetTimeout)
	defer cancel()

	for _, collection := range collections {
		if err := clearCollection(ctx, db, collection); err != nil {
			t.Fatalf("firestoretest: clearing %s in %s: %v", collection, db.Target(), err)
		}
	}
}

// clearCollection deletes a collection's documents a page at a time through the
// connector's Reader/Writer, so the sweep obeys the same mediation discipline
// (and the same error mapping) as the stores under test.
func clearCollection(ctx context.Context, db *firestore.DB, collection string) error {
	reader, writer := db.ReaderFrom(ctx), db.WriterFrom(ctx)
	// Select() with no fields asks the server for document names only.
	query := db.Collection(collection).Query.Select().Limit(resetPageSize)

	for {
		refs, err := pageRefs(ctx, reader, query)
		if err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		for _, ref := range refs {
			if err := writer.Delete(ctx, ref); err != nil {
				return fmt.Errorf("deleting %s: %w", ref.Path, err)
			}
		}
		if len(refs) < resetPageSize {
			return nil
		}
	}
}

// pageRefs reads one page of document references, stopping the iterator and
// mapping its errors at the iteration boundary (the connector's mediation
// discipline, applied by its own test helper).
func pageRefs(ctx context.Context, reader firestore.Reader, query gcfs.Query) ([]*gcfs.DocumentRef, error) {
	it := reader.Documents(ctx, query)
	defer it.Stop()

	var refs []*gcfs.DocumentRef
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return refs, nil
		}
		if err != nil {
			return nil, firestore.MapError(err)
		}
		refs = append(refs, snap.Ref)
	}
}

// liveConfig is a resolved live target.
type liveConfig struct {
	project  string
	database string
}

// liveTarget resolves the live database from the environment, returning a
// refusal reason instead of a target when the configuration is missing or
// unsafe. Pure (env is injected), so every decision below is testable without a
// project, credentials, or a network.
//
// Order matters: the emulator check comes FIRST, because a run with both
// emulator and live variables set is ambiguous, and resolving that ambiguity in
// favor of "live" would report emulator results as production evidence.
func liveTarget(env func(string) string) (liveConfig, string) {
	if host := strings.TrimSpace(env(EmulatorHostEnv)); host != "" {
		return liveConfig{}, fmt.Sprintf("%s is set (%s): the live factory refuses to run against the emulator", EmulatorHostEnv, host)
	}

	project := strings.TrimSpace(env(LiveProjectEnv))
	database := strings.TrimSpace(env(LiveDatabaseEnv))
	switch {
	case project == "" && database == "":
		return liveConfig{}, fmt.Sprintf("%s and %s are not set", LiveProjectEnv, LiveDatabaseEnv)
	case project == "":
		return liveConfig{}, fmt.Sprintf("%s is not set", LiveProjectEnv)
	case database == "":
		return liveConfig{}, fmt.Sprintf("%s is not set", LiveDatabaseEnv)
	case database == firestore.DefaultDatabase:
		return liveConfig{}, fmt.Sprintf("%s is %s: the live factory refuses the default database — point it at a disposable, run-owned database", LiveDatabaseEnv, firestore.DefaultDatabase)
	}
	return liveConfig{project: project, database: database}, ""
}

// liveRequired reports whether missing live configuration must fail the run
// rather than skip it.
func liveRequired(env func(string) string) bool {
	return strings.TrimSpace(env(LiveRequiredEnv)) == "1"
}

// resetLiveGuard returns the refusal message for a live reset, or "" when the
// target and collection list are safe. Pure, for the same reason as resetGuard.
func resetLiveGuard(emulated bool, target string, collections []string) string {
	if emulated {
		return fmt.Sprintf("firestoretest: ResetLive refuses %s — it is an emulator client; use Reset", target)
	}
	if strings.HasSuffix(target, "/"+firestore.DefaultDatabase) {
		return fmt.Sprintf("firestoretest: ResetLive refuses %s — the default database is a real host's database, not a disposable test one", target)
	}
	if len(collections) == 0 {
		return "firestoretest: ResetLive was named no collections — a reset that clears nothing is a false green"
	}
	for _, collection := range collections {
		if strings.TrimSpace(collection) == "" {
			return "firestoretest: ResetLive was named an empty collection"
		}
	}
	return ""
}

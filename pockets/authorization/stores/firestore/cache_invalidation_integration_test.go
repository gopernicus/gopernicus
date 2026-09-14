//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"math"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
)

func newCacheFixture(t *testing.T, options ...Option) (*firestoredb.DB, authorization.Repositories) {
	t.Helper()
	db := firestoretest.OpenDatabase(t, "authorization-cache")
	firestoretest.Reset(t, db)
	if err := InitializeCacheInvalidation(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(t.Context(), db, append([]Option{WithoutIndexProbe(), WithCacheReads()}, options...)...)
	if err != nil {
		t.Fatal(err)
	}
	return db, repos
}

func TestCacheReadSnapshots(t *testing.T) {
	storetest.RunReadSnapshots(t, func(t *testing.T) authorization.Repositories { _, repos := newCacheFixture(t); return repos })
}

func TestCacheInvalidationActivationAndWriters(t *testing.T) {
	db := firestoretest.OpenDatabase(t, "authorization-cache")
	firestoretest.Reset(t, db)
	ctx := t.Context()
	plain, err := Repositories(ctx, db, WithoutIndexProbe())
	if err != nil {
		t.Fatal(err)
	}
	if plain.CacheSource != nil {
		t.Fatal("default exposed cache")
	}
	if err := plain.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "uncached", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReaderFrom(ctx).Get(ctx, db.Doc(collectionCacheInvalidation, "head")); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("default writer created metadata: %v", err)
	}
	if _, err := Repositories(ctx, db, WithoutIndexProbe(), WithCacheReads()); !errors.Is(err, decisions.ErrCacheVersion) {
		t.Fatalf("missing head: %v", err)
	}
	if err := InitializeCacheInvalidation(ctx, db); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(ctx, db, WithoutIndexProbe(), WithCacheReads())
	if err != nil {
		t.Fatal(err)
	}
	version := func() decisions.CacheVersion {
		t.Helper()
		v, err := repos.CacheSource.Observe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	initial := version()
	if err := InitializeCacheInvalidation(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got := version(); got != initial {
		t.Fatal("initialization reset head")
	}
	if _, err := RelationshipRepository(ctx, db, WithoutIndexProbe(), WithCacheReads()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("single constructor accepted reads: %v", err)
	}
	writer, err := Repositories(ctx, db, WithoutIndexProbe(), WithCacheInvalidation())
	if err != nil {
		t.Fatal(err)
	}
	if writer.CacheSource != nil {
		t.Fatal("writer-only exposed source")
	}
	tuple := relationships.CreateRelationship{ResourceType: "document", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"}
	if err := writer.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{tuple}); err != nil {
		t.Fatal(err)
	}
	first := version()
	if first.Generation <= initial.Generation {
		t.Fatal("nil-cacher tuple writer did not advance head")
	}
	if err := writer.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{tuple}); err != nil {
		t.Fatal(err)
	}
	if got := version(); got != first {
		t.Fatal("duplicate tuple advanced head")
	}
	assignment := roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "viewer"}
	if err := writer.Roles.Assign(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	second := version()
	if second.Generation <= first.Generation {
		t.Fatal("role writer did not advance head")
	}
	if err := writer.Roles.Assign(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	if got := version(); got != second {
		t.Fatal("duplicate role advanced head")
	}
	assertAmbientRefusal(t, db, repos)
	if err := db.ReadSnapshot(ctx, func(ctx context.Context, _ firestoredb.Reader) error {
		if repos.CacheSource.CacheableContext(ctx) {
			t.Fatal("ambient cache allowed")
		}
		if _, err := repos.CacheSource.Observe(ctx); !errors.Is(err, ErrAmbientTransactionUnsupported) {
			t.Fatalf("ambient observe: %v", err)
		}
		if err := repos.CacheSource.ReadSnapshot(ctx, func(context.Context, decisions.CacheVersion, decisions.CheckReads) error {
			t.Fatal("ambient callback ran")
			return nil
		}); !errors.Is(err, ErrAmbientTransactionUnsupported) {
			t.Fatalf("ambient snapshot: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCacheInvalidationInvalidHeadAbortsFacts(t *testing.T) {
	for _, head := range []any{nil, cacheHead{Protocol: 1, Epoch: "0123456789abcdef0123456789abcdef", Generation: math.MaxInt64}, cacheHead{Protocol: 2, Epoch: "0123456789abcdef0123456789abcdef"}, map[string]any{"protocol": int64(1), "epoch": "0123456789abcdef0123456789abcdef", "generation": float64(2)}} {
		db, repos := newCacheFixture(t)
		ctx := t.Context()
		w := db.WriterFrom(ctx)
		if head == nil {
			if err := w.Delete(ctx, db.Doc(collectionCacheInvalidation, "head")); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := w.Set(ctx, db.Doc(collectionCacheInvalidation, "head"), head); err != nil {
				t.Fatal(err)
			}
		}
		tuples := []relationships.CreateRelationship{{ResourceType: "document", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"}, {ResourceType: "document", ResourceID: "d2", Relation: "viewer", SubjectType: "user", SubjectID: "u1"}}
		if err := repos.Relationships.CreateRelationships(ctx, tuples); !errors.Is(err, decisions.ErrCacheVersion) {
			t.Fatalf("invalid head write: %v", err)
		}
		for _, tuple := range tuples {
			exists, err := repos.Relationships.CheckRelationExists(ctx, tuple.ResourceType, tuple.ResourceID, tuple.Relation, tuple.SubjectType, tuple.SubjectID)
			if err != nil || exists {
				t.Fatalf("partial fact: %v/%v", exists, err)
			}
		}
		if err := repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "viewer"}); !errors.Is(err, decisions.ErrCacheVersion) {
			t.Fatalf("invalid head role write: %v", err)
		}
	}
}

func TestCacheEpochChangeRequiresReconstruction(t *testing.T) {
	db, repos := newCacheFixture(t)
	ctx := t.Context()
	writer, err := Repositories(ctx, db, WithoutIndexProbe(), WithCacheInvalidation())
	if err != nil {
		t.Fatal(err)
	}
	w := db.WriterFrom(ctx)
	if err := w.Set(ctx, db.Doc(collectionCacheInvalidation, "head"), cacheHead{Protocol: 1, Epoch: "00000000000000000000000000000000"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.CacheSource.Observe(ctx); !errors.Is(err, decisions.ErrCacheVersion) {
		t.Fatalf("old source observed new epoch: %v", err)
	}
	if err := repos.CacheSource.ReadSnapshot(ctx, func(context.Context, decisions.CacheVersion, decisions.CheckReads) error {
		t.Fatal("old source invoked new-epoch callback")
		return nil
	}); !errors.Is(err, decisions.ErrCacheVersion) {
		t.Fatalf("old source snapshot: %v", err)
	}
	assignment := roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "viewer"}
	if err := writer.Roles.Assign(ctx, assignment); !errors.Is(err, decisions.ErrCacheVersion) {
		t.Fatalf("writer-only epoch mismatch: %v", err)
	}
	if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{{ResourceType: "document", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"}}); !errors.Is(err, decisions.ErrCacheVersion) {
		t.Fatalf("reader writer epoch mismatch: %v", err)
	}
	fresh, err := Repositories(ctx, db, WithoutIndexProbe(), WithCacheReads())
	if err != nil {
		t.Fatal(err)
	}
	if fresh.CacheSource.CacheBinding() == repos.CacheSource.CacheBinding() {
		t.Fatal("rotation did not change binding")
	}
	if err := fresh.Roles.Assign(ctx, assignment); err != nil {
		t.Fatal(err)
	}
}

func TestCacheWriterConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T, policy mutations.GuardianPolicy) authorization.Repositories {
		_, repos := newCacheFixture(t, WithGuardianPolicy(policy))
		return repos
	})
}
func TestCacheAuditConformance(t *testing.T) {
	storetest.RunAudit(t, func(t *testing.T, enabled bool) authorization.Repositories {
		var options []Option
		if enabled {
			options = append(options, WithAudit())
		}
		_, repos := newCacheFixture(t, options...)
		return repos
	})
}

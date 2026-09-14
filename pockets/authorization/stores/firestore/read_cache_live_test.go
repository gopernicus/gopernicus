//go:build integration && live

package firestore

import (
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

// These entrypoints use the same explicitly disposable live target and required
// index probes as the existing live suite. They are not emulator release proof.
func liveCacheRepos(t *testing.T, db *firestoredb.DB, policy mutations.GuardianPolicy, audit bool) authorization.Repositories {
	t.Helper()
	probeLiveOnce(t, db)
	firestoretest.ResetLive(t, db, liveAllCollections...)
	if err := InitializeCacheInvalidation(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	opts := []Option{WithoutIndexProbe(), WithCacheReads(), WithGuardianPolicy(policy)}
	if audit {
		opts = append(opts, WithAudit())
	}
	repos, err := Repositories(t.Context(), db, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return repos
}

func TestCacheConformanceLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	storetest.Run(t, func(t *testing.T, policy mutations.GuardianPolicy) authorization.Repositories {
		return liveCacheRepos(t, db, policy, false)
	})
}

func TestCacheAuditLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	storetest.RunAudit(t, func(t *testing.T, enabled bool) authorization.Repositories {
		return liveCacheRepos(t, db, mutations.GuardianPolicy{}, enabled)
	})
}

func TestCacheSnapshotsLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	storetest.RunReadSnapshots(t, func(t *testing.T) authorization.Repositories {
		return liveCacheRepos(t, db, mutations.GuardianPolicy{}, false)
	})
}

func TestCachePublicBehaviorLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	storetest.RunReadCache(t, func(t *testing.T) authorization.Repositories {
		return liveCacheRepos(t, db, mutations.GuardianPolicy{}, false)
	}, func(*testing.T) cacher.Storer { return cacher.NewMemory() })
}

func TestCacheIndependentWriterLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	repos := liveCacheRepos(t, db, mutations.GuardianPolicy{}, false)
	before, err := repos.CacheSource.Observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := Repositories(t.Context(), db, WithoutIndexProbe(), WithCacheInvalidation())
	if err != nil {
		t.Fatal(err)
	}
	if writer.CacheSource != nil {
		t.Fatal("writer-only bundle exposes cache reads")
	}
	assignment := roles.Assignment{SubjectType: "user", SubjectID: "live-user", Role: "viewer", ResourceType: "document", ResourceID: "live-document"}
	if err := writer.Roles.Assign(t.Context(), assignment); err != nil {
		t.Fatal(err)
	}
	after, err := repos.CacheSource.Observe(t.Context())
	if err != nil || after.Epoch != before.Epoch || after.Generation <= before.Generation {
		t.Fatalf("participating writer head: before=%+v after=%+v err=%v", before, after, err)
	}
	if err := InitializeCacheInvalidation(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	repeated, err := repos.CacheSource.Observe(t.Context())
	if err != nil || repeated != after {
		t.Fatalf("initialization changed head: %+v/%v", repeated, err)
	}
}

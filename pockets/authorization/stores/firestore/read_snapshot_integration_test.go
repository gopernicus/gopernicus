//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
)

func TestCacheSnapshotRetainsContext(t *testing.T) {
	_, repos := newCacheFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := repos.CacheSource.ReadSnapshot(ctx, func(_ context.Context, _ decisions.CacheVersion, reads decisions.CheckReads) error {
		scoped := reads.ForChecks(relationships.ReadModel{})
		cancel()
		fresh := context.Background()
		if _, err := reads.HasExactRole(fresh, "user", "u1", "viewer", "", ""); !errors.Is(err, context.Canceled) {
			t.Fatalf("role snapshot context: %v", err)
		}
		if _, err := scoped.GetRelationTargets(fresh, "document", "d1", "viewer"); !errors.Is(err, context.Canceled) {
			t.Fatalf("targets snapshot context: %v", err)
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot cancellation: %v", err)
	}
}

func TestCacheConcurrentWritersRebuildHead(t *testing.T) {
	_, repos := newCacheFixture(t)
	before, err := repos.CacheSource.Observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const count = 6
	var wg sync.WaitGroup
	errorsOut := make(chan error, count)
	start := make(chan struct{})
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errorsOut <- repos.Roles.Assign(t.Context(), roles.Assignment{SubjectType: "user", SubjectID: fmt.Sprintf("u%d", i), Role: "viewer"})
		}()
	}
	close(start)
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	after, err := repos.CacheSource.Observe(t.Context())
	if err != nil || after.Generation != before.Generation+count {
		t.Fatalf("concurrent head=%+v/%v", after, err)
	}
}

func TestCacheAuditFailureRollsBackHeadAndFacts(t *testing.T) {
	db, repos := newCacheFixture(t)
	ctx := audit.WithSource(t.Context(), audit.Source{ActorType: "user", ActorID: "actor", Reason: "maintenance"})
	version, err := repos.CacheSource.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("audit refused")
	row := newRow(relationships.CreateRelationship{ResourceType: "document", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"})
	calls := 0
	err = retryTransact(ctx, db, func(ctx context.Context) error {
		return (factWrites{creates: []relationshipDoc{row}}).flush(ctx, db, failingAuditWriter{Writer: db.WriterFrom(ctx), failure: failure, calls: &calls}, true, version.Epoch)
	})
	if !errors.Is(err, failure) {
		t.Fatalf("audit failure: %v", err)
	}
	after, err := repos.CacheSource.Observe(ctx)
	if err != nil || after != version {
		t.Fatalf("audit failure advanced head: %+v/%v", after, err)
	}
	exists, err := repos.Relationships.CheckRelationExists(ctx, "document", "d1", "viewer", "user", "u1")
	if err != nil || exists {
		t.Fatalf("audit failure published fact: %v/%v", exists, err)
	}
}

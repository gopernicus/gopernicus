package pgx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestLive_ConcurrentReplaceOwnerKey is the compare-and-swap proof: N rotators
// racing on one owner key with the same expectation produce exactly one
// replacement and N-1 sdk.ErrConflict, and exactly one audit record.
func TestLive_ConcurrentReplaceOwnerKey(t *testing.T) {
	dsn := requireDSN(t)
	db, opts := openAndMigrate(t, dsn)
	repo := NewRepository(db, opts...)
	ctx := context.Background()
	owner := vault.Owner{Type: "tenant", ID: "race"}
	now := time.Now()
	if err := repo.CreateOwnerKey(ctx, vault.OwnerKey{Owner: owner, KeyID: "k1", WrappedKey: "w1", WrapperKeyID: "m1", CreatedAt: now, RotatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}

	const racers = 16
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		won       int
		conflicts int
		others    []error
	)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			record, err := vault.NewAuditRecord(time.Now(), vault.ActionOwnerKeyRewrapped, owner, "", 0, "k1", vault.Source{System: "race"})
			if err != nil {
				t.Error(err)
				return
			}
			key := vault.OwnerKey{Owner: owner, KeyID: "k2", WrappedKey: "w2", WrapperKeyID: "m2", CreatedAt: now, RotatedAt: now.Add(time.Duration(i) * time.Millisecond)}
			err = repo.ReplaceOwnerKey(ctx, key, "k1", nil, []vault.AuditRecord{record})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, sdk.ErrConflict):
				conflicts++
			default:
				others = append(others, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if won != 1 || conflicts != racers-1 || len(others) != 0 {
		t.Fatalf("won=%d conflicts=%d others=%v", won, conflicts, others)
	}
	audit, _, err := repo.ListAudit(ctx, vault.AuditFilter{Owner: &owner}, "", 100)
	if err != nil || len(audit) != 1 {
		t.Fatalf("audit: %+v %v", audit, err)
	}
}

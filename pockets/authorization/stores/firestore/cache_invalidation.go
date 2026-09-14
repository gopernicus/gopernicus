package firestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/sdk"
)

const collectionCacheInvalidation = "iam_cache_invalidation"

type cacheHead struct {
	Protocol   int64  `firestore:"protocol"`
	Epoch      string `firestore:"epoch"`
	Generation int64  `firestore:"generation"`
}

// WithCacheInvalidation requires every fact change to update the head atomically.
// Hosts must upgrade and configure every writer before activating cache readers.
func WithCacheInvalidation() Option { return func(c *config) { c.cacheInvalidation = true } }

// WithCacheReads exposes consistent check snapshots and implies writer participation.
// Initialize metadata explicitly before constructing the repository bundle.
func WithCacheReads() Option {
	return func(c *config) { c.cacheReads = true; c.cacheInvalidation = true }
}

// InitializeCacheInvalidation creates metadata once, or validates an existing
// head without changing it. This maintenance operation requires fenced readers
// and writers; it never rotates an existing epoch after a restore or clone.
func InitializeCacheInvalidation(ctx context.Context, db *firestoredb.DB) error {
	if db == nil {
		return fmt.Errorf("authorization cache: nil database: %w", sdk.ErrInvalidInput)
	}
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return retryTransact(ctx, db, func(ctx context.Context) error {
		r := db.ReaderFrom(ctx)
		snap, err := r.Get(ctx, db.Doc(collectionCacheInvalidation, "head"))
		if err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return err
		}
		if snap != nil && snap.Exists() {
			_, err := decodeCacheHead(snap)
			return err
		}
		var epoch [16]byte
		_, _ = rand.Read(epoch[:])
		w := db.WriterFrom(ctx)
		return w.Create(ctx, db.Doc(collectionCacheInvalidation, "head"), cacheHead{Protocol: 1, Epoch: hex.EncodeToString(epoch[:])})
	})
}

func decodeCacheHead(snap *gcfs.DocumentSnapshot) (decisions.CacheVersion, error) {
	if snap == nil || !snap.Exists() {
		return decisions.CacheVersion{}, decisions.ErrCacheVersion
	}
	data := snap.Data()
	protocol, pok := data["protocol"].(int64)
	epoch, eok := data["epoch"].(string)
	generation, gok := data["generation"].(int64)
	version := decisions.CacheVersion{Epoch: epoch, Generation: generation}
	if !pok || protocol != 1 || !eok || !gok {
		return decisions.CacheVersion{}, decisions.ErrCacheVersion
	}
	return version, version.Validate()
}
func readCacheHead(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader) (decisions.CacheVersion, error) {
	snap, err := r.Get(ctx, db.Doc(collectionCacheInvalidation, "head"))
	if errors.Is(err, sdk.ErrNotFound) {
		return decisions.CacheVersion{}, decisions.ErrCacheVersion
	}
	if err != nil {
		return decisions.CacheVersion{}, err
	}
	return decodeCacheHead(snap)
}

// Called before the first queued fact/audit write, within the same native attempt.
func nextCacheHead(ctx context.Context, db *firestoredb.DB, epoch string) (cacheHead, error) {
	version, err := readCacheHead(ctx, db, db.ReaderFrom(ctx))
	if err != nil {
		return cacheHead{}, err
	}
	if version.Epoch != epoch || version.Generation == math.MaxInt64 {
		return cacheHead{}, decisions.ErrCacheVersion
	}
	return cacheHead{Protocol: 1, Epoch: version.Epoch, Generation: version.Generation + 1}, nil
}
func writeCacheHead(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, head cacheHead) error {
	return w.Set(ctx, db.Doc(collectionCacheInvalidation, "head"), head)
}
func cacheBinding(db *firestoredb.DB, version decisions.CacheVersion) string {
	return "firestore/authorization-check-reads/v1/" + db.Target() + "/" + version.Epoch
}

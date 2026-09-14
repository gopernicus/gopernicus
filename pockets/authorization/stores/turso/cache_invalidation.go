package turso

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"regexp"
	"strings"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/sdk"
)

// CacheMigrationsFS is the optional authorization-cache source. Hosts must apply
// authorization through 0007 first, and own application and rollback of this source.
//
//go:embed cache_migrations/*.sql
var CacheMigrationsFS embed.FS

const CacheMigrationsDir = "cache_migrations"
const CacheMigrationSource = "authorization-cache"

func ExportCacheMigrations(dst string) error {
	return tursodb.ExportMigrations(CacheMigrationsFS, CacheMigrationsDir, dst)
}

// WithCacheReads probes and exposes consistent reads. Installing the optional
// migration independently activates invalidation for every ordinary SQL writer.
func WithCacheReads() Option { return func(c *config) { c.cacheReads = true } }

func prepareCacheSource(ctx context.Context, db *tursodb.DB, cfg *config) (*cacheSource, error) {
	if _, ambient := tursodb.TxFromContext(ctx); ambient {
		return nil, fmt.Errorf("authorization cache: ambient construction: %w", sdk.ErrInvalidInput)
	}
	tx, err := db.BeginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	data, err := CacheMigrationsFS.ReadFile(CacheMigrationsDir + "/0001_iam_cache_invalidation.sql")
	if err != nil {
		return nil, err
	}
	// Compare the complete canonical definitions, preserving literals. SQLite
	// removes the final semicolon when persisting these CREATE statements.
	definitions := regexp.MustCompile(`(?s)CREATE TABLE (iam_cache_invalidation) \(.*?\n\);|CREATE TRIGGER (\w+).*?\nEND;`).FindAllStringSubmatch(string(data), -1)
	if len(definitions) != 7 {
		return nil, decisions.ErrCacheVersion
	}
	for _, def := range definitions {
		name, kind, table := def[1], "table", "iam_cache_invalidation"
		if name == "" {
			name, kind = def[2], "trigger"
			table = strings.Split(name, "_cache_")[0]
		}
		var actual string
		if err := tx.QueryRow(ctx, "SELECT sql FROM main.sqlite_schema WHERE name=? AND type=? AND tbl_name=?", name, kind, table).Scan(&actual); err != nil {
			return nil, fmt.Errorf("authorization-cache definition %s missing: %w", name, err)
		}
		if strings.TrimSpace(actual) != strings.TrimSuffix(def[0], ";") {
			return nil, fmt.Errorf("authorization-cache definition %s incompatible: %w", name, decisions.ErrCacheVersion)
		}
	}
	source := &cacheSource{db: db}
	v, err := source.readHead(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	cfg.cacheBinding = fmt.Sprintf("%x", sha256.Sum256([]byte("turso/main/authorization-check-reads/v1/"+v.Epoch)))
	source.cfg, source.epoch = *cfg, v.Epoch
	return source, nil
}

func (s *cacheSource) readHead(ctx context.Context, q tursodb.Querier) (decisions.CacheVersion, error) {
	rows, err := q.Query(ctx, "SELECT slot, protocol, epoch, generation, typeof(slot), typeof(protocol), typeof(epoch), typeof(generation) FROM main.iam_cache_invalidation")
	if err != nil {
		return decisions.CacheVersion{}, err
	}
	defer rows.Close()
	var v decisions.CacheVersion
	count := 0
	for rows.Next() {
		var slot, protocol int64
		var slotType, protocolType, epochType, generationType string
		if err := rows.Scan(&slot, &protocol, &v.Epoch, &v.Generation, &slotType, &protocolType, &epochType, &generationType); err != nil {
			return decisions.CacheVersion{}, fmt.Errorf("%w: %v", decisions.ErrCacheVersion, err)
		}
		count++
		if count != 1 || slot != 1 || protocol != 1 || slotType != "integer" || protocolType != "integer" || epochType != "text" || generationType != "integer" {
			return decisions.CacheVersion{}, decisions.ErrCacheVersion
		}
	}
	if err := rows.Err(); err != nil {
		return decisions.CacheVersion{}, err
	}
	if err := rows.Close(); err != nil {
		return decisions.CacheVersion{}, err
	}
	if count != 1 || v.Validate() != nil || s.epoch != "" && v.Epoch != s.epoch {
		return decisions.CacheVersion{}, decisions.ErrCacheVersion
	}
	return v, nil
}

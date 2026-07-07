package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/logic/media"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// AssetStore implements media.AssetRepository (metadata) over a PostgreSQL
// database.
type AssetStore struct {
	db *pgxdb.DB
}

var _ media.AssetRepository = (*AssetStore)(nil)

// NewAssetStore returns an AssetStore backed by db.
func NewAssetStore(db *pgxdb.DB) *AssetStore {
	return &AssetStore{db: db}
}

const assetColumns = "id, filename, content_type, size, storage_key, alt, created_at"

// Create persists asset metadata.
func (s *AssetStore) Create(ctx context.Context, a media.Asset) (media.Asset, error) {
	const q = `INSERT INTO assets (` + assetColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := s.db.Exec(ctx, q,
		a.ID, a.Filename, a.ContentType, a.Size, a.StorageKey, a.Alt,
		a.CreatedAt.UTC(),
	)
	if err != nil {
		return media.Asset{}, err
	}
	return a, nil
}

// Get returns the asset with the given id, or crud.ErrNotFound.
func (s *AssetStore) Get(ctx context.Context, id string) (media.Asset, error) {
	const q = `SELECT ` + assetColumns + ` FROM assets WHERE id = $1`
	return scanAsset(s.db.QueryRow(ctx, q, id))
}

// List returns all assets, newest first.
func (s *AssetStore) List(ctx context.Context) ([]media.Asset, error) {
	const q = `SELECT ` + assetColumns + ` FROM assets ORDER BY created_at DESC, id DESC`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []media.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, pgxdb.MapError(rows.Err())
}

// Delete removes asset metadata by id.
func (s *AssetStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM assets WHERE id = $1`, id)
	return err
}

func scanAsset(sc scanner) (media.Asset, error) {
	var (
		a         media.Asset
		createdAt time.Time
	)
	err := sc.Scan(&a.ID, &a.Filename, &a.ContentType, &a.Size, &a.StorageKey, &a.Alt, &createdAt)
	if err != nil {
		return media.Asset{}, pgxdb.MapError(err)
	}
	a.CreatedAt = createdAt.UTC()
	return a, nil
}

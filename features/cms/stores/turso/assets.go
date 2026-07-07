package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/cms/logic/media"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// AssetStore implements media.AssetRepository (metadata) over a libSQL database.
type AssetStore struct {
	db *tursodb.DB
}

var _ media.AssetRepository = (*AssetStore)(nil)

// NewAssetStore returns an AssetStore backed by db.
func NewAssetStore(db *tursodb.DB) *AssetStore {
	return &AssetStore{db: db}
}

const assetColumns = "id, filename, content_type, size, storage_key, alt, created_at"

// Create persists asset metadata.
func (s *AssetStore) Create(ctx context.Context, a media.Asset) (media.Asset, error) {
	const q = `INSERT INTO assets (` + assetColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		a.ID, a.Filename, a.ContentType, a.Size, a.StorageKey, a.Alt,
		a.CreatedAt.UTC().Format(tsLayout),
	)
	if err != nil {
		return media.Asset{}, err
	}
	return a, nil
}

// Get returns the asset with the given id, or crud.ErrNotFound.
func (s *AssetStore) Get(ctx context.Context, id string) (media.Asset, error) {
	const q = `SELECT ` + assetColumns + ` FROM assets WHERE id = ?`
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
	return out, tursodb.MapError(rows.Err())
}

// Delete removes asset metadata by id.
func (s *AssetStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM assets WHERE id = ?`, id)
	return err
}

func scanAsset(sc scanner) (media.Asset, error) {
	var (
		a         media.Asset
		createdAt string
	)
	err := sc.Scan(&a.ID, &a.Filename, &a.ContentType, &a.Size, &a.StorageKey, &a.Alt, &createdAt)
	if err != nil {
		return media.Asset{}, tursodb.MapError(err)
	}
	if a.CreatedAt, err = parseTime(createdAt); err != nil {
		return media.Asset{}, err
	}
	return a, nil
}

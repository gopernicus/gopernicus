package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/cms/domain/media"
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

// assetRow is the store-local, db-tagged projection of an assets row.
type assetRow struct {
	ID          string       `db:"id"`
	Filename    string       `db:"filename"`
	ContentType string       `db:"content_type"`
	Size        int64        `db:"size"`
	StorageKey  string       `db:"storage_key"`
	Alt         string       `db:"alt"`
	CreatedAt   tursodb.Time `db:"created_at"`
}

func (r assetRow) toDomain() media.Asset {
	return media.Asset{
		ID:          r.ID,
		Filename:    r.Filename,
		ContentType: r.ContentType,
		Size:        r.Size,
		StorageKey:  r.StorageKey,
		Alt:         r.Alt,
		CreatedAt:   r.CreatedAt.Time,
	}
}

// Create persists asset metadata.
func (s *AssetStore) Create(ctx context.Context, a media.Asset) (media.Asset, error) {
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if a.ID == "" {
		const q = `INSERT INTO assets (filename, content_type, size, storage_key, alt, created_at)
			VALUES (?, ?, ?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q,
			a.Filename, a.ContentType, a.Size, a.StorageKey, a.Alt,
			tursodb.FormatTime(a.CreatedAt),
		).Scan(&a.ID); err != nil {
			return media.Asset{}, tursodb.MapError(err)
		}
		return a, nil
	}
	const q = `INSERT INTO assets (` + assetColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		a.ID, a.Filename, a.ContentType, a.Size, a.StorageKey, a.Alt,
		tursodb.FormatTime(a.CreatedAt),
	)
	if err != nil {
		return media.Asset{}, err
	}
	return a, nil
}

// Get returns the asset with the given id, or crud.ErrNotFound.
func (s *AssetStore) Get(ctx context.Context, id string) (media.Asset, error) {
	const q = `SELECT ` + assetColumns + ` FROM assets WHERE id = ?`
	row, err := tursodb.QueryOne[assetRow](ctx, s.db, q, id)
	if err != nil {
		return media.Asset{}, err
	}
	return row.toDomain(), nil
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
		row, err := tursodb.ScanStruct[assetRow](rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row.toDomain())
	}
	return out, tursodb.MapError(rows.Err())
}

// Delete removes asset metadata by id.
func (s *AssetStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM assets WHERE id = ?`, id)
	return err
}

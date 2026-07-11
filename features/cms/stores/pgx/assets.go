package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/cms/domain/media"
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

// assetRow is the store-local, db-tagged projection of an assets row.
type assetRow struct {
	ID          string    `db:"id"`
	Filename    string    `db:"filename"`
	ContentType string    `db:"content_type"`
	Size        int64     `db:"size"`
	StorageKey  string    `db:"storage_key"`
	Alt         string    `db:"alt"`
	CreatedAt   time.Time `db:"created_at"`
}

func (r assetRow) toDomain() media.Asset {
	return media.Asset{
		ID:          r.ID,
		Filename:    r.Filename,
		ContentType: r.ContentType,
		Size:        r.Size,
		StorageKey:  r.StorageKey,
		Alt:         r.Alt,
		CreatedAt:   r.CreatedAt.UTC(),
	}
}

// Create persists asset metadata.
func (s *AssetStore) Create(ctx context.Context, a media.Asset) (media.Asset, error) {
	args := pgx.NamedArgs{
		"filename":     a.Filename,
		"content_type": a.ContentType,
		"size":         a.Size,
		"storage_key":  a.StorageKey,
		"alt":          a.Alt,
		"created_at":   a.CreatedAt.UTC(),
	}
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if a.ID == "" {
		const q = `INSERT INTO assets (filename, content_type, size, storage_key, alt, created_at)
			VALUES (@filename, @content_type, @size, @storage_key, @alt, @created_at)
			RETURNING id`
		if err := s.db.QueryRow(ctx, q, args).Scan(&a.ID); err != nil {
			return media.Asset{}, pgxdb.MapError(err)
		}
		return a, nil
	}
	const q = `INSERT INTO assets (` + assetColumns + `)
		VALUES (@id, @filename, @content_type, @size, @storage_key, @alt, @created_at)`
	args["id"] = a.ID
	if _, err := s.db.Exec(ctx, q, args); err != nil {
		return media.Asset{}, err
	}
	return a, nil
}

// Get returns the asset with the given id, or crud.ErrNotFound.
func (s *AssetStore) Get(ctx context.Context, id string) (media.Asset, error) {
	const q = `SELECT ` + assetColumns + ` FROM assets WHERE id = @id`
	row, err := pgxdb.QueryOne[assetRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
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
		return nil, pgxdb.MapError(err)
	}
	assets, err := pgx.CollectRows(rows, pgx.RowToStructByName[assetRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	var out []media.Asset
	for _, a := range assets {
		out = append(out, a.toDomain())
	}
	return out, nil
}

// Delete removes asset metadata by id.
func (s *AssetStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM assets WHERE id = @id`, pgx.NamedArgs{"id": id})
	return err
}

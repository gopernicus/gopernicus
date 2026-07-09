package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// TermStore implements taxonomy.TermRepository over a PostgreSQL database.
type TermStore struct {
	db *pgxdb.DB
}

var _ taxonomy.TermRepository = (*TermStore)(nil)

// NewTermStore returns a TermStore backed by db.
func NewTermStore(db *pgxdb.DB) *TermStore {
	return &TermStore{db: db}
}

const termColumns = "id, kind, slug, name, parent_id, created_at, updated_at"

// termRow is the store-local, db-tagged projection of a terms row.
type termRow struct {
	ID        string    `db:"id"`
	Kind      string    `db:"kind"`
	Slug      string    `db:"slug"`
	Name      string    `db:"name"`
	ParentID  string    `db:"parent_id"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (r termRow) toDomain() taxonomy.Term {
	return taxonomy.Term{
		ID:        r.ID,
		Kind:      taxonomy.Kind(r.Kind),
		Slug:      r.Slug,
		Name:      r.Name,
		ParentID:  r.ParentID,
		CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// Create persists a new term.
func (s *TermStore) Create(ctx context.Context, t taxonomy.Term) (taxonomy.Term, error) {
	args := pgx.NamedArgs{
		"kind":       string(t.Kind),
		"slug":       t.Slug,
		"name":       t.Name,
		"parent_id":  t.ParentID,
		"created_at": t.CreatedAt.UTC(),
		"updated_at": t.UpdatedAt.UTC(),
	}
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if t.ID == "" {
		const q = `INSERT INTO terms (kind, slug, name, parent_id, created_at, updated_at)
			VALUES (@kind, @slug, @name, @parent_id, @created_at, @updated_at)
			RETURNING id`
		if err := s.db.QueryRow(ctx, q, args).Scan(&t.ID); err != nil {
			return taxonomy.Term{}, pgxdb.MapError(err)
		}
		return t, nil
	}
	const q = `INSERT INTO terms (` + termColumns + `)
		VALUES (@id, @kind, @slug, @name, @parent_id, @created_at, @updated_at)`
	args["id"] = t.ID
	if _, err := s.db.Exec(ctx, q, args); err != nil {
		return taxonomy.Term{}, err
	}
	return t, nil
}

// Update persists changes to the term with the given id.
func (s *TermStore) Update(ctx context.Context, id string, t taxonomy.Term) (taxonomy.Term, error) {
	const q = `UPDATE terms SET kind = @kind, slug = @slug, name = @name, parent_id = @parent_id, updated_at = @updated_at WHERE id = @id`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{
		"kind":       string(t.Kind),
		"slug":       t.Slug,
		"name":       t.Name,
		"parent_id":  t.ParentID,
		"updated_at": t.UpdatedAt.UTC(),
		"id":         id,
	})
	if err != nil {
		return taxonomy.Term{}, err
	}
	if n == 0 {
		return taxonomy.Term{}, crud.ErrNotFound
	}
	return t, nil
}

// Get returns the term with the given id, or crud.ErrNotFound.
func (s *TermStore) Get(ctx context.Context, id string) (taxonomy.Term, error) {
	const q = `SELECT ` + termColumns + ` FROM terms WHERE id = @id`
	row, err := queryOne[termRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return taxonomy.Term{}, err
	}
	return row.toDomain(), nil
}

// GetBySlug returns the term with the given kind+slug, or crud.ErrNotFound.
func (s *TermStore) GetBySlug(ctx context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error) {
	const q = `SELECT ` + termColumns + ` FROM terms WHERE kind = @kind AND slug = @slug`
	row, err := queryOne[termRow](ctx, s.db, q, pgx.NamedArgs{"kind": string(kind), "slug": slug})
	if err != nil {
		return taxonomy.Term{}, err
	}
	return row.toDomain(), nil
}

// ListByKind returns all terms of a kind, ordered by name.
func (s *TermStore) ListByKind(ctx context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error) {
	const q = `SELECT ` + termColumns + ` FROM terms WHERE kind = @kind ORDER BY name`
	rows, err := s.db.Query(ctx, q, pgx.NamedArgs{"kind": string(kind)})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	trows, err := pgx.CollectRows(rows, pgx.RowToStructByName[termRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	var terms []taxonomy.Term
	for _, t := range trows {
		terms = append(terms, t.toDomain())
	}
	return terms, nil
}

// Delete removes the term and its entry associations in a transaction.
func (s *TermStore) Delete(ctx context.Context, id string) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM entry_terms WHERE term_id = @term_id", pgx.NamedArgs{"term_id": id}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM terms WHERE id = @id", pgx.NamedArgs{"id": id})
		return err
	})
}

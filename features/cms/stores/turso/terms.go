package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// TermStore implements taxonomy.TermRepository over a libSQL database.
type TermStore struct {
	db *tursodb.DB
}

var _ taxonomy.TermRepository = (*TermStore)(nil)

// NewTermStore returns a TermStore backed by db.
func NewTermStore(db *tursodb.DB) *TermStore {
	return &TermStore{db: db}
}

const termColumns = "id, kind, slug, name, parent_id, created_at, updated_at"

// termRow is the store-local, db-tagged projection of a terms row.
type termRow struct {
	ID        string       `db:"id"`
	Kind      string       `db:"kind"`
	Slug      string       `db:"slug"`
	Name      string       `db:"name"`
	ParentID  string       `db:"parent_id"`
	CreatedAt tursodb.Time `db:"created_at"`
	UpdatedAt tursodb.Time `db:"updated_at"`
}

func (r termRow) toDomain() taxonomy.Term {
	return taxonomy.Term{
		ID:        r.ID,
		Kind:      taxonomy.Kind(r.Kind),
		Slug:      r.Slug,
		Name:      r.Name,
		ParentID:  r.ParentID,
		CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time,
	}
}

// Create persists a new term.
func (s *TermStore) Create(ctx context.Context, t taxonomy.Term) (taxonomy.Term, error) {
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if t.ID == "" {
		const q = `INSERT INTO terms (kind, slug, name, parent_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q,
			string(t.Kind), t.Slug, t.Name, t.ParentID,
			tursodb.FormatTime(t.CreatedAt), tursodb.FormatTime(t.UpdatedAt),
		).Scan(&t.ID); err != nil {
			return taxonomy.Term{}, tursodb.MapError(err)
		}
		return t, nil
	}
	const q = `INSERT INTO terms (` + termColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		t.ID, string(t.Kind), t.Slug, t.Name, t.ParentID,
		tursodb.FormatTime(t.CreatedAt), tursodb.FormatTime(t.UpdatedAt),
	)
	if err != nil {
		return taxonomy.Term{}, err
	}
	return t, nil
}

// Update persists changes to the term with the given id.
func (s *TermStore) Update(ctx context.Context, id string, t taxonomy.Term) (taxonomy.Term, error) {
	const q = `UPDATE terms SET kind=?, slug=?, name=?, parent_id=?, updated_at=? WHERE id=?`
	n, err := tursodb.ExecAffecting(ctx, s.db, q,
		string(t.Kind), t.Slug, t.Name, t.ParentID, tursodb.FormatTime(t.UpdatedAt), id,
	)
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
	const q = `SELECT ` + termColumns + ` FROM terms WHERE id = ?`
	row, err := tursodb.QueryOne[termRow](ctx, s.db, q, id)
	if err != nil {
		return taxonomy.Term{}, err
	}
	return row.toDomain(), nil
}

// GetBySlug returns the term with the given kind+slug, or crud.ErrNotFound.
func (s *TermStore) GetBySlug(ctx context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error) {
	const q = `SELECT ` + termColumns + ` FROM terms WHERE kind = ? AND slug = ?`
	row, err := tursodb.QueryOne[termRow](ctx, s.db, q, string(kind), slug)
	if err != nil {
		return taxonomy.Term{}, err
	}
	return row.toDomain(), nil
}

// ListByKind returns all terms of a kind, ordered by name.
func (s *TermStore) ListByKind(ctx context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error) {
	const q = `SELECT ` + termColumns + ` FROM terms WHERE kind = ? ORDER BY name`
	rows, err := s.db.Query(ctx, q, string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var terms []taxonomy.Term
	for rows.Next() {
		row, err := tursodb.ScanStruct[termRow](rows)
		if err != nil {
			return nil, err
		}
		terms = append(terms, row.toDomain())
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return terms, nil
}

// Delete removes the term and its entry associations in a transaction.
func (s *TermStore) Delete(ctx context.Context, id string) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM entry_terms WHERE term_id = ?", id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM terms WHERE id = ?", id)
		return err
	})
}

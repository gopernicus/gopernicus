package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/logic/taxonomy"
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

// Create persists a new term.
func (s *TermStore) Create(ctx context.Context, t taxonomy.Term) (taxonomy.Term, error) {
	const q = `INSERT INTO terms (` + termColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := s.db.Exec(ctx, q,
		t.ID, string(t.Kind), t.Slug, t.Name, t.ParentID,
		t.CreatedAt.UTC(), t.UpdatedAt.UTC(),
	)
	if err != nil {
		return taxonomy.Term{}, err
	}
	return t, nil
}

// Update persists changes to the term with the given id.
func (s *TermStore) Update(ctx context.Context, id string, t taxonomy.Term) (taxonomy.Term, error) {
	const q = `UPDATE terms SET kind=$1, slug=$2, name=$3, parent_id=$4, updated_at=$5 WHERE id=$6`
	res, err := s.db.Exec(ctx, q,
		string(t.Kind), t.Slug, t.Name, t.ParentID, t.UpdatedAt.UTC(), id,
	)
	if err != nil {
		return taxonomy.Term{}, err
	}
	if res.RowsAffected() == 0 {
		return taxonomy.Term{}, crud.ErrNotFound
	}
	return t, nil
}

// Get returns the term with the given id, or crud.ErrNotFound.
func (s *TermStore) Get(ctx context.Context, id string) (taxonomy.Term, error) {
	const q = `SELECT ` + termColumns + ` FROM terms WHERE id = $1`
	return scanTerm(s.db.QueryRow(ctx, q, id))
}

// GetBySlug returns the term with the given kind+slug, or crud.ErrNotFound.
func (s *TermStore) GetBySlug(ctx context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error) {
	const q = `SELECT ` + termColumns + ` FROM terms WHERE kind = $1 AND slug = $2`
	return scanTerm(s.db.QueryRow(ctx, q, string(kind), slug))
}

// ListByKind returns all terms of a kind, ordered by name.
func (s *TermStore) ListByKind(ctx context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error) {
	const q = `SELECT ` + termColumns + ` FROM terms WHERE kind = $1 ORDER BY name`
	rows, err := s.db.Query(ctx, q, string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var terms []taxonomy.Term
	for rows.Next() {
		t, err := scanTerm(rows)
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.MapError(err)
	}
	return terms, nil
}

// Delete removes the term and its entry associations in a transaction.
func (s *TermStore) Delete(ctx context.Context, id string) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM entry_terms WHERE term_id = $1", id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM terms WHERE id = $1", id)
		return err
	})
}

func scanTerm(sc scanner) (taxonomy.Term, error) {
	var (
		t                    taxonomy.Term
		kind                 string
		createdAt, updatedAt time.Time
	)
	err := sc.Scan(&t.ID, &kind, &t.Slug, &t.Name, &t.ParentID, &createdAt, &updatedAt)
	if err != nil {
		return taxonomy.Term{}, pgxdb.MapError(err)
	}
	t.Kind = taxonomy.Kind(kind)
	t.CreatedAt = createdAt.UTC()
	t.UpdatedAt = updatedAt.UTC()
	return t, nil
}

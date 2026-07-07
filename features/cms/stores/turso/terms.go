package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/cms/logic/taxonomy"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
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

// Create persists a new term.
func (s *TermStore) Create(ctx context.Context, t taxonomy.Term) (taxonomy.Term, error) {
	const q = `INSERT INTO terms (` + termColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		t.ID, string(t.Kind), t.Slug, t.Name, t.ParentID,
		t.CreatedAt.UTC().Format(tsLayout), t.UpdatedAt.UTC().Format(tsLayout),
	)
	if err != nil {
		return taxonomy.Term{}, err
	}
	return t, nil
}

// Update persists changes to the term with the given id.
func (s *TermStore) Update(ctx context.Context, id string, t taxonomy.Term) (taxonomy.Term, error) {
	const q = `UPDATE terms SET kind=?, slug=?, name=?, parent_id=?, updated_at=? WHERE id=?`
	res, err := s.db.Exec(ctx, q,
		string(t.Kind), t.Slug, t.Name, t.ParentID, t.UpdatedAt.UTC().Format(tsLayout), id,
	)
	if err != nil {
		return taxonomy.Term{}, err
	}
	n, err := res.RowsAffected()
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
	return scanTerm(s.db.QueryRow(ctx, q, id))
}

// GetBySlug returns the term with the given kind+slug, or crud.ErrNotFound.
func (s *TermStore) GetBySlug(ctx context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error) {
	const q = `SELECT ` + termColumns + ` FROM terms WHERE kind = ? AND slug = ?`
	return scanTerm(s.db.QueryRow(ctx, q, string(kind), slug))
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
		t, err := scanTerm(rows)
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
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

func scanTerm(sc scanner) (taxonomy.Term, error) {
	var (
		t                    taxonomy.Term
		kind                 string
		createdAt, updatedAt string
	)
	err := sc.Scan(&t.ID, &kind, &t.Slug, &t.Name, &t.ParentID, &createdAt, &updatedAt)
	if err != nil {
		return taxonomy.Term{}, tursodb.MapError(err)
	}
	t.Kind = taxonomy.Kind(kind)
	if t.CreatedAt, err = parseTime(createdAt); err != nil {
		return taxonomy.Term{}, err
	}
	if t.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return taxonomy.Term{}, err
	}
	return t, nil
}

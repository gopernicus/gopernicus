package turso

import (
	"context"
	"database/sql"

	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// EntryStore implements content.EntryRepository over a libSQL database. It is
// the single content store on the frozen spine: Create/Update write the spine
// row and replace the entry's entry_fields rows in one Tx; Get loads spine +
// fields (coerced via kind) + terms; List/ListByTerm query indexed spine
// columns. One store replaces the former PostStore + PageStore.
type EntryStore struct {
	db *tursodb.DB
}

var _ content.EntryRepository = (*EntryStore)(nil)

// NewEntryStore returns an EntryStore backed by db.
func NewEntryStore(db *tursodb.DB) *EntryStore {
	return &EntryStore{db: db}
}

const entryColumns = "id, type, slug, title, status, body, excerpt, author, template, parent_id, menu_order, published_at, created_at, updated_at"

// Create persists a new entry and its custom fields in one transaction.
func (s *EntryStore) Create(ctx context.Context, e content.Entry) (content.Entry, error) {
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		const q = `INSERT INTO entries (` + entryColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		if _, err := tx.Exec(ctx, q,
			e.ID, e.Type, e.Slug, e.Title, string(e.Status), e.Body, e.Excerpt, e.Author,
			e.Template, e.ParentID, e.MenuOrder, tursodb.NullTimePtr(e.PublishedAt),
			tursodb.FormatTime(e.CreatedAt), tursodb.FormatTime(e.UpdatedAt),
		); err != nil {
			return err
		}
		return writeFields(ctx, tx, e.ID, e.Fields)
	})
	if err != nil {
		return content.Entry{}, tursodb.MapError(err)
	}
	return e, nil
}

// Update persists changes to the entry with the given id, replacing its custom
// fields, in one transaction.
func (s *EntryStore) Update(ctx context.Context, id string, e content.Entry) (content.Entry, error) {
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		const q = `UPDATE entries SET type=?, slug=?, title=?, status=?, body=?, excerpt=?, author=?, template=?, parent_id=?, menu_order=?, published_at=?, updated_at=? WHERE id=?`
		n, err := tursodb.ExecAffecting(ctx, tx, q,
			e.Type, e.Slug, e.Title, string(e.Status), e.Body, e.Excerpt, e.Author,
			e.Template, e.ParentID, e.MenuOrder, tursodb.NullTimePtr(e.PublishedAt),
			tursodb.FormatTime(e.UpdatedAt), id,
		)
		if err != nil {
			return err
		}
		if n == 0 {
			return crud.ErrNotFound
		}
		if _, err := tx.Exec(ctx, "DELETE FROM entry_fields WHERE entry_id = ?", id); err != nil {
			return err
		}
		return writeFields(ctx, tx, id, e.Fields)
	})
	if err != nil {
		return content.Entry{}, tursodb.MapError(err)
	}
	return e, nil
}

// Get returns the entry with the given id — spine, fields, and term IDs.
func (s *EntryStore) Get(ctx context.Context, id string) (content.Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM entries WHERE id = ?`
	return s.loadOne(ctx, s.db.QueryRow(ctx, q, id))
}

// GetBySlug returns the entry of type typ with the given slug.
func (s *EntryStore) GetBySlug(ctx context.Context, typ, slug string) (content.Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM entries WHERE type = ? AND slug = ?`
	return s.loadOne(ctx, s.db.QueryRow(ctx, q, typ, slug))
}

// Delete removes the entry; its fields and terms cascade.
func (s *EntryStore) Delete(ctx context.Context, id string) error {
	n, err := tursodb.ExecAffecting(ctx, s.db, "DELETE FROM entries WHERE id = ?", id)
	if err != nil {
		return tursodb.MapError(err)
	}
	if n == 0 {
		return crud.ErrNotFound
	}
	return nil
}

// List returns a cursor-paginated page of entries matching q, ordered by
// (created_at, id) descending.
func (s *EntryStore) List(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
	where := "WHERE type = ?"
	args := []any{q.Type}
	return s.listWhere(ctx, where, args, q)
}

// ListByTerm returns a cursor-paginated page of entries matching q that are
// associated with termID.
func (s *EntryStore) ListByTerm(ctx context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error) {
	where := "WHERE type = ? AND id IN (SELECT entry_id FROM entry_terms WHERE term_id = ?)"
	args := []any{q.Type, termID}
	return s.listWhere(ctx, where, args, q)
}

// listWhere runs an order-aware, bidirectionally pageable, offset-capable list
// given a base WHERE clause and its args. It appends the optional status filter,
// runs the shared turso.List matrix over the entry order allow-list, then loads
// fields + terms for each returned spine row.
func (s *EntryStore) listWhere(ctx context.Context, where string, args []any, q content.EntryQuery) (crud.Page[content.Entry], error) {
	if q.Status != "" {
		where += " AND status = ?"
		args = append(args, string(q.Status))
	}

	lq := tursodb.ListQuery[content.Entry]{
		BaseSQL:      `SELECT ` + entryColumns + ` FROM entries ` + where,
		Args:         args,
		OrderFields:  content.OrderFields,
		DefaultOrder: content.DefaultOrder,
		PK:           "id",
		Scan:         scanEntry,
		OrderValueOf: func(e content.Entry, _ string) any { return e.CreatedAt },
		PKOf:         func(e content.Entry) string { return e.ID },
	}
	page, err := tursodb.List(ctx, s.db, lq, q.ListRequest)
	if err != nil {
		return crud.Page[content.Entry]{}, err
	}

	// Hydrate fields + terms for the rows on this page only.
	for i := range page.Items {
		if page.Items[i].Fields, err = s.loadFields(ctx, page.Items[i].ID); err != nil {
			return crud.Page[content.Entry]{}, err
		}
		if page.Items[i].TermIDs, err = s.loadTermIDs(ctx, page.Items[i].ID); err != nil {
			return crud.Page[content.Entry]{}, err
		}
	}

	return page, nil
}

// SetTerms replaces the entry's taxonomy associations in a single transaction.
func (s *EntryStore) SetTerms(ctx context.Context, entryID string, termIDs []string) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM entry_terms WHERE entry_id = ?", entryID); err != nil {
			return err
		}
		for _, tid := range termIDs {
			if _, err := tx.Exec(ctx, "INSERT INTO entry_terms (entry_id, term_id) VALUES (?, ?)", entryID, tid); err != nil {
				return err
			}
		}
		return nil
	})
}

// loadOne scans a spine row then hydrates its fields and term IDs.
func (s *EntryStore) loadOne(ctx context.Context, row scanner) (content.Entry, error) {
	e, err := scanEntry(row)
	if err != nil {
		return content.Entry{}, err
	}
	if e.Fields, err = s.loadFields(ctx, e.ID); err != nil {
		return content.Entry{}, err
	}
	if e.TermIDs, err = s.loadTermIDs(ctx, e.ID); err != nil {
		return content.Entry{}, err
	}
	return e, nil
}

// loadFields returns the custom fields for entryID, tagged with their stored
// kind. Repeater ordinals are deferred, so flat fields are keyed by key.
func (s *EntryStore) loadFields(ctx context.Context, entryID string) (content.Fields, error) {
	rows, err := s.db.Query(ctx, "SELECT key, kind, value FROM entry_fields WHERE entry_id = ? ORDER BY key, ordinal", entryID)
	if err != nil {
		return nil, tursodb.MapError(err)
	}
	defer rows.Close()
	fields := content.Fields{}
	for rows.Next() {
		var key, kind, value string
		if err := rows.Scan(&key, &kind, &value); err != nil {
			return nil, tursodb.MapError(err)
		}
		fields[key] = content.Value{Kind: content.FieldKind(kind), Raw: value}
	}
	return fields, rows.Err()
}

// loadTermIDs returns the taxonomy term IDs associated with entryID.
func (s *EntryStore) loadTermIDs(ctx context.Context, entryID string) ([]string, error) {
	rows, err := s.db.Query(ctx, "SELECT term_id FROM entry_terms WHERE entry_id = ? ORDER BY term_id", entryID)
	if err != nil {
		return nil, tursodb.MapError(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, tursodb.MapError(err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// writeFields inserts the entry's custom fields. Callers run it inside the same
// Tx as the spine write (and after deleting prior rows on update).
func writeFields(ctx context.Context, tx *tursodb.Tx, entryID string, fields content.Fields) error {
	for key, v := range fields {
		if _, err := tx.Exec(ctx,
			"INSERT INTO entry_fields (entry_id, key, kind, value, ordinal) VALUES (?, ?, ?, ?, 0)",
			entryID, key, string(v.Kind), v.Raw,
		); err != nil {
			return err
		}
	}
	return nil
}

// scanEntry scans one spine row into an Entry (fields/terms loaded separately).
func scanEntry(sc scanner) (content.Entry, error) {
	var (
		e                    content.Entry
		status               string
		publishedAt          sql.NullString
		createdAt, updatedAt string
	)
	err := sc.Scan(&e.ID, &e.Type, &e.Slug, &e.Title, &status, &e.Body, &e.Excerpt, &e.Author,
		&e.Template, &e.ParentID, &e.MenuOrder, &publishedAt, &createdAt, &updatedAt)
	if err != nil {
		return content.Entry{}, tursodb.MapError(err)
	}
	e.Status = content.Status(status)
	if publishedAt.Valid && publishedAt.String != "" {
		t, err := tursodb.ParseTime(publishedAt.String)
		if err != nil {
			return content.Entry{}, err
		}
		e.PublishedAt = &t
	}
	if e.CreatedAt, err = tursodb.ParseTime(createdAt); err != nil {
		return content.Entry{}, err
	}
	if e.UpdatedAt, err = tursodb.ParseTime(updatedAt); err != nil {
		return content.Entry{}, err
	}
	return e, nil
}

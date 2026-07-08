package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// EntryStore implements content.EntryRepository over a PostgreSQL database. It is
// the single content store on the frozen spine: Create/Update write the spine
// row and replace the entry's entry_fields rows in one Tx; Get loads spine +
// fields (coerced via kind) + terms; List/ListByTerm query indexed spine
// columns. One store replaces the former PostStore + PageStore.
type EntryStore struct {
	db *pgxdb.DB
}

var _ content.EntryRepository = (*EntryStore)(nil)

// NewEntryStore returns an EntryStore backed by db.
func NewEntryStore(db *pgxdb.DB) *EntryStore {
	return &EntryStore{db: db}
}

const entryColumns = "id, type, slug, title, status, body, excerpt, author, template, parent_id, menu_order, published_at, created_at, updated_at"

// Create persists a new entry and its custom fields in one transaction.
func (s *EntryStore) Create(ctx context.Context, e content.Entry) (content.Entry, error) {
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		const q = `INSERT INTO entries (` + entryColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`
		if _, err := tx.Exec(ctx, q,
			e.ID, e.Type, e.Slug, e.Title, string(e.Status), e.Body, e.Excerpt, e.Author,
			e.Template, e.ParentID, e.MenuOrder, pgxdb.NullTimePtr(e.PublishedAt),
			e.CreatedAt.UTC(), e.UpdatedAt.UTC(),
		); err != nil {
			return err
		}
		return writeFields(ctx, tx, e.ID, e.Fields)
	})
	if err != nil {
		return content.Entry{}, pgxdb.MapError(err)
	}
	return e, nil
}

// Update persists changes to the entry with the given id, replacing its custom
// fields, in one transaction.
func (s *EntryStore) Update(ctx context.Context, id string, e content.Entry) (content.Entry, error) {
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		const q = `UPDATE entries SET type=$1, slug=$2, title=$3, status=$4, body=$5, excerpt=$6, author=$7, template=$8, parent_id=$9, menu_order=$10, published_at=$11, updated_at=$12 WHERE id=$13`
		res, err := tx.Exec(ctx, q,
			e.Type, e.Slug, e.Title, string(e.Status), e.Body, e.Excerpt, e.Author,
			e.Template, e.ParentID, e.MenuOrder, pgxdb.NullTimePtr(e.PublishedAt),
			e.UpdatedAt.UTC(), id,
		)
		if err != nil {
			return err
		}
		if res.RowsAffected() == 0 {
			return crud.ErrNotFound
		}
		if _, err := tx.Exec(ctx, "DELETE FROM entry_fields WHERE entry_id = $1", id); err != nil {
			return err
		}
		return writeFields(ctx, tx, id, e.Fields)
	})
	if err != nil {
		return content.Entry{}, pgxdb.MapError(err)
	}
	return e, nil
}

// Get returns the entry with the given id — spine, fields, and term IDs.
func (s *EntryStore) Get(ctx context.Context, id string) (content.Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM entries WHERE id = $1`
	return s.loadOne(ctx, s.db.QueryRow(ctx, q, id))
}

// GetBySlug returns the entry of type typ with the given slug.
func (s *EntryStore) GetBySlug(ctx context.Context, typ, slug string) (content.Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM entries WHERE type = $1 AND slug = $2`
	return s.loadOne(ctx, s.db.QueryRow(ctx, q, typ, slug))
}

// Delete removes the entry; its fields and terms cascade.
func (s *EntryStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.Exec(ctx, "DELETE FROM entries WHERE id = $1", id)
	if err != nil {
		return pgxdb.MapError(err)
	}
	if res.RowsAffected() == 0 {
		return crud.ErrNotFound
	}
	return nil
}

// List returns a cursor-paginated page of entries matching q, ordered by
// (created_at, id) descending.
func (s *EntryStore) List(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
	where := "WHERE type = $1"
	args := []any{q.Type}
	return s.listWhere(ctx, where, args, q)
}

// ListByTerm returns a cursor-paginated page of entries matching q that are
// associated with termID.
func (s *EntryStore) ListByTerm(ctx context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error) {
	where := "WHERE type = $1 AND id IN (SELECT entry_id FROM entry_terms WHERE term_id = $2)"
	args := []any{q.Type, termID}
	return s.listWhere(ctx, where, args, q)
}

// listWhere runs a keyset-paginated list given a base WHERE clause and its args.
// It appends the optional status filter and the cursor predicate, then loads
// fields + terms for each returned spine row. Placeholders are numbered from
// len(args)+1, so the base WHERE owns $1.. and the appended predicates continue
// the sequence.
func (s *EntryStore) listWhere(ctx context.Context, where string, args []any, q content.EntryQuery) (crud.Page[content.Entry], error) {
	if q.Status != "" {
		where += " AND status = " + placeholder(len(args)+1)
		args = append(args, string(q.Status))
	}

	page, err := pgxdb.ListPage(ctx, s.db, entryColumns, "entries", where, args, orderField, "id", q.ListRequest,
		scanEntry,
		func(e content.Entry) (time.Time, string) { return e.CreatedAt, e.ID },
	)
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
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM entry_terms WHERE entry_id = $1", entryID); err != nil {
			return err
		}
		for _, tid := range termIDs {
			if _, err := tx.Exec(ctx, "INSERT INTO entry_terms (entry_id, term_id) VALUES ($1, $2)", entryID, tid); err != nil {
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
	rows, err := s.db.Query(ctx, "SELECT key, kind, value FROM entry_fields WHERE entry_id = $1 ORDER BY key, ordinal", entryID)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	defer rows.Close()
	fields := content.Fields{}
	for rows.Next() {
		var key, kind, value string
		if err := rows.Scan(&key, &kind, &value); err != nil {
			return nil, pgxdb.MapError(err)
		}
		fields[key] = content.Value{Kind: content.FieldKind(kind), Raw: value}
	}
	return fields, pgxdb.MapError(rows.Err())
}

// loadTermIDs returns the taxonomy term IDs associated with entryID.
func (s *EntryStore) loadTermIDs(ctx context.Context, entryID string) ([]string, error) {
	rows, err := s.db.Query(ctx, "SELECT term_id FROM entry_terms WHERE entry_id = $1 ORDER BY term_id", entryID)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, pgxdb.MapError(err)
		}
		ids = append(ids, id)
	}
	return ids, pgxdb.MapError(rows.Err())
}

// writeFields inserts the entry's custom fields. Callers run it inside the same
// Tx as the spine write (and after deleting prior rows on update).
func writeFields(ctx context.Context, tx *pgxdb.Tx, entryID string, fields content.Fields) error {
	for key, v := range fields {
		if _, err := tx.Exec(ctx,
			"INSERT INTO entry_fields (entry_id, key, kind, value, ordinal) VALUES ($1, $2, $3, $4, 0)",
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
		published            *time.Time
		createdAt, updatedAt time.Time
	)
	err := sc.Scan(&e.ID, &e.Type, &e.Slug, &e.Title, &status, &e.Body, &e.Excerpt, &e.Author,
		&e.Template, &e.ParentID, &e.MenuOrder, &published, &createdAt, &updatedAt)
	if err != nil {
		return content.Entry{}, pgxdb.MapError(err)
	}
	e.Status = content.Status(status)
	e.PublishedAt = pgxdb.FromNullTimePtr(published)
	e.CreatedAt = createdAt.UTC()
	e.UpdatedAt = updatedAt.UTC()
	return e, nil
}

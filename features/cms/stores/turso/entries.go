package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
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

// entryRow is the store-local, db-tagged projection of an entries spine row
// ScanStruct scans into; toDomain maps it to the persistence-free domain entity
// (fields + terms hydrate separately). published_at is nullable (turso.NullTime →
// *time.Time via TimePtr).
type entryRow struct {
	ID          string           `db:"id"`
	Type        string           `db:"type"`
	Slug        string           `db:"slug"`
	Title       string           `db:"title"`
	Status      string           `db:"status"`
	Body        string           `db:"body"`
	Excerpt     string           `db:"excerpt"`
	Author      string           `db:"author"`
	Template    string           `db:"template"`
	ParentID    string           `db:"parent_id"`
	MenuOrder   int              `db:"menu_order"`
	PublishedAt tursodb.NullTime `db:"published_at"`
	CreatedAt   tursodb.Time     `db:"created_at"`
	UpdatedAt   tursodb.Time     `db:"updated_at"`
}

func (r entryRow) toDomain() content.Entry {
	return content.Entry{
		ID:          r.ID,
		Type:        r.Type,
		Slug:        r.Slug,
		Title:       r.Title,
		Status:      content.Status(r.Status),
		Body:        r.Body,
		Excerpt:     r.Excerpt,
		Author:      r.Author,
		Template:    r.Template,
		ParentID:    r.ParentID,
		MenuOrder:   r.MenuOrder,
		PublishedAt: r.PublishedAt.TimePtr(),
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
}

// fieldRow is the store-local, db-tagged projection of an entry_fields row.
type fieldRow struct {
	Key   string `db:"key"`
	Kind  string `db:"kind"`
	Value string `db:"value"`
}

// Create persists a new entry and its custom fields in one transaction.
func (s *EntryStore) Create(ctx context.Context, e content.Entry) (content.Entry, error) {
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		// Empty ID → the cryptids.Database strategy (amended D10): omit the id
		// column so the schema default generates the key, read back with RETURNING.
		// The generated key must be assigned before writeFields writes the child rows.
		if e.ID == "" {
			const q = `INSERT INTO entries (type, slug, title, status, body, excerpt, author, template, parent_id, menu_order, published_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`
			if err := tx.QueryRow(ctx, q,
				e.Type, e.Slug, e.Title, string(e.Status), e.Body, e.Excerpt, e.Author,
				e.Template, e.ParentID, e.MenuOrder, tursodb.FormatNullTimePtr(e.PublishedAt),
				tursodb.FormatTime(e.CreatedAt), tursodb.FormatTime(e.UpdatedAt),
			).Scan(&e.ID); err != nil {
				return err
			}
			return writeFields(ctx, tx, e.ID, e.Fields)
		}
		const q = `INSERT INTO entries (` + entryColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		if _, err := tx.Exec(ctx, q,
			e.ID, e.Type, e.Slug, e.Title, string(e.Status), e.Body, e.Excerpt, e.Author,
			e.Template, e.ParentID, e.MenuOrder, tursodb.FormatNullTimePtr(e.PublishedAt),
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
			e.Template, e.ParentID, e.MenuOrder, tursodb.FormatNullTimePtr(e.PublishedAt),
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
	row, err := queryOne[entryRow](ctx, s.db, q, id)
	if err != nil {
		return content.Entry{}, err
	}
	return s.hydrate(ctx, row.toDomain())
}

// GetBySlug returns the entry of type typ with the given slug.
func (s *EntryStore) GetBySlug(ctx context.Context, typ, slug string) (content.Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM entries WHERE type = ? AND slug = ?`
	row, err := queryOne[entryRow](ctx, s.db, q, typ, slug)
	if err != nil {
		return content.Entry{}, err
	}
	return s.hydrate(ctx, row.toDomain())
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

	lq := tursodb.ListQuery[entryRow]{
		BaseSQL:      `SELECT ` + entryColumns + ` FROM entries ` + where,
		Args:         args,
		OrderFields:  content.OrderFields,
		DefaultOrder: content.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r entryRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r entryRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, lq, q.ListRequest)
	if err != nil {
		return crud.Page[content.Entry]{}, err
	}
	domainPage := crud.MapPage(page, entryRow.toDomain)

	// Hydrate fields + terms for the rows on this page only.
	for i := range domainPage.Items {
		if domainPage.Items[i].Fields, err = s.loadFields(ctx, domainPage.Items[i].ID); err != nil {
			return crud.Page[content.Entry]{}, err
		}
		if domainPage.Items[i].TermIDs, err = s.loadTermIDs(ctx, domainPage.Items[i].ID); err != nil {
			return crud.Page[content.Entry]{}, err
		}
	}

	return domainPage, nil
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

// hydrate loads an entry's fields and term IDs onto e.
func (s *EntryStore) hydrate(ctx context.Context, e content.Entry) (content.Entry, error) {
	var err error
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
		row, err := tursodb.ScanStruct[fieldRow](rows)
		if err != nil {
			return nil, err
		}
		fields[row.Key] = content.Value{Kind: content.FieldKind(row.Kind), Raw: row.Value}
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

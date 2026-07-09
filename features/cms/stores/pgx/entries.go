package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/cms/domain/content"
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

// entryRow is the store-local, db-tagged projection of an entries spine row that
// pgx.RowToStructByName scans into; toDomain maps it to the persistence-free
// domain entity (fields + terms hydrate separately).
type entryRow struct {
	ID          string     `db:"id"`
	Type        string     `db:"type"`
	Slug        string     `db:"slug"`
	Title       string     `db:"title"`
	Status      string     `db:"status"`
	Body        string     `db:"body"`
	Excerpt     string     `db:"excerpt"`
	Author      string     `db:"author"`
	Template    string     `db:"template"`
	ParentID    string     `db:"parent_id"`
	MenuOrder   int        `db:"menu_order"`
	PublishedAt *time.Time `db:"published_at"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
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
		PublishedAt: pgxdb.FromNullTimePtr(r.PublishedAt),
		CreatedAt:   r.CreatedAt.UTC(),
		UpdatedAt:   r.UpdatedAt.UTC(),
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
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		args := pgx.NamedArgs{
			"type":         e.Type,
			"slug":         e.Slug,
			"title":        e.Title,
			"status":       string(e.Status),
			"body":         e.Body,
			"excerpt":      e.Excerpt,
			"author":       e.Author,
			"template":     e.Template,
			"parent_id":    e.ParentID,
			"menu_order":   e.MenuOrder,
			"published_at": pgxdb.NullTimePtr(e.PublishedAt),
			"created_at":   e.CreatedAt.UTC(),
			"updated_at":   e.UpdatedAt.UTC(),
		}
		// Empty ID → the cryptids.Database strategy (amended D10): omit the id
		// column so the schema default generates the key, read back with RETURNING.
		// The generated key must be assigned before writeFields writes the child rows.
		if e.ID == "" {
			const q = `INSERT INTO entries (type, slug, title, status, body, excerpt, author, template, parent_id, menu_order, published_at, created_at, updated_at)
				VALUES (@type, @slug, @title, @status, @body, @excerpt, @author, @template, @parent_id, @menu_order, @published_at, @created_at, @updated_at)
				RETURNING id`
			if err := tx.QueryRow(ctx, q, args).Scan(&e.ID); err != nil {
				return err
			}
			return writeFields(ctx, tx, e.ID, e.Fields)
		}
		const q = `INSERT INTO entries (` + entryColumns + `)
			VALUES (@id, @type, @slug, @title, @status, @body, @excerpt, @author, @template, @parent_id, @menu_order, @published_at, @created_at, @updated_at)`
		args["id"] = e.ID
		if _, err := tx.Exec(ctx, q, args); err != nil {
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
		const q = `UPDATE entries
			SET type = @type, slug = @slug, title = @title, status = @status, body = @body,
				excerpt = @excerpt, author = @author, template = @template, parent_id = @parent_id,
				menu_order = @menu_order, published_at = @published_at, updated_at = @updated_at
			WHERE id = @id`
		n, err := pgxdb.ExecAffecting(ctx, tx, q, pgx.NamedArgs{
			"type":         e.Type,
			"slug":         e.Slug,
			"title":        e.Title,
			"status":       string(e.Status),
			"body":         e.Body,
			"excerpt":      e.Excerpt,
			"author":       e.Author,
			"template":     e.Template,
			"parent_id":    e.ParentID,
			"menu_order":   e.MenuOrder,
			"published_at": pgxdb.NullTimePtr(e.PublishedAt),
			"updated_at":   e.UpdatedAt.UTC(),
			"id":           id,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return crud.ErrNotFound
		}
		if _, err := tx.Exec(ctx, "DELETE FROM entry_fields WHERE entry_id = @entry_id", pgx.NamedArgs{"entry_id": id}); err != nil {
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
	const q = `SELECT ` + entryColumns + ` FROM entries WHERE id = @id`
	row, err := queryOne[entryRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return content.Entry{}, err
	}
	return s.hydrate(ctx, row.toDomain())
}

// GetBySlug returns the entry of type typ with the given slug.
func (s *EntryStore) GetBySlug(ctx context.Context, typ, slug string) (content.Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM entries WHERE type = @type AND slug = @slug`
	row, err := queryOne[entryRow](ctx, s.db, q, pgx.NamedArgs{"type": typ, "slug": slug})
	if err != nil {
		return content.Entry{}, err
	}
	return s.hydrate(ctx, row.toDomain())
}

// Delete removes the entry; its fields and terms cascade.
func (s *EntryStore) Delete(ctx context.Context, id string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM entries WHERE id = @id", pgx.NamedArgs{"id": id})
	if err != nil {
		return pgxdb.MapError(err)
	}
	if n == 0 {
		return crud.ErrNotFound
	}
	return nil
}

// List returns a page of entries matching q, in the resolved order (default
// created_at DESC, id DESC).
func (s *EntryStore) List(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
	where, args := entryFilter(q, "")
	return s.listPage(ctx, where, args, q.ListRequest)
}

// ListByTerm returns a page of entries matching q that are associated with
// termID, in the resolved order.
func (s *EntryStore) ListByTerm(ctx context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error) {
	where, args := entryFilter(q, termID)
	return s.listPage(ctx, where, args, q.ListRequest)
}

// entryFilter composes the type, optional term, and optional status filters into
// a parameterized WHERE fragment and its NamedArgs — never string concatenation
// of values. The list helper's keyset builder appends its predicate with AND;
// the count wrap reuses the same fragment and args.
func entryFilter(q content.EntryQuery, termID string) (string, pgx.NamedArgs) {
	where := " WHERE type = @type"
	args := pgx.NamedArgs{"type": q.Type}
	if termID != "" {
		where += " AND id IN (SELECT entry_id FROM entry_terms WHERE term_id = @term_id)"
		args["term_id"] = termID
	}
	if q.Status != "" {
		where += " AND status = @status"
		args["status"] = string(q.Status)
	}
	return where, args
}

// listPage runs the paginated spine query via pgxdb.List over the entry row
// struct, then hydrates fields + terms for the rows on this page only.
func (s *EntryStore) listPage(ctx context.Context, where string, args pgx.NamedArgs, req crud.ListRequest) (crud.Page[content.Entry], error) {
	lq := pgxdb.ListQuery[entryRow]{
		BaseSQL:      `SELECT ` + entryColumns + ` FROM entries` + where,
		Args:         args,
		OrderFields:  content.OrderFields,
		DefaultOrder: content.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r entryRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r entryRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, lq, req)
	if err != nil {
		return crud.Page[content.Entry]{}, err
	}
	domainPage := crud.MapPage(page, entryRow.toDomain)

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

// SetTerms replaces the entry's taxonomy associations in a single transaction —
// a DELETE of the prior rows and one UNNEST insert of the new set.
func (s *EntryStore) SetTerms(ctx context.Context, entryID string, termIDs []string) error {
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM entry_terms WHERE entry_id = @entry_id", pgx.NamedArgs{"entry_id": entryID}); err != nil {
			return err
		}
		if len(termIDs) == 0 {
			return nil
		}
		const q = `INSERT INTO entry_terms (entry_id, term_id)
			SELECT @entry_id, term_id FROM UNNEST(@term_ids::text[]) AS a(term_id)`
		_, err := tx.Exec(ctx, q, pgx.NamedArgs{"entry_id": entryID, "term_ids": termIDs})
		return err
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
	const q = `SELECT key, kind, value FROM entry_fields WHERE entry_id = @entry_id ORDER BY key, ordinal`
	rows, err := s.db.Query(ctx, q, pgx.NamedArgs{"entry_id": entryID})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	frows, err := pgx.CollectRows(rows, pgx.RowToStructByName[fieldRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	fields := content.Fields{}
	for _, f := range frows {
		fields[f.Key] = content.Value{Kind: content.FieldKind(f.Kind), Raw: f.Value}
	}
	return fields, nil
}

// loadTermIDs returns the taxonomy term IDs associated with entryID.
func (s *EntryStore) loadTermIDs(ctx context.Context, entryID string) ([]string, error) {
	const q = `SELECT term_id FROM entry_terms WHERE entry_id = @entry_id ORDER BY term_id`
	rows, err := s.db.Query(ctx, q, pgx.NamedArgs{"entry_id": entryID})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	return ids, nil
}

// writeFields inserts the entry's custom fields as one UNNEST statement. Callers
// run it inside the same Tx as the spine write (and after deleting prior rows on
// update). Flat fields use ordinal 0 (repeaters deferred).
func writeFields(ctx context.Context, tx *pgxdb.Tx, entryID string, fields content.Fields) error {
	if len(fields) == 0 {
		return nil
	}
	keys := make([]string, 0, len(fields))
	kinds := make([]string, 0, len(fields))
	values := make([]string, 0, len(fields))
	for key, v := range fields {
		keys = append(keys, key)
		kinds = append(kinds, string(v.Kind))
		values = append(values, v.Raw)
	}
	const q = `INSERT INTO entry_fields (entry_id, key, kind, value, ordinal)
		SELECT @entry_id, key, kind, value, 0
		FROM UNNEST(@keys::text[], @kinds::text[], @values::text[]) AS f(key, kind, value)`
	_, err := tx.Exec(ctx, q, pgx.NamedArgs{
		"entry_id": entryID,
		"keys":     keys,
		"kinds":    kinds,
		"values":   values,
	})
	return err
}

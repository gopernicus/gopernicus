package pgx

import (
	"context"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
)

// TestRecordIDBackstop_Live proves the record-class backstop (plan D3): a raw
// SQL insert that omits the id gets a UUIDv7 from the column default, for every
// table in generatedIDColumns, and the store's empty-id (sdk.DatabaseID) path
// reads one back.
func TestRecordIDBackstop_Live(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()
	schema := testSchema(t)
	db := openDB(t, dsn)
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db, schema)
	t.Cleanup(func() { truncate(t, db, schema) })

	now := time.Now().UTC()
	inserts := map[string]struct {
		sql  string
		args []any
	}{
		"terms":      {`(kind, slug, name, created_at, updated_at) VALUES ('category', 'raw', 'Raw', $1, $1)`, []any{now}},
		"menus":      {`(name, slug, created_at, updated_at) VALUES ('Raw', 'raw', $1, $1)`, []any{now}},
		"menu_items": {`(menu_id, label, created_at, updated_at) VALUES ('m-raw', 'Raw', $1, $1)`, []any{now}},
		"assets":     {`(filename, content_type, storage_key, created_at) VALUES ('raw.png', 'image/png', 'raw-key', $1)`, []any{now}},
		"inquiries":  {`(name, email, message, created_at) VALUES ('Raw', 'raw@example.com', 'raw', $1)`, []any{now}},
		"entries":    {`(type, slug, title, status, created_at, updated_at) VALUES ('article', 'raw', 'Raw', 'draft', $1, $1)`, []any{now}},
	}
	for _, g := range generatedIDColumns {
		t.Run(g.Table, func(t *testing.T) {
			ins, ok := inserts[g.Table]
			if !ok {
				t.Fatalf("no raw insert for generated-id table %s", g.Table)
			}
			var id string
			if err := db.QueryRow(ctx, `INSERT INTO `+schema.Table(g.Table)+` `+ins.sql+` RETURNING id`, ins.args...).Scan(&id); err != nil {
				t.Fatalf("raw insert: %v", err)
			}
			assertUUIDv7(t, g.Table, id)
		})
	}

	t.Run("entries store path", func(t *testing.T) {
		e := content.Entry{Type: "article", Slug: "store-backstop", Title: "Store backstop", Status: content.StatusDraft, Template: "default", CreatedAt: now, UpdatedAt: now}
		got, err := NewEntryStore(db, WithSchema(schema)).Create(ctx, e)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		assertUUIDv7(t, entriesTable, got.ID)
	})
}

// assertUUIDv7 fails unless id is a canonical lowercase UUID with version 7.
func assertUUIDv7(t *testing.T, table, id string) {
	t.Helper()
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("%s id %q is not a canonical UUID", table, id)
	}
	if id[14] != '7' {
		t.Fatalf("%s id %q has version nibble %q, want 7", table, id, id[14])
	}
	if v := id[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Fatalf("%s id %q has variant nibble %q, want RFC 9562 (8, 9, a or b)", table, id, v)
	}
}

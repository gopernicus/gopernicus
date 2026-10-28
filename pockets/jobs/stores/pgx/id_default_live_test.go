package pgx

import (
	"context"
	"testing"
	"time"
)

// TestRecordIDBackstop_Live proves the record-class backstop (plan D3): a raw
// SQL insert that omits the id gets a UUIDv7 from the column default, for every
// table in generatedIDColumns. The store itself never omits the id.
func TestRecordIDBackstop_Live(t *testing.T) {
	db, _ := openAndMigrate(t, requireDSN(t))
	ctx := context.Background()
	schema := testSchema(t)

	now := time.Now().UTC()
	inserts := map[string]string{
		"job_queue":        `(kind, scheduled_for, created_at, updated_at) VALUES ('raw', $1, $1, $1)`,
		"fenced_job_queue": `(kind, scheduled_for, created_at, updated_at) VALUES ('raw', $1, $1, $1)`,
		"job_schedules":    `(name, kind, every_secs, next_run_at, created_at, updated_at) VALUES ('raw', 'raw', 60, $1, $1, $1)`,
	}
	for _, g := range generatedIDColumns {
		t.Run(g.Table, func(t *testing.T) {
			ins, ok := inserts[g.Table]
			if !ok {
				t.Fatalf("no raw insert for generated-id table %s", g.Table)
			}
			var id string
			q := `INSERT INTO ` + schema.Table(g.Table) + ` ` + ins + ` RETURNING ` + g.Column
			if err := db.QueryRow(ctx, q, now).Scan(&id); err != nil {
				t.Fatalf("raw insert: %v", err)
			}
			assertUUIDv7(t, g.Table, id)
		})
	}
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

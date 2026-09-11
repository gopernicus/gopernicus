package pgxdb

import (
	"slices"
	"testing"
	"testing/fstest"
)

func TestMigrationFilesUseOneFlatStream(t *testing.T) {
	src := fstest.MapFS{
		"migrations/0002_second.sql":       {Data: []byte("SELECT 2;")},
		"migrations/0001_first.sql":        {Data: []byte("SELECT 1;")},
		"migrations/_disabled.sql":         {Data: []byte("SELECT 0;")},
		"migrations/README.md":             {Data: []byte("host notes")},
		"migrations/nested/0001_first.sql": {Data: []byte("different stream")},
		"migrations/nested/0003_third.sql": {Data: []byte("different stream")},
	}
	got, err := getMigrationFiles(src, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"0001_first.sql", "0002_second.sql"}; !slices.Equal(got, want) {
		t.Fatalf("migration files = %v, want %v", got, want)
	}
}

// TestMigrateConfig_WithSchema pins the option plumbing: no option leaves the
// zero Schema (today's unqualified SQL), and WithSchema carries a validated
// schema into the config. Invalid names are tested only through NewSchema —
// external code cannot construct an invalid Schema, so the option has no
// second validation seam.
func TestMigrateConfig_WithSchema(t *testing.T) {
	t.Run("no option keeps the default", func(t *testing.T) {
		var cfg migrateConfig
		for _, opt := range []MigrateOption(nil) {
			opt(&cfg)
		}
		if !cfg.schema.IsZero() {
			t.Fatalf("default schema = %q, want zero", cfg.schema)
		}
		if got := cfg.schema.Table(migrationsTable); got != migrationsTable {
			t.Errorf("ledger table = %q, want %q", got, migrationsTable)
		}
	})

	t.Run("carries the schema", func(t *testing.T) {
		s, err := NewSchema("auth")
		if err != nil {
			t.Fatalf("NewSchema: %v", err)
		}
		var cfg migrateConfig
		WithSchema(s)(&cfg)
		if cfg.schema.IsZero() || cfg.schema.String() != "auth" {
			t.Fatalf("schema = %q, want auth", cfg.schema)
		}
		if got, want := cfg.schema.Table(migrationsTable), `"auth".schema_migrations`; got != want {
			t.Errorf("ledger table = %q, want %q", got, want)
		}
	})

	t.Run("zero schema option is a no-op", func(t *testing.T) {
		var cfg migrateConfig
		WithSchema(Schema{})(&cfg)
		if !cfg.schema.IsZero() {
			t.Fatalf("schema = %q, want zero", cfg.schema)
		}
	})

	t.Run("last option wins", func(t *testing.T) {
		first, _ := NewSchema("one")
		second, _ := NewSchema("two")
		var cfg migrateConfig
		for _, opt := range []MigrateOption{WithSchema(first), WithSchema(second)} {
			opt(&cfg)
		}
		if cfg.schema.String() != "two" {
			t.Fatalf("schema = %q, want two", cfg.schema)
		}
	})
}

//go:build integration

package crud_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/database/crud"
	"github.com/gopernicus/gopernicus/infrastructure/database/crud/pgxq"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/fop"
	"github.com/gopernicus/gopernicus/workshop/testing/testpgx"
)

var (
	errThingNotFound = errors.New("thing not found")
	errNameTaken     = errors.New("name taken")
)

// thing exercises the Postgres-specific scan paths: text[] arrays, jsonb,
// timestamptz, and a generated tsvector column (kept out of Columns — the
// explicit select list is what makes generated columns a non-problem).
type thing struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Tags      []string  `db:"tags"`
	Meta      string    `db:"meta"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

type thingFilter struct {
	Name          *string
	CreatedAfter  *time.Time
	SearchTerm    *string
	AuthorizedIDs []string
}

type createThing struct {
	ID   string
	Name string
	Tags []string
	Meta string
	At   time.Time
}

type updateThing struct {
	Name *string
	Tags *[]string
}

const thingsSchema = `
CREATE TABLE things (
	id            text PRIMARY KEY,
	name          text NOT NULL UNIQUE,
	tags          text[] NOT NULL DEFAULT '{}',
	meta          jsonb NOT NULL DEFAULT '{}',
	search_vector tsvector GENERATED ALWAYS AS (to_tsvector('english', coalesce(name, ''))) STORED,
	created_at    timestamptz NOT NULL,
	updated_at    timestamptz NOT NULL
);`

func thingSpec(clock func() time.Time) crud.Spec[thing, thingFilter, createThing, updateThing] {
	return crud.Spec[thing, thingFilter, createThing, updateThing]{
		Table:   "things",
		PK:      "id",
		Columns: []string{"id", "name", "tags", "meta", "created_at", "updated_at"},
		Filters: func(f thingFilter) []crud.Pred {
			var preds []crud.Pred
			if f.Name != nil {
				preds = append(preds, crud.Pred{Col: "name", Op: crud.OpEq, Val: *f.Name})
			}
			if f.CreatedAfter != nil {
				preds = append(preds, crud.Pred{Col: "created_at", Op: crud.OpGT, Val: *f.CreatedAfter})
			}
			return preds
		},
		Search:        &crud.SearchSpec{Strategy: crud.SearchTSVector, Column: "search_vector"},
		SearchTerm:    func(f thingFilter) *string { return f.SearchTerm },
		AuthorizedIDs: func(f thingFilter) []string { return f.AuthorizedIDs },
		Creates: func(c createThing) []crud.Set {
			return []crud.Set{
				{Col: "id", Val: c.ID},
				{Col: "name", Val: c.Name},
				{Col: "tags", Val: c.Tags},
				{Col: "meta", Val: c.Meta},
				{Col: "created_at", Val: c.At},
				{Col: "updated_at", Val: c.At},
			}
		},
		Updates: func(u updateThing) []crud.Set {
			var sets []crud.Set
			if u.Name != nil {
				sets = append(sets, crud.Set{Col: "name", Val: *u.Name})
			}
			if u.Tags != nil {
				sets = append(sets, crud.Set{Col: "tags", Val: *u.Tags})
			}
			return sets
		},
		AutoNow:      []string{"updated_at"},
		OrderFields:  map[string]crud.OrderField{"created_at": {Col: "created_at"}, "name": {Col: "name", CastLower: true}},
		DefaultOrder: "created_at",
		MapError: func(err error) error {
			switch {
			case errors.Is(err, crud.ErrNotFound):
				return errThingNotFound
			case errors.Is(err, pgxdb.ErrDBDuplicatedEntry):
				return errNameTaken
			default:
				return err
			}
		},
		Now: clock,
	}
}

func TestPostgresCRUDIntegration(t *testing.T) {
	ctx := context.Background()
	tp := testpgx.SetupTestPGX(t, ctx, testpgx.WithMigrations(func(ctx context.Context, pool *pgxdb.Pool) error {
		_, err := pool.Exec(ctx, thingsSchema)
		return err
	}))
	defer tp.Cleanup(t)

	base := time.Date(2026, 6, 9, 12, 0, 0, 123456000, time.UTC) // microsecond precision: timestamptz resolution
	clock := base.Add(time.Hour)
	store, err := crud.NewStore(pgxq.New(tp.Pool), crud.PostgresDialect{}, thingSpec(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// Create with RETURNING: text[] and jsonb round-trip.
	created, err := store.Create(ctx, createThing{
		ID: "t1", Name: "blue widget", Tags: []string{"blue", "widget"},
		Meta: `{"color": "blue"}`, At: base,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(created.Tags) != 2 || created.Tags[0] != "blue" {
		t.Errorf("Tags = %v — text[] did not round-trip", created.Tags)
	}
	if created.Meta == "" {
		t.Errorf("Meta empty — jsonb did not round-trip")
	}
	if !created.CreatedAt.Equal(base) {
		t.Errorf("CreatedAt = %v, want %v", created.CreatedAt, base)
	}

	for i, name := range []string{"red gadget", "green gizmo", "blue spinner"} {
		if _, err := store.Create(ctx, createThing{
			ID: string(rune('a' + i)), Name: name, Tags: []string{"x"}, Meta: "{}",
			At: base.Add(time.Duration(i+1) * time.Minute),
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// Unique violation maps pgx SQLSTATE → pgxdb sentinel → domain error.
	// (pgx surfaces execution errors at rows.Err(); the adapter maps there.)
	if _, err := store.Create(ctx, createThing{ID: "dup", Name: "blue widget", Tags: []string{}, Meta: "{}", At: base}); !errors.Is(err, errNameTaken) {
		t.Errorf("duplicate err = %v, want errNameTaken", err)
	}

	// tsvector search against the generated column.
	term := "blue"
	order := fop.NewOrder("created_at", fop.ASC)
	records, err := store.List(ctx, thingFilter{SearchTerm: &term}, order, fop.PageStringCursor{Limit: 10}, false)
	if err != nil {
		t.Fatalf("tsvector search: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("search 'blue' = %d records, want 2 (widget, spinner)", len(records))
	}

	// Prefilter authorization with = ANY: empty non-nil blocks all.
	records, err = store.List(ctx, thingFilter{AuthorizedIDs: []string{}}, order, fop.PageStringCursor{Limit: 10}, false)
	if err != nil {
		t.Fatalf("authorized empty: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("empty AuthorizedIDs returned %d records", len(records))
	}

	// Tuple cursor across a page boundary with timestamptz order values.
	page1, err := store.List(ctx, thingFilter{}, order, fop.PageStringCursor{Limit: 2}, false)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	cursor, err := fop.EncodeCursor("created_at", page1[1].CreatedAt, page1[1].ID)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	page2, err := store.List(ctx, thingFilter{}, order, fop.PageStringCursor{Limit: 2, Cursor: cursor}, false)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID == page1[1].ID {
		t.Fatalf("page2 = %+v — cursor boundary wrong", page2)
	}

	// Partial update + AutoNow; RETURNING reflects the stored row.
	newTags := []string{"blue", "widget", "sale"}
	updated, err := store.Update(ctx, "t1", updateThing{Tags: &newTags})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(updated.Tags) != 3 || updated.Name != "blue widget" {
		t.Errorf("update result = %+v", updated)
	}
	if !updated.UpdatedAt.Equal(clock) {
		t.Errorf("UpdatedAt = %v, want auto-now %v", updated.UpdatedAt, clock)
	}

	// Delete + not-found mapping.
	if err := store.Delete(ctx, "t1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, "t1"); !errors.Is(err, errThingNotFound) {
		t.Errorf("get deleted = %v, want errThingNotFound", err)
	}
}

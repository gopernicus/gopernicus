package crud_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/database/crud"
	"github.com/gopernicus/gopernicus/infrastructure/database/crud/sqliteq"
	"github.com/gopernicus/gopernicus/infrastructure/database/sqlite/moderncdb"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

var (
	errUserNotFound = errors.New("user not found")
	errEmailTaken   = errors.New("email already registered")
)

// user mirrors the shape of a generated entity: string PK, nullable
// timestamp, record state, audit timestamps.
type user struct {
	UserID      string     `db:"user_id"`
	Email       string     `db:"email"`
	DisplayName string     `db:"display_name"`
	LastLoginAt *time.Time `db:"last_login_at"`
	RecordState string     `db:"record_state"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
}

type userFilter struct {
	Email          *string
	RecordState    *string
	CreatedAtAfter *time.Time
	SearchTerm     *string
	AuthorizedIDs  []string
}

type createUser struct {
	UserID      string
	Email       string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type updateUser struct {
	Email       *string
	DisplayName *string
	LastLoginAt *time.Time
	UpdatedAt   *time.Time
}

// userSpec is what the generator would emit for this entity — entirely
// declarative; every behavior lives in the generic store.
func userSpec(clock func() time.Time) crud.Spec[user, userFilter, createUser, updateUser] {
	return crud.Spec[user, userFilter, createUser, updateUser]{
		Table:   "users",
		PK:      "user_id",
		Columns: []string{"user_id", "email", "display_name", "last_login_at", "record_state", "created_at", "updated_at"},
		Filters: func(f userFilter) []crud.Pred {
			var preds []crud.Pred
			if f.Email != nil {
				preds = append(preds, crud.Pred{Col: "email", Op: crud.OpEq, Val: *f.Email})
			}
			if f.RecordState != nil {
				preds = append(preds, crud.Pred{Col: "record_state", Op: crud.OpEq, Val: *f.RecordState})
			}
			if f.CreatedAtAfter != nil {
				preds = append(preds, crud.Pred{Col: "created_at", Op: crud.OpGT, Val: *f.CreatedAtAfter})
			}
			return preds
		},
		Search:        &crud.SearchSpec{Strategy: crud.SearchContains, Fields: []string{"email", "display_name"}},
		SearchTerm:    func(f userFilter) *string { return f.SearchTerm },
		AuthorizedIDs: func(f userFilter) []string { return f.AuthorizedIDs },
		Creates: func(c createUser) []crud.Set {
			return []crud.Set{
				{Col: "user_id", Val: c.UserID},
				{Col: "email", Val: c.Email},
				{Col: "display_name", Val: c.DisplayName},
				{Col: "record_state", Val: "active"},
				{Col: "created_at", Val: c.CreatedAt},
				{Col: "updated_at", Val: c.UpdatedAt},
			}
		},
		Updates: func(u updateUser) []crud.Set {
			var sets []crud.Set
			if u.Email != nil {
				sets = append(sets, crud.Set{Col: "email", Val: *u.Email})
			}
			if u.DisplayName != nil {
				sets = append(sets, crud.Set{Col: "display_name", Val: *u.DisplayName})
			}
			if u.LastLoginAt != nil {
				sets = append(sets, crud.Set{Col: "last_login_at", Val: *u.LastLoginAt})
			}
			if u.UpdatedAt != nil {
				sets = append(sets, crud.Set{Col: "updated_at", Val: *u.UpdatedAt})
			}
			return sets
		},
		AutoNow:        []string{"updated_at"},
		RecordStateCol: "record_state",
		OrderFields: map[string]crud.OrderField{
			"created_at": {Col: "created_at"},
			"email":      {Col: "email", CastLower: true},
			"user_id":    {Col: "user_id"},
		},
		DefaultOrder: "created_at",
		MapError: func(err error) error {
			switch {
			case errors.Is(err, crud.ErrNotFound):
				return errUserNotFound
			case errors.Is(err, errs.ErrAlreadyExists):
				return errEmailTaken
			default:
				return err
			}
		},
		Now: clock,
	}
}

const usersSchema = `
CREATE TABLE users (
	user_id       TEXT PRIMARY KEY,
	email         TEXT NOT NULL UNIQUE,
	display_name  TEXT NOT NULL,
	last_login_at TEXT,
	record_state  TEXT NOT NULL DEFAULT 'active',
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);`

func newSQLiteStore(t *testing.T, clock func() time.Time) *crud.Store[user, userFilter, createUser, updateUser] {
	t.Helper()
	db, err := moderncdb.NewInMemory()
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.ExecSchema(context.Background(), usersSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	store, err := crud.NewStore(sqliteq.New(db), crud.SQLiteDialect{}, userSpec(clock))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

func seedUsers(t *testing.T, store *crud.Store[user, userFilter, createUser, updateUser], base time.Time, n int) []user {
	t.Helper()
	ctx := context.Background()
	out := make([]user, 0, n)
	names := []string{"Ada Lovelace", "Grace Hopper", "Barbara Liskov", "Frances Allen", "Katherine Johnson"}
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		rec, err := store.Create(ctx, createUser{
			UserID:      string(rune('a'+i)) + "-id",
			Email:       string(rune('a'+i)) + "@example.com",
			DisplayName: names[i%len(names)],
			CreatedAt:   ts,
			UpdatedAt:   ts,
		})
		if err != nil {
			t.Fatalf("seed create %d: %v", i, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestSQLiteCRUDLifecycle(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 9, 12, 0, 0, 123456789, time.UTC)
	clock := base.Add(time.Hour)
	store := newSQLiteStore(t, func() time.Time { return clock })

	seeded := seedUsers(t, store, base, 3)

	// Time round-trip: nanosecond precision survives the TEXT codec.
	got, err := store.Get(ctx, seeded[0].UserID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.CreatedAt.Equal(base) {
		t.Errorf("CreatedAt = %v, want %v (TEXT codec lost precision)", got.CreatedAt, base)
	}
	if got.LastLoginAt != nil {
		t.Errorf("LastLoginAt = %v, want nil", got.LastLoginAt)
	}

	// Duplicate create maps through moderncdb sentinel to the domain error.
	if _, err := store.Create(ctx, createUser{UserID: "dup-id", Email: "a@example.com", DisplayName: "Dup", CreatedAt: base, UpdatedAt: base}); !errors.Is(err, errEmailTaken) {
		t.Errorf("duplicate create err = %v, want errEmailTaken", err)
	}

	// Partial update: only display_name provided; AutoNow touches updated_at,
	// email is untouched, nullable timestamp set in a second update.
	newName := "Ada L."
	updated, err := store.Update(ctx, seeded[0].UserID, updateUser{DisplayName: &newName})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DisplayName != newName || updated.Email != seeded[0].Email {
		t.Errorf("update result = %+v", updated)
	}
	if !updated.UpdatedAt.Equal(clock) {
		t.Errorf("UpdatedAt = %v, want auto-now %v", updated.UpdatedAt, clock)
	}
	login := base.Add(30 * time.Minute)
	updated, err = store.Update(ctx, seeded[0].UserID, updateUser{LastLoginAt: &login})
	if err != nil {
		t.Fatalf("update login: %v", err)
	}
	if updated.LastLoginAt == nil || !updated.LastLoginAt.Equal(login) {
		t.Errorf("LastLoginAt = %v, want %v", updated.LastLoginAt, login)
	}

	// Empty update is a mapped sentinel.
	if _, err := store.Update(ctx, seeded[0].UserID, updateUser{}); !errors.Is(err, crud.ErrNoFieldsToUpdate) {
		t.Errorf("empty update err = %v, want ErrNoFieldsToUpdate", err)
	}

	// Record-state transition.
	if err := store.SetRecordState(ctx, seeded[1].UserID, "archived"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	archived := "archived"
	records, err := store.List(ctx, userFilter{RecordState: &archived}, fop.NewOrder("created_at", fop.ASC), fop.PageStringCursor{Limit: 10}, false)
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(records) != 1 || records[0].UserID != seeded[1].UserID {
		t.Errorf("archived list = %+v", records)
	}

	// Delete, then not-found mapping.
	if err := store.Delete(ctx, seeded[2].UserID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, seeded[2].UserID); !errors.Is(err, errUserNotFound) {
		t.Errorf("get deleted err = %v, want errUserNotFound", err)
	}
	if err := store.Delete(ctx, seeded[2].UserID); !errors.Is(err, errUserNotFound) {
		t.Errorf("double delete err = %v, want errUserNotFound", err)
	}
}

func TestSQLiteListFilterSearchAuthorization(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	store := newSQLiteStore(t, time.Now)
	seeded := seedUsers(t, store, base, 5)

	order := fop.NewOrder("created_at", fop.ASC)
	page := fop.PageStringCursor{Limit: 10}

	// Range filter on TEXT timestamps: lexical comparison must agree with
	// chronological order via the canonical write format.
	after := base.Add(90 * time.Second)
	records, err := store.List(ctx, userFilter{CreatedAtAfter: &after}, order, page, false)
	if err != nil {
		t.Fatalf("range list: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("range list = %d records, want 3", len(records))
	}

	// Search (contains strategy).
	term := "grace"
	records, err = store.List(ctx, userFilter{SearchTerm: &term}, order, page, false)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(records) != 1 || records[0].DisplayName != "Grace Hopper" {
		t.Errorf("search = %+v", records)
	}

	// Prefilter authorization: empty non-nil = no access; subset = scoped.
	records, err = store.List(ctx, userFilter{AuthorizedIDs: []string{}}, order, page, false)
	if err != nil {
		t.Fatalf("authorized empty: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("empty AuthorizedIDs returned %d records — cross-principal leak", len(records))
	}
	records, err = store.List(ctx, userFilter{AuthorizedIDs: []string{seeded[0].UserID, seeded[3].UserID}}, order, page, false)
	if err != nil {
		t.Fatalf("authorized subset: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("authorized subset = %d records, want 2", len(records))
	}
}

func TestSQLiteCursorPagination(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 9, 12, 0, 0, 500000000, time.UTC)
	store := newSQLiteStore(t, time.Now)
	seeded := seedUsers(t, store, base, 5)

	order := fop.NewOrder("created_at", fop.ASC)

	// Page 1: limit 2.
	page1, err := store.List(ctx, userFilter{}, order, fop.PageStringCursor{Limit: 2}, false)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].UserID != seeded[0].UserID || page1[1].UserID != seeded[1].UserID {
		t.Fatalf("page1 = %+v", page1)
	}

	// Encode a cursor from the last returned record (the Repository layer's
	// job via fop.TrimPage; done manually here) and fetch page 2.
	cursor, err := fop.EncodeCursor("created_at", page1[1].CreatedAt, page1[1].UserID)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	page2, err := store.List(ctx, userFilter{}, order, fop.PageStringCursor{Limit: 2, Cursor: cursor}, false)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].UserID != seeded[2].UserID || page2[1].UserID != seeded[3].UserID {
		t.Fatalf("page2 = %+v — records skipped or repeated across cursor boundary", page2)
	}

	// Previous-page probe from page 2's position scans in reverse. The tuple
	// comparison is strict (<), so the cursor record itself is excluded —
	// only seeded[0] lies strictly before it. This matches pgxdb's upstream
	// semantics (HasPrev is exact; PreviousCursor is approximate by one).
	prev, err := store.List(ctx, userFilter{}, order, fop.PageStringCursor{Limit: 2, Cursor: cursor}, true)
	if err != nil {
		t.Fatalf("prev probe: %v", err)
	}
	if len(prev) != 1 || prev[0].UserID != seeded[0].UserID {
		t.Fatalf("prev probe = %+v, want exactly [seeded[0]] (strict < excludes the cursor record)", prev)
	}

	// Stale cursor (different order field) degrades to first page.
	stale, err := fop.EncodeCursor("email", page1[1].Email, page1[1].UserID)
	if err != nil {
		t.Fatalf("encode stale: %v", err)
	}
	first, err := store.List(ctx, userFilter{}, order, fop.PageStringCursor{Limit: 2, Cursor: stale}, false)
	if err != nil {
		t.Fatalf("stale cursor list: %v", err)
	}
	if len(first) != 2 || first[0].UserID != seeded[0].UserID {
		t.Errorf("stale cursor should reset to first page, got %+v", first)
	}
}

// userStore demonstrates escape-hatch rung three: embed the generic store
// and hand-write methods the spec vocabulary cannot express, sharing the
// store's querier, dialect, and scanner.
type userStore struct {
	*crud.Store[user, userFilter, createUser, updateUser]
}

func (s *userStore) GetByEmail(ctx context.Context, email string) (user, error) {
	args := crud.NewArgs(s.Dialect())
	rows, err := s.Querier().Query(ctx,
		`SELECT user_id, email, display_name, last_login_at, record_state, created_at, updated_at
		 FROM users WHERE email = `+args.Add(email), args.Values()...)
	if err != nil {
		return user{}, err
	}
	return crud.ScanOne[user](rows, s.Dialect())
}

func TestSQLiteHandWrittenMethodEscapeHatch(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	store := &userStore{Store: newSQLiteStore(t, time.Now)}
	seeded := seedUsers(t, store.Store, base, 2)

	got, err := store.GetByEmail(ctx, seeded[1].Email)
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if got.UserID != seeded[1].UserID {
		t.Errorf("GetByEmail = %+v, want %s", got, seeded[1].UserID)
	}
	if _, err := store.GetByEmail(ctx, "nobody@example.com"); !errors.Is(err, crud.ErrNotFound) {
		t.Errorf("missing email err = %v, want ErrNotFound", err)
	}
	// Generic methods remain available on the embedding store.
	if _, err := store.Get(ctx, seeded[0].UserID); err != nil {
		t.Errorf("embedded Get: %v", err)
	}
}

func TestSQLiteRejectsTSVectorAtConstruction(t *testing.T) {
	db, err := moderncdb.NewInMemory()
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	spec := userSpec(time.Now)
	spec.Search = &crud.SearchSpec{Strategy: crud.SearchTSVector, Column: "search_vector"}

	if _, err := crud.NewStore(sqliteq.New(db), crud.SQLiteDialect{}, spec); err == nil {
		t.Fatal("tsvector on sqlite must fail at store construction, got nil error")
	}
}

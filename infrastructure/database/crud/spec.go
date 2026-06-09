package crud

import "time"

// Op is the closed predicate vocabulary. Anything it cannot say belongs in a
// Pred.Raw escape hatch or a hand-written store method — resist growing it
// toward a query DSL.
type Op int

const (
	OpEq Op = iota
	OpNotEq
	OpLT
	OpLTE
	OpGT
	OpGTE
	OpIn
	OpContains // substring match: ILIKE on Postgres, LIKE on SQLite
	OpIsNull
	OpNotNull
)

// SearchStrategy names a full-text search rendering strategy. Dialects
// validate strategies at store construction, so an unsupported combination
// (e.g. tsvector on SQLite) fails at generate/boot time, never at runtime.
type SearchStrategy string

const (
	// SearchContains ORs an OpContains predicate across Fields. Portable.
	SearchContains SearchStrategy = "contains"
	// SearchWebSearch builds a tsvector over Fields and matches with
	// websearch_to_tsquery. Postgres only.
	SearchWebSearch SearchStrategy = "web_search"
	// SearchTSVector matches a precomputed tsvector Column with
	// websearch_to_tsquery. Postgres only.
	SearchTSVector SearchStrategy = "tsvector"
)

// Pred is a single WHERE predicate. Val is ignored for OpIsNull/OpNotNull;
// for OpIn it must be a non-nil slice ([]string, []int64, ...). A Pred with
// Raw set bypasses the vocabulary entirely: the function receives the
// dialect and argument accumulator and returns a SQL fragment — rung two of
// the escape-hatch ladder.
type Pred struct {
	Col string
	Op  Op
	Val any
	Raw func(d Dialect, args *Args) (string, error)
}

// Set is one column assignment in an INSERT or UPDATE.
type Set struct {
	Col string
	Val any
}

// SearchSpec declares search intent; the dialect owns rendering.
type SearchSpec struct {
	Strategy SearchStrategy
	Fields   []string // searched columns (contains, web_search)
	Column   string   // precomputed tsvector column (tsvector)
	Config   string   // text search config, default "english" (Postgres strategies)
}

// OrderField maps an order key (from the API) to its column.
type OrderField struct {
	Col       string
	CastLower bool // wrap in LOWER() for case-insensitive text ordering
}

// Spec declares everything entity-specific about a CRUD store: the table
// shape, how filter/create/update structs map to SQL, and how driver errors
// map to domain errors. The generator emits one Spec per entity; the Store
// supplies all behavior.
type Spec[T any, F any, C any, U any] struct {
	Table   string
	PK      string
	Columns []string // full select/returning list, in scan order is irrelevant (scan is by name)

	// Filters maps the filter struct to predicates. Typically a sequence of
	// nil-checks — the data the old generated if-ladders encoded.
	Filters func(F) []Pred

	// Search declares the search strategy; SearchTerm extracts the term from
	// the filter struct (nil/empty disables search for that call).
	Search     *SearchSpec
	SearchTerm func(F) *string

	// AuthorizedIDs implements the prefilter authorization contract: nil
	// means unrestricted, empty non-nil means no access (renders a
	// constant-false predicate — never an empty IN).
	AuthorizedIDs func(F) []string

	// Creates / Updates map input structs to column assignments. Updates
	// returning zero sets yields ErrNoFieldsToUpdate.
	Creates func(C) []Set
	Updates func(U) []Set

	// AutoNow columns are set to the current UTC time on Update when the
	// update struct did not provide them (e.g. updated_at).
	AutoNow []string

	// RecordState enables SoftDelete/Archive/Restore (a record_state
	// column); empty disables them.
	RecordStateCol string

	// OrderFields whitelists orderable fields; DefaultOrder is used when the
	// requested field is unknown or empty.
	OrderFields  map[string]OrderField
	DefaultOrder string

	// MapError converts infrastructure sentinels (duplicate, FK, not found)
	// to domain errors. Applied to every returned error; nil disables.
	MapError func(error) error

	// Now overrides the clock (tests). Defaults to time.Now().UTC.
	Now func() time.Time
}

func (s *Spec[T, F, C, U]) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Spec[T, F, C, U]) mapErr(err error) error {
	if err == nil {
		return nil
	}
	if s.MapError != nil {
		return s.MapError(err)
	}
	return err
}

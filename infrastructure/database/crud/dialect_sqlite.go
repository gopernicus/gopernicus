package crud

import (
	"fmt"
	"strings"
	"time"
)

// sqliteTimeWriteLayout is the canonical TEXT timestamp format. Every write
// MUST use it: timestamps are compared lexically in SQL, so a format split
// would make range predicates silently wrong (an expired session that
// compares as unexpired).
const sqliteTimeWriteLayout = time.RFC3339Nano

// sqliteTimeReadLayouts are accepted when scanning, defensive against data
// written by other tools.
var sqliteTimeReadLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// SQLiteDialect renders SQLite SQL: ? placeholders, LIKE contains (a
// case-sensitivity regression vs ILIKE for non-ASCII, accepted), expanded IN
// lists, and TEXT timestamps in canonical RFC3339Nano UTC.
type SQLiteDialect struct{}

var _ Dialect = SQLiteDialect{}

func (SQLiteDialect) Name() string { return "sqlite" }

func (SQLiteDialect) Placeholder(int) string { return "?" }

func (SQLiteDialect) QuoteIdent(s string) (string, error) { return quoteIdent(s) }

func (SQLiteDialect) Contains(col, placeholder string) string {
	return col + " LIKE " + placeholder
}

func (SQLiteDialect) In(col string, vals []any, args *Args) string {
	// An empty IN must render constant-false: `IN ()` is a syntax error, and
	// anything permissive would leak rows across the prefilter authorization
	// boundary.
	if len(vals) == 0 {
		return "0=1"
	}
	placeholders := make([]string, len(vals))
	for i, v := range vals {
		placeholders[i] = args.Add(v)
	}
	return col + " IN (" + strings.Join(placeholders, ", ") + ")"
}

func (SQLiteDialect) ValidateSearch(s SearchSpec) error {
	switch s.Strategy {
	case SearchContains:
		return nil
	case SearchWebSearch, SearchTSVector:
		return fmt.Errorf("sqlite dialect: search strategy %q is not supported — declare a sqlite fallback (e.g. contains) for this entity", s.Strategy)
	default:
		return fmt.Errorf("sqlite dialect: unknown search strategy %q", s.Strategy)
	}
}

func (d SQLiteDialect) SearchPredicate(s SearchSpec, term string, args *Args) (string, error) {
	switch s.Strategy {
	case SearchContains:
		return renderContainsAcross(d, s.Fields, term, args)
	default:
		return "", fmt.Errorf("sqlite dialect: search strategy %q is not supported", s.Strategy)
	}
}

func (SQLiteDialect) TimeArg(t time.Time) any {
	return t.UTC().Format(sqliteTimeWriteLayout)
}

func (SQLiteDialect) WrapScanDest(dest any) any {
	switch d := dest.(type) {
	case *time.Time:
		return &sqliteTimeScanner{target: d}
	case **time.Time:
		return &sqliteNullTimeScanner{target: d}
	}
	return dest
}

// sqliteTimeScanner parses TEXT timestamps into a time.Time field.
type sqliteTimeScanner struct {
	target *time.Time
}

func (s *sqliteTimeScanner) Scan(src any) error {
	t, err := parseSQLiteTime(src)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("scan time: NULL into non-nullable time field")
	}
	*s.target = *t
	return nil
}

// sqliteNullTimeScanner parses nullable TEXT timestamps into *time.Time.
type sqliteNullTimeScanner struct {
	target **time.Time
}

func (s *sqliteNullTimeScanner) Scan(src any) error {
	t, err := parseSQLiteTime(src)
	if err != nil {
		return err
	}
	*s.target = t
	return nil
}

func parseSQLiteTime(src any) (*time.Time, error) {
	switch v := src.(type) {
	case nil:
		return nil, nil
	case time.Time:
		return &v, nil
	case string:
		for _, layout := range sqliteTimeReadLayouts {
			if t, err := time.Parse(layout, v); err == nil {
				return &t, nil
			}
		}
		return nil, fmt.Errorf("scan time: unrecognized timestamp %q", v)
	case []byte:
		return parseSQLiteTime(string(v))
	default:
		return nil, fmt.Errorf("scan time: unsupported source type %T", src)
	}
}

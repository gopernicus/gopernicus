package turso

import (
	"fmt"
	"reflect"
)

// columnScanner is the row surface ScanStruct needs: the result column names plus
// Scan. Only *sql.Rows satisfies it — *sql.Row exposes no Columns — so ScanStruct
// reads the current row of an iterator, never a single-row handle. Single-row
// reads route a QueryRow through Query and step once (see QueryOne) to reach a
// column-bearing row.
type columnScanner interface {
	Columns() ([]string, error)
	Scan(dest ...any) error
}

// ScanStruct scans the current row of rows into a new T by matching the result
// columns against T's `db:"..."` struct tags, binding each field's ADDRESS into
// rows.Scan so sql.Scanner wrapper types (Time/NullTime/Bool) run their
// conversions. It is the turso twin of pgx.RowToStructByName and STRICT-ONLY: it
// is a loud error when a result column has no matching tagged field, when a tagged
// field has no matching column, or when an exported field carries no `db` tag
// (use `db:"-"` to skip a field). T must be a struct. A NULL scanned into a plain
// (non-Scanner, non-pointer) field surfaces the driver's conversion error
// unswallowed — model a nullable column with turso.NullTime, sql.Null*, or a
// pointer. There is no Lax variant.
func ScanStruct[T any](rows columnScanner) (T, error) {
	var out T
	rv := reflect.ValueOf(&out).Elem()
	rt := rv.Type()
	if rt.Kind() != reflect.Struct {
		return out, fmt.Errorf("turso: ScanStruct type parameter must be a struct, got %s", rt.Kind())
	}

	cols, err := rows.Columns()
	if err != nil {
		return out, MapError(err)
	}

	byTag := make(map[string]int, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		tag, ok := f.Tag.Lookup("db")
		if !ok {
			return out, fmt.Errorf("turso: ScanStruct: exported field %s.%s has no db tag (add a db tag or `db:\"-\"` to skip)", rt.Name(), f.Name)
		}
		if tag == "-" {
			continue
		}
		if _, dup := byTag[tag]; dup {
			return out, fmt.Errorf("turso: ScanStruct: duplicate db tag %q on %s", tag, rt.Name())
		}
		byTag[tag] = i
	}

	dest := make([]any, len(cols))
	matched := make(map[string]struct{}, len(cols))
	for j, col := range cols {
		idx, ok := byTag[col]
		if !ok {
			return out, fmt.Errorf("turso: ScanStruct: result column %q has no matching db-tagged field on %s", col, rt.Name())
		}
		if _, dup := matched[col]; dup {
			return out, fmt.Errorf("turso: ScanStruct: result column %q appears more than once", col)
		}
		matched[col] = struct{}{}
		dest[j] = rv.Field(idx).Addr().Interface()
	}

	for tag := range byTag {
		if _, ok := matched[tag]; !ok {
			return out, fmt.Errorf("turso: ScanStruct: db-tagged field %q on %s has no matching result column", tag, rt.Name())
		}
	}

	if err := rows.Scan(dest...); err != nil {
		return out, MapError(err)
	}
	return out, nil
}

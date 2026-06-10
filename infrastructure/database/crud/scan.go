package crud

import (
	"fmt"
	"reflect"
	"sync"
)

// fieldIndexCache caches struct type → (db tag → field index) maps.
var fieldIndexCache sync.Map // reflect.Type → map[string][]int

// fieldIndexes returns the db-tag → field-index map for a struct type,
// walking embedded structs. Fields without a db tag are ignored.
func fieldIndexes(t reflect.Type) map[string][]int {
	if cached, ok := fieldIndexCache.Load(t); ok {
		return cached.(map[string][]int)
	}

	m := map[string][]int{}
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			idx := append(append([]int{}, prefix...), i)
			if f.Anonymous && f.Type.Kind() == reflect.Struct {
				walk(f.Type, idx)
				continue
			}
			tag := f.Tag.Get("db")
			if tag == "" || tag == "-" || !f.IsExported() {
				continue
			}
			if _, exists := m[tag]; !exists {
				m[tag] = idx
			}
		}
	}
	walk(t, nil)

	fieldIndexCache.Store(t, m)
	return m
}

// scanRows collects every row into T by column NAME, matching db struct
// tags. A result column with no matching tagged field is an error — schema
// drift surfaces immediately instead of silently mis-mapping fields (the
// failure mode of positional scans). The dialect may wrap destinations
// (e.g. SQLite TEXT timestamps).
func scanRows[T any](rows Rows, d Dialect) ([]T, error) {
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}

	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("scan target %T is not a struct", zero)
	}
	indexes := fieldIndexes(t)

	colIdx := make([][]int, len(cols))
	for i, c := range cols {
		idx, ok := indexes[c]
		if !ok {
			return nil, fmt.Errorf("scan %T: result column %q has no field with db:%q tag", zero, c, c)
		}
		colIdx[i] = idx
	}

	var out []T
	for rows.Next() {
		var rec T
		rv := reflect.ValueOf(&rec).Elem()
		dests := make([]any, len(cols))
		for i, idx := range colIdx {
			dests[i] = d.WrapScanDest(rv.FieldByIndex(idx).Addr().Interface())
		}
		if err := rows.Scan(dests...); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return out, nil
}

// ScanRows collects a result set into T values by column name — exported
// for hand-written store methods (escape-hatch rung three) so custom SQL
// shares the store's scanning, time decoding, and drift detection.
func ScanRows[T any](rows Rows, d Dialect) ([]T, error) {
	return scanRows[T](rows, d)
}

// ScanOne returns the single row of a result set, or ErrNotFound — the
// hand-written-method counterpart of ScanRows.
func ScanOne[T any](rows Rows, d Dialect) (T, error) {
	return scanOne[T](rows, d)
}

// scanOne returns the single row of a result set, or ErrNotFound.
func scanOne[T any](rows Rows, d Dialect) (T, error) {
	var zero T
	records, err := scanRows[T](rows, d)
	if err != nil {
		return zero, err
	}
	if len(records) == 0 {
		return zero, ErrNotFound
	}
	return records[0], nil
}

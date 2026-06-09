package crud

import (
	"context"
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/fop"
)

// Store implements the CRUD verbs for one entity from its Spec. It mirrors
// the generated Storer contract (List takes a forPrevious flag and returns
// raw over-fetched rows; the Repository layer applies fop.TrimPage), so a
// Store can sit behind an existing Repository unchanged.
//
// Hand-written methods are rung three of the escape ladder: embed *Store in
// an entity store struct and add methods using Querier()/Dialect().
type Store[T any, F any, C any, U any] struct {
	q    Querier
	d    Dialect
	spec Spec[T, F, C, U]

	quotedTable   string
	quotedPK      string
	quotedColumns []string
}

// NewStore validates the spec against the dialect and builds a store.
// Validation failures (bad identifiers, unsupported search strategy for the
// dialect, unknown default order) are construction-time errors — they must
// never surface as runtime query failures.
func NewStore[T any, F any, C any, U any](q Querier, d Dialect, spec Spec[T, F, C, U]) (*Store[T, F, C, U], error) {
	s := &Store[T, F, C, U]{q: q, d: d, spec: spec}

	var err error
	if s.quotedTable, err = d.QuoteIdent(spec.Table); err != nil {
		return nil, fmt.Errorf("crud: table: %w", err)
	}
	if s.quotedPK, err = d.QuoteIdent(spec.PK); err != nil {
		return nil, fmt.Errorf("crud: pk: %w", err)
	}
	if len(spec.Columns) == 0 {
		return nil, fmt.Errorf("crud: %s: no columns declared", spec.Table)
	}
	s.quotedColumns = make([]string, len(spec.Columns))
	for i, c := range spec.Columns {
		if s.quotedColumns[i], err = d.QuoteIdent(c); err != nil {
			return nil, fmt.Errorf("crud: %s: column: %w", spec.Table, err)
		}
	}
	if spec.Search != nil {
		if err := d.ValidateSearch(*spec.Search); err != nil {
			return nil, fmt.Errorf("crud: %s: %w", spec.Table, err)
		}
	}
	for key, of := range spec.OrderFields {
		if !validIdent(of.Col) {
			return nil, fmt.Errorf("crud: %s: order field %q: invalid column %q", spec.Table, key, of.Col)
		}
	}
	if spec.DefaultOrder != "" {
		if _, ok := spec.OrderFields[spec.DefaultOrder]; !ok {
			return nil, fmt.Errorf("crud: %s: default order %q is not in OrderFields", spec.Table, spec.DefaultOrder)
		}
	}
	if spec.RecordStateCol != "" && !validIdent(spec.RecordStateCol) {
		return nil, fmt.Errorf("crud: %s: invalid record state column %q", spec.Table, spec.RecordStateCol)
	}
	return s, nil
}

// Querier exposes the execution surface for hand-written methods.
func (s *Store[T, F, C, U]) Querier() Querier { return s.q }

// Dialect exposes the dialect for hand-written methods.
func (s *Store[T, F, C, U]) Dialect() Dialect { return s.d }

func (s *Store[T, F, C, U]) selectList() string {
	return strings.Join(s.quotedColumns, ", ")
}

// List queries with filters, search, prefilter authorization, keyset cursor
// pagination, and ordering. It returns raw rows (callers over-fetch and trim
// via fop.TrimPage). forPrevious reverses the scan for HasPrev probes;
// reversal of the returned slice is the caller's concern, matching the
// existing Storer contract.
func (s *Store[T, F, C, U]) List(ctx context.Context, filter F, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]T, error) {
	args := NewArgs(s.d)
	var conds []string

	if s.spec.Filters != nil {
		for _, p := range s.spec.Filters(filter) {
			frag, err := renderPred(s.d, p, args)
			if err != nil {
				return nil, fmt.Errorf("filter: %w", err)
			}
			conds = append(conds, frag)
		}
	}

	if s.spec.AuthorizedIDs != nil {
		if ids := s.spec.AuthorizedIDs(filter); ids != nil {
			conds = append(conds, s.d.In(s.quotedPK, anySlice(ids), args))
		}
	}

	if s.spec.Search != nil && s.spec.SearchTerm != nil {
		if term := s.spec.SearchTerm(filter); term != nil && *term != "" {
			frag, err := s.d.SearchPredicate(*s.spec.Search, *term, args)
			if err != nil {
				return nil, fmt.Errorf("search: %w", err)
			}
			conds = append(conds, frag)
		}
	}

	of := s.orderField(orderBy.Field)

	// Keyset cursor: tuple comparison against the cursor position. Row-value
	// syntax is shared by Postgres and SQLite (>= 3.15).
	if page.Cursor != "" {
		cursor, err := fop.DecodeCursor(page.Cursor, of.Col)
		if err != nil {
			return nil, fmt.Errorf("cursor: %w", err)
		}
		if cursor != nil {
			quotedOrder, err := s.d.QuoteIdent(of.Col)
			if err != nil {
				return nil, fmt.Errorf("cursor order field: %w", err)
			}
			op := cursorOperator(orderBy.Direction, forPrevious)
			orderPh := args.Add(timeAware(s.d, cursor.OrderValue))
			pkPh := args.Add(cursor.PK)
			if of.CastLower {
				conds = append(conds, fmt.Sprintf("(LOWER(%s), %s) %s (LOWER(%s), %s)", quotedOrder, s.quotedPK, op, orderPh, pkPh))
			} else {
				conds = append(conds, fmt.Sprintf("(%s, %s) %s (%s, %s)", quotedOrder, s.quotedPK, op, orderPh, pkPh))
			}
		}
	}

	var b strings.Builder
	b.WriteString("SELECT " + s.selectList() + " FROM " + s.quotedTable)
	if len(conds) > 0 {
		b.WriteString(" WHERE " + strings.Join(conds, " AND "))
	}

	if err := s.writeOrderBy(&b, of, orderBy.Direction, forPrevious); err != nil {
		return nil, err
	}

	limit := page.Limit
	if limit <= 0 {
		limit = 50
	}
	b.WriteString(" LIMIT " + args.Add(limit))

	rows, err := s.q.Query(ctx, b.String(), args.Values()...)
	if err != nil {
		return nil, s.spec.mapErr(err)
	}
	records, err := scanRows[T](rows, s.d)
	if err != nil {
		return nil, s.spec.mapErr(err)
	}
	return records, nil
}

// Get fetches one record by primary key.
func (s *Store[T, F, C, U]) Get(ctx context.Context, id string) (T, error) {
	var zero T
	args := NewArgs(s.d)
	query := "SELECT " + s.selectList() + " FROM " + s.quotedTable + " WHERE " + s.quotedPK + " = " + args.Add(id)

	rows, err := s.q.Query(ctx, query, args.Values()...)
	if err != nil {
		return zero, s.spec.mapErr(err)
	}
	record, err := scanOne[T](rows, s.d)
	if err != nil {
		return zero, s.spec.mapErr(err)
	}
	return record, nil
}

// Create inserts a record and returns the stored row.
func (s *Store[T, F, C, U]) Create(ctx context.Context, input C) (T, error) {
	var zero T
	if s.spec.Creates == nil {
		return zero, fmt.Errorf("crud: %s: no Creates mapping declared", s.spec.Table)
	}

	sets := s.spec.Creates(input)
	if len(sets) == 0 {
		return zero, fmt.Errorf("crud: %s: create resolved to no columns", s.spec.Table)
	}

	args := NewArgs(s.d)
	cols := make([]string, len(sets))
	placeholders := make([]string, len(sets))
	for i, set := range sets {
		col, err := s.d.QuoteIdent(set.Col)
		if err != nil {
			return zero, fmt.Errorf("create: %w", err)
		}
		cols[i] = col
		placeholders[i] = args.Add(timeAware(s.d, set.Val))
	}

	query := "INSERT INTO " + s.quotedTable + " (" + strings.Join(cols, ", ") + ") VALUES (" +
		strings.Join(placeholders, ", ") + ") RETURNING " + s.selectList()

	rows, err := s.q.Query(ctx, query, args.Values()...)
	if err != nil {
		return zero, s.spec.mapErr(err)
	}
	record, err := scanOne[T](rows, s.d)
	if err != nil {
		return zero, s.spec.mapErr(err)
	}
	return record, nil
}

// Update applies the non-nil fields of the update struct plus AutoNow
// columns, returning the stored row.
func (s *Store[T, F, C, U]) Update(ctx context.Context, id string, input U) (T, error) {
	var zero T
	if s.spec.Updates == nil {
		return zero, fmt.Errorf("crud: %s: no Updates mapping declared", s.spec.Table)
	}

	sets := s.spec.Updates(input)
	if len(sets) == 0 {
		return zero, s.spec.mapErr(ErrNoFieldsToUpdate)
	}

	provided := make(map[string]bool, len(sets))
	for _, set := range sets {
		provided[set.Col] = true
	}
	for _, col := range s.spec.AutoNow {
		if !provided[col] {
			sets = append(sets, Set{Col: col, Val: s.spec.now()})
		}
	}

	args := NewArgs(s.d)
	clauses := make([]string, len(sets))
	for i, set := range sets {
		col, err := s.d.QuoteIdent(set.Col)
		if err != nil {
			return zero, fmt.Errorf("update: %w", err)
		}
		clauses[i] = col + " = " + args.Add(timeAware(s.d, set.Val))
	}

	query := "UPDATE " + s.quotedTable + " SET " + strings.Join(clauses, ", ") +
		" WHERE " + s.quotedPK + " = " + args.Add(id) + " RETURNING " + s.selectList()

	rows, err := s.q.Query(ctx, query, args.Values()...)
	if err != nil {
		return zero, s.spec.mapErr(err)
	}
	record, err := scanOne[T](rows, s.d)
	if err != nil {
		return zero, s.spec.mapErr(err)
	}
	return record, nil
}

// Delete removes a record by primary key. ErrNotFound when nothing matched.
func (s *Store[T, F, C, U]) Delete(ctx context.Context, id string) error {
	args := NewArgs(s.d)
	query := "DELETE FROM " + s.quotedTable + " WHERE " + s.quotedPK + " = " + args.Add(id)

	affected, err := s.q.Exec(ctx, query, args.Values()...)
	if err != nil {
		return s.spec.mapErr(err)
	}
	if affected == 0 {
		return s.spec.mapErr(ErrNotFound)
	}
	return nil
}

// SetRecordState transitions the record-state column (soft delete, archive,
// restore), touching AutoNow columns. ErrNotFound when nothing matched.
func (s *Store[T, F, C, U]) SetRecordState(ctx context.Context, id, state string) error {
	if s.spec.RecordStateCol == "" {
		return fmt.Errorf("crud: %s: no record state column declared", s.spec.Table)
	}

	args := NewArgs(s.d)
	stateCol, err := s.d.QuoteIdent(s.spec.RecordStateCol)
	if err != nil {
		return fmt.Errorf("record state: %w", err)
	}
	clauses := []string{stateCol + " = " + args.Add(state)}
	for _, col := range s.spec.AutoNow {
		quoted, err := s.d.QuoteIdent(col)
		if err != nil {
			return fmt.Errorf("record state: %w", err)
		}
		clauses = append(clauses, quoted+" = "+args.Add(timeAware(s.d, s.spec.now())))
	}

	query := "UPDATE " + s.quotedTable + " SET " + strings.Join(clauses, ", ") +
		" WHERE " + s.quotedPK + " = " + args.Add(id)

	affected, err := s.q.Exec(ctx, query, args.Values()...)
	if err != nil {
		return s.spec.mapErr(err)
	}
	if affected == 0 {
		return s.spec.mapErr(ErrNotFound)
	}
	return nil
}

func (s *Store[T, F, C, U]) orderField(requested string) OrderField {
	if of, ok := s.spec.OrderFields[requested]; ok {
		return of
	}
	if of, ok := s.spec.OrderFields[s.spec.DefaultOrder]; ok {
		return of
	}
	return OrderField{Col: s.spec.PK}
}

func (s *Store[T, F, C, U]) writeOrderBy(b *strings.Builder, of OrderField, direction string, forPrevious bool) error {
	quotedOrder, err := s.d.QuoteIdent(of.Col)
	if err != nil {
		return fmt.Errorf("order field: %w", err)
	}

	dir := strings.ToUpper(direction)
	if dir != fop.DESC {
		dir = fop.ASC
	}
	if forPrevious {
		if dir == fop.ASC {
			dir = fop.DESC
		} else {
			dir = fop.ASC
		}
	}

	orderExpr := quotedOrder
	if of.CastLower {
		orderExpr = "LOWER(" + quotedOrder + ")"
	}

	b.WriteString(" ORDER BY " + orderExpr + " " + dir)
	if of.Col != s.spec.PK {
		b.WriteString(", " + s.quotedPK + " " + dir)
	}
	return nil
}

// cursorOperator picks the tuple-comparison operator for the scan direction.
func cursorOperator(direction string, forPrevious bool) string {
	desc := strings.EqualFold(direction, fop.DESC)
	if desc != forPrevious {
		return "<"
	}
	return ">"
}

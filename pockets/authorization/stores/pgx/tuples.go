package pgx

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync/atomic"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/tuplekey"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	jackpgx "github.com/jackc/pgx/v5"
)

const tupleColumns = "scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation"
const canonicalTupleKeyExpr = "('2' || chr(1) || scope_kind::text || chr(1) || resource_type || chr(1) || resource_id || chr(1) || relation || chr(1) || subject_type || chr(1) || subject_id || chr(1) || subject_relation) COLLATE \"C\""
const tupleTransportBatch = 100

type tupleStore struct {
	db          *pgxdb.DB
	cfg         config
	readQuerier pgxdb.Querier
	viewContext context.Context
	closed      *atomic.Bool
}

func newTupleStore(db *pgxdb.DB, cfg config) *tupleStore { return &tupleStore{db: db, cfg: cfg} }

var _ tuples.Storer = (*tupleStore)(nil)

func (s *tupleStore) TupleCacheBinding() string { return s.cfg.tupleBinding }
func (s *tupleStore) table() string             { return s.cfg.schema.Table("iam_tuples") }
func (s *tupleStore) reader(ctx context.Context) pgxdb.Querier {
	if s.readQuerier != nil {
		return s.readQuerier
	}
	return s.db.QuerierFrom(ctx)
}
func (s *tupleStore) check(ctx context.Context) error {
	if s.closed != nil && s.closed.Load() {
		return tuples.ErrSnapshotClosed
	}
	if s.viewContext != nil {
		if err := s.viewContext.Err(); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type tupleArgs struct{ values []any }

func (a *tupleArgs) bind(v any) string {
	a.values = append(a.values, v)
	return fmt.Sprintf("@p%d", len(a.values)) + func() string {
		switch v.(type) {
		case int, int64:
			return "::bigint"
		}
		return ""
	}()
}
func (a *tupleArgs) ref(v string) string { return a.bind(v) + `::text COLLATE "C"` }
func (a *tupleArgs) params() []any       { return []any{a.named()} }
func (a *tupleArgs) fact(f tuples.Tuple) string {
	return "(" + strings.Join([]string{a.bind(int(f.Scope.Kind)), a.ref(f.Scope.Type), a.ref(f.Scope.ID), a.ref(f.Relation), a.ref(f.Subject.Type), a.ref(f.Subject.ID), a.ref(f.Subject.Relation)}, ",") + ")"
}
func tuplePredicate(a *tupleArgs, f tuples.Tuple) string {
	return "(" + tupleColumns + ")=" + a.fact(f)
}
func tupleWhere(a *tupleArgs, q tuples.Query) (string, error) {
	if err := q.Validate(); err != nil {
		return "", err
	}
	where := " WHERE 1=1"
	if q.Scope != nil {
		where += " AND scope_kind=" + a.bind(int(q.Scope.Kind)) + " AND resource_type=" + a.ref(q.Scope.Type) + " AND resource_id=" + a.ref(q.Scope.ID)
	}
	if q.ResourceOnly {
		where += " AND scope_kind=2"
	}
	if q.ResourceType != "" {
		where += " AND scope_kind=2 AND resource_type=" + a.ref(q.ResourceType)
	}
	if q.Relation != "" {
		where += " AND relation=" + a.ref(q.Relation)
	}
	if q.Subject != nil {
		where += " AND subject_type=" + a.ref(q.Subject.Type) + " AND subject_id=" + a.ref(q.Subject.ID) + " AND subject_relation=" + a.ref(q.Subject.Relation)
	}
	if q.SubjectType != "" {
		where += " AND subject_type=" + a.ref(q.SubjectType)
	}
	if q.SubjectID != "" {
		where += " AND subject_id=" + a.ref(q.SubjectID)
	}
	if q.ConcreteOnly {
		where += " AND subject_relation=''"
	}
	if q.After != nil {
		where += " AND (" + tupleColumns + ")>" + a.fact(*q.After)
	}
	return where, nil
}
func (s *tupleStore) Contains(ctx context.Context, f tuples.Tuple) (bool, error) {
	if err := f.Validate(); err != nil {
		return false, err
	}
	if err := s.check(ctx); err != nil {
		return false, err
	}
	a := &tupleArgs{}
	query := "SELECT EXISTS(SELECT 1 FROM " + s.table() + " WHERE " + tuplePredicate(a, f) + ")"
	var found bool
	err := s.reader(ctx).QueryRow(ctx, query, a.params()...).Scan(&found)
	return found, pgxdb.MapError(err)
}
func (s *tupleStore) ContainsMany(ctx context.Context, facts []tuples.Tuple) ([]bool, error) {
	for _, f := range facts {
		if err := f.Validate(); err != nil {
			return nil, err
		}
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if s.readQuerier == nil {
		var out []bool
		err := s.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
			var e error
			out, e = r.ContainsMany(ctx, facts)
			return e
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	out := make([]bool, len(facts))
	for offset := 0; offset < len(facts); offset += tupleTransportBatch {
		end := min(offset+tupleTransportBatch, len(facts))
		a := &tupleArgs{}
		values := make([]string, 0, end-offset)
		for i := offset; i < end; i++ {
			index := a.bind(i)
			fact := a.fact(facts[i])
			values = append(values, "("+index+","+fact[1:])
		}
		equality := []string{}
		for _, field := range strings.Split(tupleColumns, ", ") {
			equality = append(equality, "t."+field+"=r."+field)
		}
		query := "WITH requested(n," + tupleColumns + ") AS (VALUES " + strings.Join(values, ",") + ") SELECT r.n,EXISTS(SELECT 1 FROM " + s.table() + " t WHERE " + strings.Join(equality, " AND ") + ") FROM requested r"
		rows, err := s.reader(ctx).Query(ctx, query, a.params()...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var n int
			var held bool
			if err := rows.Scan(&n, &held); err != nil {
				rows.Close()
				return nil, pgxdb.MapError(err)
			}
			out[n] = held
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, pgxdb.MapError(err)
		}
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *tupleStore) ReadSets(ctx context.Context, keys []tuples.SetKey, maxResults int) ([][]tuples.Tuple, error) {
	if maxResults < 0 {
		return nil, sdk.ErrInvalidInput
	}
	for _, k := range keys {
		if err := k.Validate(); err != nil {
			return nil, err
		}
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if s.readQuerier == nil {
		var out [][]tuples.Tuple
		err := s.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
			var e error
			out, e = r.ReadSets(ctx, keys, maxResults)
			return e
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	out := make([][]tuples.Tuple, len(keys))
	count := 0
	for offset := 0; offset < len(keys); offset += tupleTransportBatch {
		end := min(offset+tupleTransportBatch, len(keys))
		a := &tupleArgs{}
		values := make([]string, 0, end-offset)
		for i := offset; i < end; i++ {
			k := keys[i]
			reverse := 0
			if k.Reverse {
				reverse = 1
			}
			values = append(values, "("+strings.Join([]string{a.bind(i), a.bind(reverse), a.bind(int(k.Scope.Kind)), a.ref(k.Scope.Type), a.ref(k.Scope.ID), a.ref(k.Relation), a.ref(k.Subject.Type), a.ref(k.Subject.ID), a.ref(k.Subject.Relation)}, ",")+")")
		}
		selected := []string{}
		for _, field := range strings.Split(tupleColumns, ", ") {
			selected = append(selected, "t."+field)
		}
		query := "WITH requested(n,reverse," + tupleColumns + ") AS (VALUES " + strings.Join(values, ",") + ") SELECT r.n," + strings.Join(selected, ",") + " FROM requested r JOIN " + s.table() + " t ON ((r.reverse=1 AND t.subject_type=r.subject_type AND t.subject_id=r.subject_id AND t.subject_relation=r.subject_relation) OR (r.reverse=0 AND t.scope_kind=r.scope_kind AND t.resource_type=r.resource_type AND t.resource_id=r.resource_id AND t.relation=r.relation)) ORDER BY r.n," + strings.Join(selected, ",")
		if maxResults > 0 {
			query += " LIMIT " + a.bind(maxResults-count+1)
		}
		rows, err := s.reader(ctx).Query(ctx, query, a.params()...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var n int
			var f tuples.Tuple
			if err := rows.Scan(&n, &f.Scope.Kind, &f.Scope.Type, &f.Scope.ID, &f.Relation, &f.Subject.Type, &f.Subject.ID, &f.Subject.Relation); err != nil {
				rows.Close()
				return nil, pgxdb.MapError(err)
			}
			if err := f.Validate(); err != nil {
				rows.Close()
				return nil, err
			}
			count++
			if maxResults > 0 && count > maxResults {
				rows.Close()
				return nil, tuples.ErrReadLimit
			}
			out[n] = append(out[n], f)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, pgxdb.MapError(err)
		}
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *tupleStore) Lookup(ctx context.Context, q tuples.Query) ([]tuples.Tuple, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	a := &tupleArgs{}
	where, err := tupleWhere(a, q)
	if err != nil {
		return nil, err
	}
	return s.lookupWhere(ctx, where, a, q.Limit)
}

func (s *tupleStore) lookupWhere(ctx context.Context, where string, a *tupleArgs, limit int) ([]tuples.Tuple, error) {
	query := "SELECT " + tupleColumns + " FROM " + s.table() + where + " ORDER BY " + tupleColumns
	if limit > 0 {
		query += " LIMIT " + a.bind(limit)
	}
	rows, err := s.reader(ctx).Query(ctx, query, a.params()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tuples.Tuple
	for rows.Next() {
		var f tuples.Tuple
		if err := rows.Scan(&f.Scope.Kind, &f.Scope.Type, &f.Scope.ID, &f.Relation, &f.Subject.Type, &f.Subject.ID, &f.Subject.Relation); err != nil {
			return nil, pgxdb.MapError(err)
		}
		if err := f.Validate(); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.MapError(err)
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

type tupleRow struct {
	ScopeKind       int    `db:"scope_kind"`
	ResourceType    string `db:"resource_type"`
	ResourceID      string `db:"resource_id"`
	Relation        string `db:"relation"`
	SubjectType     string `db:"subject_type"`
	SubjectID       string `db:"subject_id"`
	SubjectRelation string `db:"subject_relation"`
	TupleKey        string `db:"tuple_key"`
}

func (r tupleRow) tuple() tuples.Tuple {
	return tuples.Tuple{Scope: tuples.Scope{Kind: tuples.ScopeKind(r.ScopeKind), Type: r.ResourceType, ID: r.ResourceID}, Relation: r.Relation, Subject: tuples.SubjectRef{Type: r.SubjectType, ID: r.SubjectID, Relation: r.SubjectRelation}}
}
func (s *tupleStore) ListTuples(ctx context.Context, q tuples.Query, req list.Request) (list.Page[tuples.Tuple], error) {
	var empty list.Page[tuples.Tuple]
	if q.After != nil || q.Limit != 0 || strings.TrimSpace(req.Search) != "" {
		return empty, fmt.Errorf("tuple listing cannot contain search or a second cursor/limit: %w", sdk.ErrInvalidInput)
	}
	if err := req.Validate(); err != nil {
		return empty, err
	}
	if err := validateTupleCursor(req); err != nil {
		return empty, err
	}
	if err := q.Validate(); err != nil {
		return empty, err
	}
	if s.readQuerier == nil {
		var out list.Page[tuples.Tuple]
		err := s.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
			var e error
			out, e = r.(*tupleStore).ListTuples(ctx, q, req)
			return e
		})
		if err != nil {
			return empty, err
		}
		return out, nil
	}
	if err := s.check(ctx); err != nil {
		return empty, err
	}
	a := &tupleArgs{}
	where, err := tupleWhere(a, q)
	if err != nil {
		return empty, err
	}
	query := pgxdb.ListQuery[tupleRow]{
		BaseSQL:     "SELECT " + tupleColumns + ",tuple_key FROM (SELECT " + tupleColumns + "," + canonicalTupleKeyExpr + " AS tuple_key FROM " + s.table() + where + ") AS t WHERE 1=1",
		Args:        a.named(),
		OrderFields: map[string]list.OrderField{"tuple_key": {Column: "tuple_key"}}, DefaultOrder: list.NewOrder("tuple_key", list.ASC), PK: "tuple_key",
		PKOf: func(r tupleRow) string { return r.TupleKey }, OrderValueOf: func(r tupleRow, _ string) any { return r.TupleKey },
	}
	page, err := pgxdb.List(ctx, s.reader(ctx), query, req)
	if err != nil {
		return empty, err
	}
	for _, row := range page.Items {
		if err := row.tuple().Validate(); err != nil {
			return empty, err
		}
	}
	if err := s.check(ctx); err != nil {
		return empty, err
	}
	return list.MapPage(page, tupleRow.tuple), nil
}
func (s *tupleStore) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) (err error) {
	if fn == nil {
		return fmt.Errorf("nil tuple snapshot callback: %w", sdk.ErrInvalidInput)
	}
	if err := s.check(ctx); err != nil {
		return err
	}
	reader := *s
	var owned *pgxdb.Tx
	if reader.readQuerier == nil {
		if ambient, ok := pgxdb.TxFromContext(ctx); ok {
			suitable, e := ambient.SnapshotIsolation(ctx)
			if e != nil {
				return e
			}
			if !suitable {
				return tuples.ErrSnapshotIsolation
			}
			reader.readQuerier = ambient
		} else {
			owned, err = s.db.BeginRead(ctx)
			if err != nil {
				return err
			}
			defer func() {
				if rollbackErr := owned.Rollback(); err != nil && rollbackErr != nil && !errors.Is(rollbackErr, jackpgx.ErrTxClosed) {
					err = errors.Join(err, rollbackErr)
				}
			}()
			reader.readQuerier = owned
		}
	}
	// BEGIN alone does not fix a SQL snapshot. Pin it before the callback can
	// interleave another committed writer, even when the table is empty.
	var pinned bool
	if err := reader.readQuerier.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+reader.table()+")").Scan(&pinned); err != nil {
		return err
	}
	reader.closed = &atomic.Bool{}
	reader.viewContext = ctx
	defer reader.closed.Store(true)
	if err = fn(ctx, &reader); err != nil {
		return err
	}
	reader.closed.Store(true)
	if err = ctx.Err(); err != nil {
		return err
	}
	if owned != nil {
		return owned.Commit()
	}
	return nil
}
func (s *tupleStore) ApplyTuples(ctx context.Context, changes tuples.Changes) error {
	if err := changes.Validate(); err != nil {
		return err
	}
	if len(changes.Add)+len(changes.Remove) > 4096 {
		return fmt.Errorf("tuple batch exceeds 4096: %w", sdk.ErrInvalidInput)
	}
	return runWrite(ctx, s.db, s.cfg, func(tx *writeTx) error { return applyTupleChanges(ctx, tx, s.cfg, changes) })
}
func (s *tupleStore) ReconcileTuples(ctx context.Context, scope tuples.Scope, relation string, subjects []tuples.SubjectRef) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := tuples.ValidateRefField("relation", relation); err != nil {
		return err
	}
	if len(subjects) > 4096 {
		return sdk.ErrInvalidInput
	}
	desired := map[tuples.Tuple]struct{}{}
	for _, subject := range subjects {
		if err := subject.Validate(); err != nil {
			return err
		}
		desired[tuples.Tuple{Scope: scope, Relation: relation, Subject: subject}] = struct{}{}
	}
	return runWrite(ctx, s.db, s.cfg, func(tx *writeTx) error {
		tx.touch(scope)
		reader := *s
		reader.readQuerier = tx.Tx
		remaining := maps.Clone(desired)
		current, err := reader.Lookup(ctx, tuples.Query{Scope: &scope, Relation: relation})
		if err != nil {
			return err
		}
		changes := tuples.Changes{}
		for _, f := range current {
			if _, keep := remaining[f]; keep {
				delete(remaining, f)
			} else {
				changes.Remove = append(changes.Remove, f)
			}
		}
		for f := range remaining {
			changes.Add = append(changes.Add, f)
		}
		return applyTupleChanges(ctx, tx, s.cfg, changes)
	})
}
func (s *tupleStore) DeleteScope(ctx context.Context, scope tuples.Scope) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	return runWrite(ctx, s.db, s.cfg, func(tx *writeTx) error {
		tx.touch(scope)
		a := &tupleArgs{}
		where, err := tupleWhere(a, tuples.Query{Scope: &scope})
		if err != nil {
			return err
		}
		_, err = tx.tuples(ctx, audit.ActionRemoved, "DELETE FROM "+s.table()+where, a.params()...)
		return err
	})
}

func (a *tupleArgs) named() jackpgx.NamedArgs {
	out := jackpgx.NamedArgs{}
	for i, v := range a.values {
		out[fmt.Sprintf("p%d", i+1)] = v
	}
	return out
}

func validateTupleCursor(req list.Request) error {
	_, err := tuplekey.DecodeCursor(req.Cursor)
	return err
}

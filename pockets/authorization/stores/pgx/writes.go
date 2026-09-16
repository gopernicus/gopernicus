package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// writeTx collects only rows the database actually changed. Its caller appends
// the audit records before releasing the same transaction or savepoint.
type writeTx struct {
	*pgxdb.Tx
	audit   bool
	changes []audit.Change
	scopes  map[tuples.Scope]struct{}
}

func (tx *writeTx) touch(scope tuples.Scope) {
	if tx.scopes != nil && scope.Kind == tuples.ResourceScope {
		tx.scopes[scope] = struct{}{}
	}
}

func (tx *writeTx) tuples(ctx context.Context, action audit.Action, query string, args ...any) (int64, error) {
	if !tx.audit {
		result, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected(), nil
	}
	rows, err := tx.Query(ctx, query+` RETURNING `+tupleColumns, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		var fact tuples.Tuple
		if err := rows.Scan(&fact.Scope.Kind, &fact.Scope.Type, &fact.Scope.ID, &fact.Relation, &fact.Subject.Type, &fact.Subject.ID, &fact.Subject.Relation); err != nil {
			return 0, pgxdb.MapError(err)
		}
		tx.changes = append(tx.changes, audit.Change{Action: action, Tuple: fact})
		n++
	}
	return n, pgxdb.MapError(rows.Err())
}
func applyTupleChanges(ctx context.Context, tx *writeTx, cfg config, changes tuples.Changes) error {
	table := cfg.schema.Table("iam_tuples")
	for _, fact := range changes.Remove {
		tx.touch(fact.Scope)
		args := &tupleArgs{}
		if _, err := tx.tuples(ctx, audit.ActionRemoved, `DELETE FROM `+table+` WHERE `+tuplePredicate(args, fact), args.params()...); err != nil {
			return err
		}
	}
	for _, fact := range changes.Add {
		tx.touch(fact.Scope)
		args := &tupleArgs{}
		if _, err := tx.tuples(ctx, audit.ActionAdded, `INSERT INTO `+table+` (`+tupleColumns+`) VALUES `+args.fact(fact)+` ON CONFLICT DO NOTHING`, args.params()...); err != nil {
			return err
		}
	}
	return nil
}

func runWrite(ctx context.Context, db *pgxdb.DB, cfg config, fn func(*writeTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.audit {
		if _, err := audit.SourceFromContext(ctx); err != nil {
			return err
		}
	}
	apply := func(tx *pgxdb.Tx) error {
		if err := lockAuthorization(ctx, tx, cfg.schema); err != nil {
			return err
		}
		w := &writeTx{Tx: tx, audit: cfg.audit}
		if len(cfg.integrity.Rules) > 0 {
			w.scopes = make(map[tuples.Scope]struct{})
		}
		if err := fn(w); err != nil {
			return err
		}
		if err := checkIntegrity(ctx, w, cfg); err != nil {
			return err
		}
		return appendAudit(ctx, w, cfg)
	}
	if tx, ok := pgxdb.TxFromContext(ctx); ok {
		return mapMutationError(withSavepoint(ctx, tx, func() error { return apply(tx) }))
	}
	return retryMutation(ctx, func() (error, bool) {
		return db.InTx(ctx, func(tx *pgxdb.Tx) error {
			if _, err := tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL READ COMMITTED`); err != nil {
				return err
			}
			return apply(tx)
		}), false
	})
}

// A failed operation rolls back its own facts and audit even if the host handles
// the error and commits unrelated work. Cleanup must survive request cancellation.
func withSavepoint(ctx context.Context, tx *pgxdb.Tx, fn func() error) (err error) {
	if _, err := tx.Exec(ctx, `SAVEPOINT authorization_write`); err != nil {
		return err
	}
	released := false
	defer func() {
		if released {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, rbErr := tx.Exec(cleanup, `ROLLBACK TO SAVEPOINT authorization_write`); rbErr != nil {
			abortErr := tx.Rollback()
			err = errors.Join(err, fmt.Errorf("authorization savepoint rollback: %w", rbErr), abortErr)
			return
		}
		if _, releaseErr := tx.Exec(cleanup, `RELEASE SAVEPOINT authorization_write`); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("authorization savepoint release: %w", releaseErr), tx.Rollback())
		}
	}()
	if err = fn(); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `RELEASE SAVEPOINT authorization_write`); err != nil {
		return err
	}
	released = true
	return nil
}

func lockAuthorization(ctx context.Context, tx *pgxdb.Tx, schema pgxdb.Schema) error {
	_, err := tx.Exec(ctx, `LOCK TABLE `+schema.Table("iam_tuples")+` IN SHARE ROW EXCLUSIVE MODE`)
	return mapMutationError(err)
}

// Validate post-state before publishing facts or audit. PostgreSQL row locks also
// reject anchors deleted since an ambient repeatable-read snapshot was fixed.
func checkIntegrity(ctx context.Context, tx *writeTx, cfg config) error {
	for scope := range tx.scopes {
		a := &tupleArgs{}
		where, err := tupleWhere(a, tuples.Query{Scope: &scope})
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT "+tupleColumns+" FROM "+cfg.schema.Table("iam_tuples")+where+" FOR UPDATE", a.params()...)
		if err != nil {
			return err
		}
		facts := []tuples.Tuple{}
		for rows.Next() {
			var fact tuples.Tuple
			if err := rows.Scan(&fact.Scope.Kind, &fact.Scope.Type, &fact.Scope.ID, &fact.Relation, &fact.Subject.Type, &fact.Subject.ID, &fact.Subject.Relation); err != nil {
				rows.Close()
				return err
			}
			facts = append(facts, fact)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if err := cfg.integrity.ValidateState(scope, facts); err != nil {
			return err
		}
	}
	return ctx.Err()
}

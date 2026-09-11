package turso

import (
	"context"
	"errors"
	"fmt"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
)

// writeTx collects only rows the database actually changed. Its caller appends
// the audit records before releasing the same transaction or savepoint.
type writeTx struct {
	*tursodb.Tx
	audit   bool
	changes []audit.Change
}

func (tx *writeTx) relationships(ctx context.Context, action audit.Action, query string, args ...any) (int64, error) {
	if !tx.audit {
		result, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}
	rows, err := tx.Query(ctx, query+` RETURNING resource_type, resource_id, relation, subject_type, subject_id, subject_relation`, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		var fact relationships.CreateRelationship
		if err := rows.Scan(&fact.ResourceType, &fact.ResourceID, &fact.Relation, &fact.SubjectType, &fact.SubjectID, &fact.SubjectRelation); err != nil {
			return 0, tursodb.MapError(err)
		}
		tx.changes = append(tx.changes, audit.Change{Action: action, Relationship: &fact})
		n++
	}
	return n, tursodb.MapError(rows.Err())
}

func (tx *writeTx) roles(ctx context.Context, action audit.Action, query string, args ...any) (int64, error) {
	if !tx.audit {
		result, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}
	rows, err := tx.Query(ctx, query+` RETURNING subject_type, subject_id, role, resource_type, resource_id`, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		var fact roles.Assignment
		if err := rows.Scan(&fact.SubjectType, &fact.SubjectID, &fact.Role, &fact.ResourceType, &fact.ResourceID); err != nil {
			return 0, tursodb.MapError(err)
		}
		tx.changes = append(tx.changes, audit.Change{Action: action, Role: &fact})
		n++
	}
	return n, tursodb.MapError(rows.Err())
}

func runWrite(ctx context.Context, db *tursodb.DB, cfg config, fn func(*writeTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.audit {
		if _, err := audit.SourceFromContext(ctx); err != nil {
			return err
		}
	}
	apply := func(tx *tursodb.Tx) error {
		w := &writeTx{Tx: tx, audit: cfg.audit}
		if err := fn(w); err != nil {
			return err
		}
		return appendAudit(ctx, w, cfg)
	}
	if tx, ok := tursodb.TxFromContext(ctx); ok {
		return withSavepoint(ctx, tx, func() error { return apply(tx) })
	}
	return ownedTransaction(ctx, db, apply)
}

// A failed operation rolls back its own facts and audit even if the host handles
// the error and commits unrelated work. Cleanup must survive request cancellation.
func withSavepoint(ctx context.Context, tx *tursodb.Tx, fn func() error) (err error) {
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

// Retry only transaction acquisition. Once the callback starts, an error from
// the remote server or COMMIT may have an unknown outcome and must not replay it.
func ownedTransaction(ctx context.Context, db *tursodb.DB, fn func(*tursodb.Tx) error) (err error) {
	var tx *tursodb.Tx
	if err := retryBusy(ctx, func() error {
		var beginErr error
		tx, beginErr = db.Begin(ctx)
		return beginErr
	}); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			if rbErr := tx.Rollback(); rbErr != nil {
				err = errors.Join(err, rbErr)
			}
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	err = tx.Commit()
	// Commit performs its own rollback/connection cleanup on failure.
	committed = true
	return err
}

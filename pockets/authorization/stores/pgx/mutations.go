package pgx

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	serializationFailure = "40001"
	deadlockDetected     = "40P01"
	mutationMaxRetries   = 20
	mutationBaseDelay    = 2 * time.Millisecond
	mutationMaxDelay     = 100 * time.Millisecond
)

func retryMutation(ctx context.Context, attempt func() (err error, terminal bool)) error {
	delay := mutationBaseDelay
	for retries := 0; ; retries++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err, terminal := attempt()
		if terminal || !mutationContention(err) || retries >= mutationMaxRetries {
			return mapMutationError(err)
		}
		timer := time.NewTimer(delay/2 + time.Duration(rand.Int64N(int64(delay-delay/2))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, mutationMaxDelay)
	}
}

func mutationContention(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == serializationFailure || pgErr.Code == deadlockDetected)
}

type mutationStore struct {
	db        *pgxdb.DB
	integrity mutations.IntegrityPolicy
	schema    pgxdb.Schema
	audit     bool
}

func newMutationStore(db *pgxdb.DB, cfg config) *mutationStore {
	return &mutationStore{db: db, integrity: cfg.integrity, schema: cfg.schema, audit: cfg.audit}
}

var _ mutations.MutationRepository = (*mutationStore)(nil)

func (m *mutationStore) IntegrityPolicy() mutations.IntegrityPolicy {
	return mutations.IntegrityPolicy{Rules: slices.Clone(m.integrity.Rules)}
}

// Integrity checks and changes share one write-serialized transaction. No caller operation
// token or durable result is retained; every call validates the current model.
func (m *mutationStore) Apply(ctx context.Context, cmd mutations.Command, validate mutations.SemanticValidator) (*mutations.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := pgxdb.TxFromContext(ctx); ok {
		return nil, mutations.ErrMutationInsideTransaction
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if m.audit {
		if _, err := audit.SourceFromContext(ctx); err != nil {
			return nil, err
		}
	}
	var result *mutations.Result
	validationFailed := false
	attempt := func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL READ COMMITTED`); err != nil {
			return err
		}
		if err := lockAuthorization(ctx, tx, m.schema); err != nil {
			return err
		}
		if validate != nil {
			if err := validate(cmd); err != nil {
				validationFailed = true
				return err
			}
		}
		w := &writeTx{Tx: tx, audit: m.audit}
		outcome, _, err := m.evaluate(ctx, w, cmd)
		if err != nil {
			return err
		}
		if err := outcome.Rejection(); err != nil {
			return err
		}
		if err := appendAudit(ctx, w, config{audit: m.audit, schema: m.schema}); err != nil {
			return err
		}
		result = &mutations.Result{Outcome: outcome}
		return nil
	}
	err := retryMutation(ctx, func() (error, bool) {
		result = nil
		validationFailed = false
		err := m.db.InTx(ctx, attempt)
		return err, validationFailed
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func mapMutationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, mutations.ErrConcurrentMutation) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case serializationFailure, deadlockDetected:
			return fmt.Errorf("%w: %w", mutations.ErrConcurrentMutation, err)
		}
	}
	return pgxdb.MapError(err)
}

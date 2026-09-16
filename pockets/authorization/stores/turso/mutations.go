package turso

import (
	"context"
	"slices"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
)

type mutationStore struct {
	db        *tursodb.DB
	integrity mutations.IntegrityPolicy
	audit     bool
}

func newMutationStore(db *tursodb.DB, cfg config) *mutationStore {
	return &mutationStore{db: db, integrity: cfg.integrity, audit: cfg.audit}
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
	if _, ok := tursodb.TxFromContext(ctx); ok {
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
	attempt := func(tx *tursodb.Tx) error {
		if validate != nil {
			if err := validate(cmd); err != nil {
				return err
			}
		}
		w := &writeTx{Tx: tx, audit: m.audit}
		outcome, _, err := m.evaluate(ctx, w, cmd)
		if err != nil {
			return err
		}
		if err := appendAudit(ctx, w, config{audit: m.audit}); err != nil {
			return err
		}
		result = &mutations.Result{Outcome: outcome}
		return nil
	}
	err := ownedTransaction(ctx, m.db, attempt)
	if err != nil {
		return nil, err
	}
	return result, nil
}

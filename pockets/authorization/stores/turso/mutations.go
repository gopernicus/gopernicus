package turso

import (
	"context"
	"slices"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

type mutationStore struct {
	db        *tursodb.DB
	integrity mutations.IntegrityPolicy
	audit     bool
	ids       sdk.IDGenerator
}

func newMutationStore(db *tursodb.DB, cfg config) *mutationStore {
	return &mutationStore{db: db, integrity: cfg.integrity, audit: cfg.audit, ids: cfg.ids}
}

var (
	_ mutations.MutationRepository = (*mutationStore)(nil)
	_ mutations.OperationLedger    = (*mutationStore)(nil)
	_ mutations.OperationExpirer   = (*mutationStore)(nil)
	_ audit.RecordingReporter      = (*mutationStore)(nil)
)

// AuditRecording reports whether the store was constructed WithAudit.
func (m *mutationStore) AuditRecording() bool { return m.audit }

func (m *mutationStore) IntegrityPolicy() mutations.IntegrityPolicy {
	return mutations.IntegrityPolicy{Rules: slices.Clone(m.integrity.Rules)}
}

// Integrity checks, tuple changes and operation records share one write-serialized transaction.
func (m *mutationStore) Apply(ctx context.Context, cmd mutations.Command, validate mutations.SemanticValidator) (*mutations.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := m.db.TxFrom(ctx); ok {
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
		w := &writeTx{Tx: tx, audit: m.audit}
		if cmd.OperationID != "" {
			replayed, err := m.replayOperation(ctx, w, cmd)
			if err != nil {
				return err
			}
			if replayed != nil {
				result = replayed
				return nil
			}
		}
		if validate != nil {
			if err := validate(cmd); err != nil {
				return err
			}
		}
		outcome, _, err := m.evaluate(ctx, w, cmd)
		if err != nil {
			return err
		}
		if err := appendAudit(ctx, w, config{audit: m.audit, ids: m.ids}); err != nil {
			return err
		}
		if cmd.OperationID != "" {
			if err := m.recordOperation(ctx, w, cmd, outcome); err != nil {
				return err
			}
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

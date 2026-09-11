package turso

import (
	"context"
	"fmt"
	"slices"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

type mutationStore struct {
	db       *tursodb.DB
	guardian mutations.GuardianPolicy
	audit    bool
}

func newMutationStore(db *tursodb.DB, cfg config) *mutationStore {
	return &mutationStore{db: db, guardian: cfg.guardian, audit: cfg.audit}
}

var _ mutations.MutationRepository = (*mutationStore)(nil)

func (m *mutationStore) GuardianPolicy() mutations.GuardianPolicy {
	return mutations.GuardianPolicy{Rules: slices.Clone(m.guardian.Rules)}
}

func (m *mutationStore) Apply(ctx context.Context, cmd mutations.Command, validate mutations.SemanticValidator) (*mutations.Result, error) {
	return m.apply(ctx, cmd, nil, validate)
}

func (m *mutationStore) ApplyGuarded(ctx context.Context, cmd mutations.Command, guard mutations.Guard, validate mutations.SemanticValidator) (*mutations.Result, error) {
	return m.apply(ctx, cmd, guard, validate)
}

// Guards and changes share one write-serialized transaction. No caller operation
// token or durable result is retained; every call validates the current model.
func (m *mutationStore) apply(ctx context.Context, cmd mutations.Command, guard mutations.Guard, validate mutations.SemanticValidator) (*mutations.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := tursodb.TxFromContext(ctx); ok {
		return nil, mutations.ErrGuardedInsideTransaction
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
		if guard != nil {
			if err := runGuard(ctx, guard, newDecisionView(tx)); err != nil {
				return err
			}
		}
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
		if err := outcome.Rejection(); err != nil {
			return err
		}
		remains, err := sameRoleGrantRemains(ctx, tx, cmd)
		if err != nil {
			return err
		}
		if err := appendAudit(ctx, w, config{audit: m.audit}); err != nil {
			return err
		}
		result = &mutations.Result{Outcome: outcome, SameRoleGrantRemains: remains}
		return nil
	}
	err := ownedTransaction(ctx, m.db, attempt)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func sameRoleGrantRemains(ctx context.Context, tx *tursodb.Tx, cmd mutations.Command) (bool, error) {
	if cmd.Operation != mutations.OpRoleUnassign || cmd.Target.Kind != mutations.TargetResource {
		return false, nil
	}
	for _, row := range cmd.Roles {
		ok, err := hasExactRoleTx(ctx, tx, row.SubjectType, row.SubjectID, row.Role, "", "")
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func runGuard(ctx context.Context, guard mutations.Guard, view *decisionView) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("authorization turso store: guard panicked: %v: %w", r, sdk.ErrUnavailable)
		}
	}()
	return guard(ctx, view)
}

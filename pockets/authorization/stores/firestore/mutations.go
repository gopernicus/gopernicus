package firestore

import (
	"context"
	"errors"
	"fmt"
	"slices"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ mutation.MutationRepository = (*mutationStore)(nil)

// A policy refusal must not trigger the vendor's status-based retry loop.
// Restore the original error after the transaction has rolled back.
var errCallbackRefused = errors.New("authorization firestore store: callback refused mutation")

// Every guard and state read runs inside the same native transaction. Firestore
// validates document and query reads at commit, including negative predicates.
// The complete write plan is evaluated before its first write.
type mutationStore struct {
	db       *firestoredb.DB
	guardian mutation.GuardianPolicy
	audit    bool
}

func newMutationStore(db *firestoredb.DB, guardian mutation.GuardianPolicy, enabled bool) *mutationStore {
	guardian.Rules = slices.Clone(guardian.Rules)
	return &mutationStore{db: db, guardian: guardian, audit: enabled}
}

func (s *mutationStore) GuardianPolicy() mutation.GuardianPolicy {
	return mutation.GuardianPolicy{Rules: slices.Clone(s.guardian.Rules)}
}

func (s *mutationStore) Apply(ctx context.Context, cmd mutation.Command, validate mutation.SemanticValidator) (*mutation.Result, error) {
	return s.apply(ctx, cmd, nil, validate)
}

func (s *mutationStore) ApplyGuarded(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Result, error) {
	return s.apply(ctx, cmd, guard, validate)
}

func (s *mutationStore) apply(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Result, error) {
	if err := refuseAmbientMutation(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if err := validateAuditSource(ctx, s.audit); err != nil {
		return nil, err
	}
	var out *mutation.Result
	err := retryContention(ctx, func() (error, bool) {
		// A begin failure and each vendor retry must discard a previous result.
		out = nil
		terminal := false
		var refusal error
		err := s.db.Transact(ctx, func(ctx context.Context) error {
			out, terminal, refusal = nil, false, nil
			result, refused, err := s.applyTx(ctx, cmd, guard, validate)
			terminal = refused
			if err != nil {
				if terminal {
					refusal = err
					return errCallbackRefused
				}
				return detachVendorRetry(err)
			}
			out = result
			return nil
		})
		if terminal {
			return refusal, true
		}
		return err, false
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// The bool marks a terminal callback refusal. Only an identified view read
// failure may retry a guard; semantic validators are pure policy callbacks.
func (s *mutationStore) applyTx(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	r := s.db.ReaderFrom(ctx)
	if guard != nil {
		if err := runGuard(ctx, guard, newDecisionView(s.db, r)); err != nil {
			return nil, !retryableGuardRead(err), err
		}
	}
	if validate != nil {
		if err := validate(cmd); err != nil {
			return nil, true, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	result, err := s.evaluate(ctx, r, cmd)
	if err != nil {
		return nil, false, err
	}
	if err := result.outcome.Rejection(); err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := result.writes.flush(ctx, s.db, s.db.WriterFrom(ctx), s.audit); err != nil {
		return nil, false, err
	}
	return &mutation.Result{Outcome: result.outcome, SameRoleGrantRemains: result.sameRoleGrantRemains}, false, nil
}

func runGuard(ctx context.Context, guard mutation.Guard, view *decisionView) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("authorization firestore store: guard panicked: %v: %w", r, sdk.ErrUnavailable)
		}
	}()
	return guard(ctx, view)
}

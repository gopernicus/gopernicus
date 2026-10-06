package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

func (m *mutationStore) evaluate(ctx context.Context, tx *writeTx, cmd mutations.Command) (mutations.Outcome, bool, error) {
	before, err := m.readFacts(ctx, tx, cmd)
	if err != nil {
		return "", false, err
	}
	cfg := config{audit: m.audit}
	delta, outcome, err := mutations.Plan(cmd, before, m.integrity)
	if err != nil {
		return "", false, err
	}
	if err := applyTupleChanges(ctx, tx, cfg, delta); err != nil {
		return "", false, err
	}
	return outcome, len(delta.Add)+len(delta.Remove) > 0, nil
}

func (m *mutationStore) readFacts(ctx context.Context, tx *writeTx, cmd mutations.Command) ([]tuples.Tuple, error) {
	cfg := config{audit: m.audit}
	reader := newTupleStore(m.db, cfg)
	reader.readQuerier = tx.Tx
	scope := cmd.Target.Scope()
	a := &tupleArgs{}
	where, err := tupleWhere(a, tuples.Query{Scope: &scope})
	if err != nil {
		return nil, err
	}
	if cmd.Target.Kind == mutations.TargetSubject {
		// A global subject target includes its concrete and userset references,
		// but never requires reading another subject's grants.
		where += " AND subject_type=" + a.ref(cmd.Target.Type) + " AND subject_id=" + a.ref(cmd.Target.ID)
	}
	before, err := reader.lookupWhere(ctx, where, a, 0)
	if err != nil {
		return nil, err
	}
	return before, nil
}

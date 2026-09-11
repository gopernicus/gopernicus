package turso

import (
	"context"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

type decisionView struct{ tx *tursodb.Tx }

var _ mutations.StoreDecisionView = (*decisionView)(nil)

func newDecisionView(tx *tursodb.Tx) *decisionView { return &decisionView{tx: tx} }
func (v *decisionView) CheckRelation(ctx context.Context, target mutations.Target, relation, subjectType, subjectID string) (bool, error) {
	return v.CheckRelationBounded(ctx, target, relation, subjectType, subjectID, 0)
}

func (v *decisionView) CheckRelationBounded(ctx context.Context, target mutations.Target, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return v.checkRelationWithReader(ctx, target, relation, subjectType, subjectID, maxExpansionStates, v.tx)
}

func (v *decisionView) checkRelationWithReader(ctx context.Context, target mutations.Target, relation, subjectType, subjectID string, maxExpansionStates int, reader tursodb.Querier) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := target.Validate(); err != nil {
		return false, err
	}
	var count int
	var allowed bool
	args := []any{subjectType, subjectID}
	cte, from := reachableCTE, "reachable"
	if maxExpansionStates > 0 {
		cte, from = boundedReachableCTE, "capped"
		args = append(args, maxExpansionStates+1)
	}
	args = append(args, target.Type, target.ID, relation)
	query := cte + `
SELECT (SELECT count(*) FROM ` + from + `),
 EXISTS (SELECT 1 FROM iam_relationships r JOIN ` + from + ` s
 ON r.subject_type=s.atype AND r.subject_id=s.aid AND r.subject_relation=s.arelation
 WHERE r.resource_type=? AND r.resource_id=? AND r.relation=?)`
	err := reader.QueryRow(ctx, query, args...).Scan(&count, &allowed)
	if err != nil {
		return false, tursodb.MapError(err)
	}
	if maxExpansionStates > 0 && count > maxExpansionStates {
		return false, relationships.ErrExpansionBudgetExceeded
	}
	return allowed, nil
}

func (v *decisionView) RelationTargets(ctx context.Context, target mutations.Target, relation string) ([]relationships.RelationTarget, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	return relationTargets(ctx, v.tx, target.Type, target.ID, relation)
}

func (v *decisionView) HasRole(ctx context.Context, target mutations.Target, role, subjectType, subjectID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := target.Validate(); err != nil {
		return false, err
	}
	if err := (mutations.RoleRow{SubjectType: subjectType, SubjectID: subjectID, Role: role}).Validate(); err != nil {
		return false, err
	}
	rt, rid := roleScope(target)
	ok, err := hasExactRoleTx(ctx, v.tx, subjectType, subjectID, role, rt, rid)
	if err != nil || ok || target.Kind == mutations.TargetSubject {
		return ok, err
	}
	return hasExactRoleTx(ctx, v.tx, subjectType, subjectID, role, "", "")
}

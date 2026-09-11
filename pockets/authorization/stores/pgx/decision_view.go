package pgx

import (
	"context"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/jackc/pgx/v5"
)

type decisionView struct {
	tx     *pgxdb.Tx
	schema pgxdb.Schema
}

var _ mutations.StoreDecisionView = (*decisionView)(nil)

func newDecisionView(tx *pgxdb.Tx, schema pgxdb.Schema) *decisionView {
	return &decisionView{tx: tx, schema: schema}
}

func (v *decisionView) CheckRelation(ctx context.Context, target mutations.Target, relation, subjectType, subjectID string) (bool, error) {
	return v.CheckRelationBounded(ctx, target, relation, subjectType, subjectID, 0)
}

func (v *decisionView) CheckRelationBounded(ctx context.Context, target mutations.Target, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return v.checkRelationWithReader(ctx, target, relation, subjectType, subjectID, maxExpansionStates, v.tx)
}

func (v *decisionView) checkRelationWithReader(ctx context.Context, target mutations.Target, relation, subjectType, subjectID string, maxExpansionStates int, reader pgxdb.Querier) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := target.Validate(); err != nil {
		return false, err
	}
	var count int
	var allowed bool
	args := pgx.NamedArgs{"subject_type": subjectType, "subject_id": subjectID, "resource_type": target.Type, "resource_id": target.ID, "relation": relation}
	cte, from := reachableCTE(v.schema), "reachable"
	if maxExpansionStates > 0 {
		cte, from = boundedReachableCTE(v.schema), "capped"
		args["state_cap"] = maxExpansionStates + 1
	}
	query := cte + `
SELECT (SELECT count(*) FROM ` + from + `),
 EXISTS (SELECT 1 FROM ` + v.schema.Table("iam_relationships") + ` r JOIN ` + from + ` s
 ON r.subject_type=s.atype AND r.subject_id=s.aid AND r.subject_relation=s.arelation
 WHERE r.resource_type=@resource_type AND r.resource_id=@resource_id AND r.relation=@relation)`
	err := reader.QueryRow(ctx, query, args).Scan(&count, &allowed)
	if err != nil {
		return false, pgxdb.MapError(err)
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
	return relationTargets(ctx, v.tx, v.schema, target.Type, target.ID, relation)
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
	ok, err := hasExactRole(ctx, v.tx, v.schema, subjectType, subjectID, role, rt, rid)
	if err != nil || ok || target.Kind == mutations.TargetSubject {
		return ok, err
	}
	return hasExactRole(ctx, v.tx, v.schema, subjectType, subjectID, role, "", "")
}

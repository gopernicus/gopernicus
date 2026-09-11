package mutations

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// StoreDecisionView reads authorization state within the command's serialized
// transaction or memory critical section. All reads, including absent matches
// and userset expansion, belong to that boundary. No revision counters are used.
type StoreDecisionView interface {
	ForModel(relationships.ReadModel) relationships.PermissionReader
	CheckRelation(ctx context.Context, target Target, relation, subjectType, subjectID string) (bool, error)
	CheckRelationBounded(ctx context.Context, target Target, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error)
	RelationTargets(ctx context.Context, target Target, relation string) ([]relationships.RelationTarget, error)
	HasRole(ctx context.Context, target Target, role, subjectType, subjectID string) (bool, error)
}

// Guard runs synchronously inside the write boundary. It may read authorization
// data only through view, must honor cancellation, and must not perform network
// or unrelated-store I/O. Nil means the trusted path, not an implicit HTTP allow.
type Guard func(ctx context.Context, view StoreDecisionView) error

// SemanticValidator is a pure current-model validation performed on every
// command application. Nil adds no model checks to the structural validation.
type SemanticValidator func(cmd Command) error

// MutationRepository applies one command atomically. The store validates shape,
// runs guard and current semantic validation, then checks guardian invariants
// and changes all requested facts or none. Successful results are nonnil;
// refused/failed commands return nil results. No-op results are state based:
// repeating a grant after an intervening revoke grants it again.
//
// Guard reads and writes are serialized with concurrent authorization writers,
// including negative membership predicates. Enabled audit records actual added
// and removed facts in the same commit; attribution is domain/audit.Source from
// context. No-op/error paths record nothing. Guarded/trusted commands refuse an
// ambient host transaction; baseline writers retain their joining contract.
// Only definite aborted contention may retry, never ambiguous commit failures.
type MutationRepository interface {
	GuardianPolicy() GuardianPolicy
	Apply(ctx context.Context, cmd Command, validate SemanticValidator) (*Result, error)
	ApplyGuarded(ctx context.Context, cmd Command, guard Guard, validate SemanticValidator) (*Result, error)
}

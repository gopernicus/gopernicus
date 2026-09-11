package relationships

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
)

// SubjectRule permits one tuple shape. IDs are intentionally absent: the host
// model declares vocabulary; stored tuples identify the actual participants.
type SubjectRule struct {
	ResourceType    string `json:"resource_type"`
	Relation        string `json:"relation"`
	SubjectType     string `json:"subject_type"`
	SubjectRelation string `json:"subject_relation"`
}

// ReadModel is an immutable snapshot of the tuple shapes that may contribute
// authority. Its zero value denies every shape. Old tuples remain inspectable
// through raw store methods, but do not grant permissions under a narrower model.
type ReadModel struct {
	allowed map[SubjectRule]struct{}
	encoded string
}

// NewReadModel snapshots tuple shapes from a validated host model. It owns its
// input and removes duplicates; subsequent caller mutations cannot change reads.
func NewReadModel(rules []SubjectRule) ReadModel {
	rules = slices.Clone(rules)
	slices.SortFunc(rules, func(a, b SubjectRule) int {
		if c := cmp.Compare(a.ResourceType, b.ResourceType); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Relation, b.Relation); c != 0 {
			return c
		}
		if c := cmp.Compare(a.SubjectType, b.SubjectType); c != 0 {
			return c
		}
		return cmp.Compare(a.SubjectRelation, b.SubjectRelation)
	})
	rules = slices.Compact(rules)
	allowed := make(map[SubjectRule]struct{}, len(rules))
	for _, rule := range rules {
		allowed[rule] = struct{}{}
	}
	encoded, _ := json.Marshal(rules)
	return ReadModel{allowed: allowed, encoded: string(encoded)}
}

func (m ReadModel) Allows(resourceType, relation, subjectType, subjectRelation string) bool {
	_, ok := m.allowed[SubjectRule{resourceType, relation, subjectType, subjectRelation}]
	return ok
}

// JSON supplies the immutable allowlist to parameterized adapter queries.
func (m ReadModel) JSON() string {
	if len(m.allowed) == 0 {
		return "[]"
	}
	return m.encoded
}

func (m ReadModel) FilterTargets(resourceType, relation string, targets []RelationTarget) []RelationTarget {
	out := make([]RelationTarget, 0, len(targets))
	for _, target := range targets {
		if m.Allows(resourceType, relation, target.Type, target.Relation) {
			out = append(out, target)
		}
	}
	return out
}

// PermissionReader is the model-scoped per-resource read contract. Every
// expansion edge and the final matching tuple must satisfy the same ReadModel.
// Implementations preserve their transaction and dependency tracking when scoped.
type PermissionReader interface {
	CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error)
	GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error)
}

// Reader is the model-scoped permission and enumeration surface. Raw Storer
// methods retain their fact-inspection semantics; permission engines must use
// ForModel and never substitute those raw methods if a scoped reader is missing.
type Reader interface {
	PermissionReader
	CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error)
	LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error)
	LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error)
	LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error)
}

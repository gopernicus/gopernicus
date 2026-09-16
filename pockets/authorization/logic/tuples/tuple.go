// Package tuples defines the shared values and structural validation for
// authorization facts. It does not apply policy or expand usersets.
package tuples

import "cmp"

// Tuple is an authorization fact. Its comparable value is its full identity:
// scope, relation and exact subject, including any userset relation. Independent
// relations for the same scope and subject are distinct facts.
type Tuple struct {
	Scope    Scope      `json:"scope"`
	Relation string     `json:"relation"`
	Subject  SubjectRef `json:"subject"`
}

// Compare orders full tuple identities by scope kind, resource type, resource
// ID, relation, subject type, subject ID and subject relation. Strings compare
// in byte order. Callers validate tuples separately; Compare performs no I/O.
func Compare(a, b Tuple) int {
	return cmp.Or(
		cmp.Compare(a.Scope.Kind, b.Scope.Kind),
		cmp.Compare(a.Scope.Type, b.Scope.Type),
		cmp.Compare(a.Scope.ID, b.Scope.ID),
		cmp.Compare(a.Relation, b.Relation),
		cmp.Compare(a.Subject.Type, b.Subject.Type),
		cmp.Compare(a.Subject.ID, b.Subject.ID),
		cmp.Compare(a.Subject.Relation, b.Subject.Relation),
	)
}

// Validate checks every component without applying a permission model.
// Global and resource scopes both permit concrete subjects and usersets.
func (t Tuple) Validate() error {
	if err := t.Scope.Validate(); err != nil {
		return err
	}
	if err := ValidateRefField("relation", t.Relation); err != nil {
		return err
	}
	return t.Subject.Validate()
}

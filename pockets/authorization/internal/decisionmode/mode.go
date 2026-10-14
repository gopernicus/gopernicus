// Package decisionmode carries the private evaluation switch that equivalence
// tests use to compare the direct-hit short circuit against pure subject-first
// expansion. It is internal so the switch never becomes public API.
package decisionmode

import "context"

type subjectFirstKey struct{}

// WithSubjectFirst returns a context whose decisions skip the direct-hit short
// circuit and answer every Direct node by subject-first expansion alone.
func WithSubjectFirst(ctx context.Context) context.Context {
	return context.WithValue(ctx, subjectFirstKey{}, true)
}

// SubjectFirst reports whether ctx disables the direct-hit short circuit.
func SubjectFirst(ctx context.Context) bool {
	on, _ := ctx.Value(subjectFirstKey{}).(bool)
	return on
}

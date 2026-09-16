package decisions

import (
	"context"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// DiagnosticCode is a low-cardinality observation, never an authorization result.
type DiagnosticCode string

const DiagnosticGlobalGrantNotApplied DiagnosticCode = "global_grant_not_applied"

// DiagnosticObserver receives at most one event after an operation's snapshot
// completes successfully. Events contain no principal, resource or role labels.
// Observers must return promptly and support calls from concurrent operations.
type DiagnosticObserver func(context.Context, DiagnosticCode)

// WithDiagnosticObserver opts into transition diagnostics for scoped denials.
// The disabled default performs no extra reads. An enabled operation probes at
// most eight exact global facts in its existing snapshot; probe errors are
// ignored and never grant access. Caller-owned bound views do not emit events.
func WithDiagnosticObserver(observer DiagnosticObserver) Option {
	return func(c *config) { c.diagnostic = observer }
}

const maxDiagnosticProbes = 8

type operationDiagnostics struct {
	probes      int
	globalGrant bool
}

func (s *Service) rememberScopedDenial(b *budget, scoped tuples.Tuple) {
	if s.diagnostics == nil || scoped.Scope.Kind != tuples.ResourceScope || len(b.diagnosticFacts) >= maxDiagnosticProbes {
		return
	}
	global := scoped
	global.Scope = tuples.Global()
	for _, prior := range b.diagnosticFacts {
		if prior == global {
			return
		}
	}
	b.diagnosticFacts = append(b.diagnosticFacts, global)
}

func (s *Service) observeDenied(ctx context.Context, b *budget, result authmodel.CheckResult, evaluationErr error) {
	state := s.diagnostics
	if state == nil || result.Allowed || evaluationErr != nil || state.globalGrant || ctx.Err() != nil {
		return
	}
	for _, fact := range b.diagnosticFacts {
		if state.probes >= maxDiagnosticProbes || ctx.Err() != nil {
			return
		}
		state.probes++
		held, err := s.facts.Contains(ctx, fact)
		if err == nil && held && ctx.Err() == nil {
			state.globalGrant = true
			return
		}
	}
}

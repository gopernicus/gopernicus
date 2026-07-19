package authorization

import (
	"context"

	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/authorizersvc"
)

// RelationshipWriter is the trusted application-side capability for ordinary
// ReBAC state. It validates additive state against the compiled schema and writes
// directly to the relationship repository. It has no MutationID, receipt,
// revision, guard, guardian, replay ledger, or audit dependency.
//
// The composition root receives it separately in Components and decides which
// application services may hold it. Register exposes no authorization routes,
// and Service has no method that returns this writer. Trusted application-side
// therefore describes capability placement, not public or unauthenticated access.
//
// Use this writer for topology projections, synchronization, bootstrap, fixtures,
// migrations, and collaboration writes whose threat model accepts detached host
// authorization checks and ordinary state-write races. Use Service's guarded
// mutation methods for operations that need atomic authorization, dependency
// revision checks, guardian invariants, receipts, durable idempotency, or audit.
type RelationshipWriter struct {
	relationships *authorizersvc.Service
}

// CreateRelationships validates every tuple against the compiled schema and
// creates it idempotently. A tuple already present is a no-op. This preserves the
// original batch-create capability for straightforward additive state writes.
func (w *RelationshipWriter) CreateRelationships(ctx context.Context, relationships []CreateRelationship) error {
	if w == nil || w.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return w.relationships.CreateRelationships(ctx, relationships)
}

// DeleteRelationship removes one exact tuple. Absence is naturally idempotent.
// Deletion validates reference shape but intentionally does not require the
// relation to remain in the current schema, so stale tuples can be cleaned up.
func (w *RelationshipWriter) DeleteRelationship(ctx context.Context, resource Resource, relation string, target SubjectRef) error {
	if w == nil || w.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return w.relationships.DeleteRelationshipTarget(ctx, resource, relation, target)
}

// DeleteResourceRelationships removes every tuple for a resource. Absence is
// naturally idempotent. This is ordinary desired-state cleanup; a host that must
// treat bypassing guardian invariants as an audited security event should instead
// use SystemMutator.TeardownAuthorizationScope.
func (w *RelationshipWriter) DeleteResourceRelationships(ctx context.Context, resource Resource) error {
	if w == nil || w.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return w.relationships.DeleteResourceRelationships(ctx, resource.Type, resource.ID)
}

// SetRelationTargets atomically makes the stored targets for one
// resource+relation exactly equal targets. Targets are schema-validated and
// de-duplicated. Existing matching rows are retained, missing rows are added,
// and surplus rows are removed in one store operation.
//
// The operation is state based rather than occurrence based: repeating A is a
// no-op, and A -> B -> A restores A without any replay identity. An empty target
// set clears the relation idempotently.
func (w *RelationshipWriter) SetRelationTargets(ctx context.Context, resource Resource, relation string, targets []SubjectRef) error {
	if w == nil || w.relationships == nil {
		return ErrRelationshipsNotConfigured
	}
	return w.relationships.SetRelationTargets(ctx, resource, relation, targets)
}

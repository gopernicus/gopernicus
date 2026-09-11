package mutations

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// Guarded relationship writes authorize and apply one command atomically.
// Successful calls return applied, no_change or not_found; refusals return errors.

// GrantRelationshipCommand grants a subject a single relation on a resource. Under
// the one-relation rule a different relation for a subject already related to the
// resource is a semantic_conflict (use ReplaceRelationship); an exact-duplicate grant
// is a no_change result.
type GrantRelationshipCommand struct {
	ResourceType string
	ResourceID   string
	Relation     string
	Subject      relationships.SubjectRef
}

// RevokeRelationshipCommand removes a subject's exact relation on a resource.
// Revoking an absent tuple is a committed not_found no-op, not an error.
type RevokeRelationshipCommand struct {
	ResourceType string
	ResourceID   string
	Relation     string
	Subject      relationships.SubjectRef
}

// ReplaceRelationshipCommand atomically sets the subject's relation on the resource
// to Relation, whatever it currently holds — the sanctioned answer to a one-relation
// conflict, with no delete/create visibility gap.
type ReplaceRelationshipCommand struct {
	ResourceType string
	ResourceID   string
	Relation     string
	Subject      relationships.SubjectRef
}

// PurgeResourceAuthorizationCommand removes every relationship on a resource. It is
// bulk removal, not resource teardown: it still honors guardian invariants (a purge
// that would orphan a protected resource is invariant_blocked) and its affected rows
// are bounded by the resolved EvaluationLimits.MaxBatchSize (a purge exceeding the
// bound is invariant_blocked). Zeroing a protected scope for resource deletion is the
// trusted SystemMutator teardown operation, not this command.
type PurgeResourceAuthorizationCommand struct {
	ResourceType string
	ResourceID   string
}

// GrantRelationship runs a guarded grant on behalf of actor.
func (s *Service) GrantRelationship(ctx context.Context, actor Actor, cmd GrantRelationshipCommand) (*Result, error) {
	if s.relationships == nil {
		return nil, relationships.ErrRelationshipsNotConfigured
	}
	return s.applyMutation(ctx, actor, grantRelationshipCommand(cmd))
}

// RevokeRelationship runs a guarded revoke on behalf of actor.
func (s *Service) RevokeRelationship(ctx context.Context, actor Actor, cmd RevokeRelationshipCommand) (*Result, error) {
	if s.relationships == nil {
		return nil, relationships.ErrRelationshipsNotConfigured
	}
	return s.applyMutation(ctx, actor, revokeRelationshipCommand(cmd))
}

// ReplaceRelationship runs a guarded atomic replace on behalf of actor.
func (s *Service) ReplaceRelationship(ctx context.Context, actor Actor, cmd ReplaceRelationshipCommand) (*Result, error) {
	if s.relationships == nil {
		return nil, relationships.ErrRelationshipsNotConfigured
	}
	return s.applyMutation(ctx, actor, Command{

		Target: resourceTarget(cmd.ResourceType, cmd.ResourceID),

		Operation:     OpReplace,
		Relationships: []RelationshipRow{{Relation: cmd.Relation, Subject: cmd.Subject}},
	})
}

// PurgeResourceAuthorization runs a guarded bulk purge on behalf of actor. The guard
// distinguishes it from a single grant by MutationAttempt.Operation (OpPurge), so a
// host can require elevated authority for bulk removal; the affected rows are bounded
// by the resolved EvaluationLimits.MaxBatchSize.
func (s *Service) PurgeResourceAuthorization(ctx context.Context, actor Actor, cmd PurgeResourceAuthorizationCommand) (*Result, error) {
	if s.relationships == nil {
		return nil, relationships.ErrRelationshipsNotConfigured
	}
	return s.applyMutation(ctx, actor, Command{

		Target: resourceTarget(cmd.ResourceType, cmd.ResourceID),

		Operation:       OpPurge,
		MaxAffectedRows: s.maxBatchSize,
	})
}

// grantRelationshipCommand builds the actor-independent OpGrant command a single
// relationship grant applies. Shared by the guarded Service.GrantRelationship and the
// trusted SystemMutator.GrantRelationship so both build an identical command.
func grantRelationshipCommand(cmd GrantRelationshipCommand) Command {
	return Command{

		Target: resourceTarget(cmd.ResourceType, cmd.ResourceID),

		Operation:     OpGrant,
		Relationships: []RelationshipRow{{Relation: cmd.Relation, Subject: cmd.Subject}},
	}
}

// revokeRelationshipCommand builds the actor-independent OpRevoke command a
// single relationship revoke applies. Shared by the guarded
// Service.RevokeRelationship and the trusted SystemMutator.RevokeRelationship so
// both build an identical command (the grant pair's symmetry).
func revokeRelationshipCommand(cmd RevokeRelationshipCommand) Command {
	return Command{

		Target: resourceTarget(cmd.ResourceType, cmd.ResourceID),

		Operation:     OpRevoke,
		Relationships: []RelationshipRow{{Relation: cmd.Relation, Subject: cmd.Subject}},
	}
}

// resourceTarget builds the resource-kind mutation scope a relationship command
// mutates.
func resourceTarget(resourceType, resourceID string) Target {
	return Target{Kind: TargetResource, Type: resourceType, ID: resourceID}
}

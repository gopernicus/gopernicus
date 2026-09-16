package mutations

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// GrantRelationshipCommand adds one independent relation. Exact duplicates are no-ops.
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

// PurgeResourceAuthorizationCommand removes every relationship on a resource. It is
// bulk removal, not resource teardown: it still honors IntegrityPolicy (a purge
// that would orphan a protected resource is invariant_blocked) and its affected rows
// are bounded by the resolved EvaluationLimits.MaxBatchSize (a purge exceeding the
// bound is invariant_blocked). Zeroing a protected scope for resource deletion is the
// explicit Service teardown operation, not this command.
type PurgeResourceAuthorizationCommand struct {
	ResourceType string
	ResourceID   string
}

// GrantRelationship applies one grant with shape and integrity validation.
func (s *Service) GrantRelationship(ctx context.Context, cmd GrantRelationshipCommand) (*Result, error) {
	return s.Apply(ctx, grantRelationshipCommand(cmd))
}

// RevokeRelationship removes one exact fact with integrity validation.
func (s *Service) RevokeRelationship(ctx context.Context, cmd RevokeRelationshipCommand) (*Result, error) {
	return s.Apply(ctx, revokeRelationshipCommand(cmd))
}

// PurgeResourceAuthorization removes a scope within MaxBatchSize while retaining
// IntegrityPolicy. Teardown uses the separate explicit reason-bearing method.
func (s *Service) PurgeResourceAuthorization(ctx context.Context, cmd PurgeResourceAuthorizationCommand) (*Result, error) {
	return s.Apply(ctx, Command{
		Target:    resourceTarget(cmd.ResourceType, cmd.ResourceID),
		Operation: OpPurge,
	})
}

func grantRelationshipCommand(cmd GrantRelationshipCommand) Command {
	return Command{
		Target:        resourceTarget(cmd.ResourceType, cmd.ResourceID),
		Operation:     OpGrant,
		Relationships: []RelationshipRow{{Relation: cmd.Relation, Subject: cmd.Subject}},
	}
}

func revokeRelationshipCommand(cmd RevokeRelationshipCommand) Command {
	return Command{
		Target:        resourceTarget(cmd.ResourceType, cmd.ResourceID),
		Operation:     OpRevoke,
		Relationships: []RelationshipRow{{Relation: cmd.Relation, Subject: cmd.Subject}},
	}
}

// resourceTarget builds the resource-kind mutation scope a relationship command
// mutates.
func resourceTarget(resourceType, resourceID string) Target {
	return Target{Kind: TargetResource, Type: resourceType, ID: resourceID}
}

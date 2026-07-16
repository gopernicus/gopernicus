package authorization

import "context"

// The actor-facing, policy-guarded relationship mutation lifecycle (AZ3-3.1). Each
// typed command builds one atomic mutation.Command and drives it through the guarded
// applyMutation seam — the host MutationGuard is evaluated INSIDE the repository's
// dependency-tracking ApplyGuarded boundary, so a denial never reaches Apply and no
// detached Check→Apply exists. None of these delegate to the baseline
// RelationshipWriter; the two paths have intentionally different semantics.
//
// Each returns the mutation *Receipt: its Outcome is the explicit domain result
// (applied, no_change, semantic_conflict, invariant_blocked, not_found — the
// one-relation conflict surfaces as semantic_conflict, never a silent overwrite),
// and Replayed is the independent replay flag. A persisted outcome
// (Receipt.Outcome.Persisted) is durable and idempotent by MutationID; a command
// error (denial, stale revision, payload mismatch, malformed command, or the
// read-only/unwired posture) returns (nil, err) and commits nothing.

// GrantRelationshipCommand grants a subject a single relation on a resource. Under
// the one-relation rule a different relation for a subject already related to the
// resource is a semantic_conflict (use ReplaceRelationship); an exact-duplicate grant
// is an idempotent no_change replay.
type GrantRelationshipCommand struct {
	MutationID       MutationID
	ResourceType     string
	ResourceID       string
	Relation         string
	Subject          SubjectRef
	ExpectedRevision *Revision
}

// RevokeRelationshipCommand removes a subject's exact relation on a resource.
// Revoking an absent tuple is a committed not_found no-op, not an error.
type RevokeRelationshipCommand struct {
	MutationID       MutationID
	ResourceType     string
	ResourceID       string
	Relation         string
	Subject          SubjectRef
	ExpectedRevision *Revision
}

// ReplaceRelationshipCommand atomically sets the subject's relation on the resource
// to Relation, whatever it currently holds — the sanctioned answer to a one-relation
// conflict, with no delete/create visibility gap.
type ReplaceRelationshipCommand struct {
	MutationID       MutationID
	ResourceType     string
	ResourceID       string
	Relation         string
	Subject          SubjectRef
	ExpectedRevision *Revision
}

// PurgeResourceAuthorizationCommand removes every relationship on a resource. It is
// bulk removal, not resource teardown: it still honors guardian invariants (a purge
// that would orphan a protected resource is invariant_blocked) and its affected rows
// are bounded by the resolved EvaluationLimits.MaxBatchSize (a purge exceeding the
// bound is invariant_blocked). Zeroing a protected scope for resource deletion is the
// trusted SystemMutator teardown operation, not this command.
type PurgeResourceAuthorizationCommand struct {
	MutationID       MutationID
	ResourceType     string
	ResourceID       string
	ExpectedRevision *Revision
}

// GrantRelationship runs a guarded grant on behalf of actor.
func (s *Service) GrantRelationship(ctx context.Context, actor Actor, cmd GrantRelationshipCommand) (*Receipt, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.applyMutation(ctx, actor, grantRelationshipCommand(cmd))
}

// RevokeRelationship runs a guarded revoke on behalf of actor.
func (s *Service) RevokeRelationship(ctx context.Context, actor Actor, cmd RevokeRelationshipCommand) (*Receipt, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.applyMutation(ctx, actor, Command{
		MutationID:       cmd.MutationID,
		Scope:            resourceScope(cmd.ResourceType, cmd.ResourceID),
		ExpectedRevision: cmd.ExpectedRevision,
		Operation:        OpRevoke,
		Relationships:    []RelationshipRow{{Relation: cmd.Relation, Subject: cmd.Subject}},
	})
}

// ReplaceRelationship runs a guarded atomic replace on behalf of actor.
func (s *Service) ReplaceRelationship(ctx context.Context, actor Actor, cmd ReplaceRelationshipCommand) (*Receipt, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.applyMutation(ctx, actor, Command{
		MutationID:       cmd.MutationID,
		Scope:            resourceScope(cmd.ResourceType, cmd.ResourceID),
		ExpectedRevision: cmd.ExpectedRevision,
		Operation:        OpReplace,
		Relationships:    []RelationshipRow{{Relation: cmd.Relation, Subject: cmd.Subject}},
	})
}

// PurgeResourceAuthorization runs a guarded bulk purge on behalf of actor. The guard
// distinguishes it from a single grant by MutationAttempt.Operation (OpPurge), so a
// host can require elevated authority for bulk removal; the affected rows are bounded
// by the resolved EvaluationLimits.MaxBatchSize.
func (s *Service) PurgeResourceAuthorization(ctx context.Context, actor Actor, cmd PurgeResourceAuthorizationCommand) (*Receipt, error) {
	if s.relationships == nil {
		return nil, ErrRelationshipsNotConfigured
	}
	return s.applyMutation(ctx, actor, Command{
		MutationID:       cmd.MutationID,
		Scope:            resourceScope(cmd.ResourceType, cmd.ResourceID),
		ExpectedRevision: cmd.ExpectedRevision,
		Operation:        OpPurge,
		MaxAffectedRows:  s.maxBatchSize,
	})
}

// grantRelationshipCommand builds the actor-independent OpGrant command a single
// relationship grant applies. Shared by the guarded Service.GrantRelationship and the
// trusted SystemMutator.GrantRelationship so both build an identical command.
func grantRelationshipCommand(cmd GrantRelationshipCommand) Command {
	return Command{
		MutationID:       cmd.MutationID,
		Scope:            resourceScope(cmd.ResourceType, cmd.ResourceID),
		ExpectedRevision: cmd.ExpectedRevision,
		Operation:        OpGrant,
		Relationships:    []RelationshipRow{{Relation: cmd.Relation, Subject: cmd.Subject}},
	}
}

// resourceScope builds the resource-kind mutation scope a relationship command
// mutates.
func resourceScope(resourceType, resourceID string) ScopeKey {
	return ScopeKey{Kind: ScopeResource, Type: resourceType, ID: resourceID}
}

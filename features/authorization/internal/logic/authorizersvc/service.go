package authorizersvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

var (
	// ErrInvalidRelation indicates a relationship is not allowed by the schema.
	ErrInvalidRelation = fmt.Errorf("authorization relation: %w", sdk.ErrInvalidInput)

	// ErrInvalidSchema indicates the schema has structural errors (undefined
	// references, circular through-relations, etc.).
	ErrInvalidSchema = fmt.Errorf("authorization schema: %w", sdk.ErrInvalidInput)
)

// Config holds the relationship engine's policy settings. Both fields are
// relationship-kind-scoped.
type Config struct {
	// Limits is the resolved semantic work budget for one decision or
	// enumeration. Each zero field resolves to its Default* at NewService; a
	// negative field is rejected as ErrInvalidLimits. Every dimension is charged
	// (AZ3-1.3): Through depth, distinct graph states, per-hop fan-out, batch size,
	// and lookup results. The CHECK-path group expansion stays engine-unbounded but
	// cycle-safe (a single bool has nothing to truncate); the LOOKUP result cap is
	// the one dimension threaded into a store (MaxLookupResults+1 fetch, a
	// distinguishable overflow signal), superseding the 2026-07-08 "never threaded"
	// note for enumeration.
	Limits EvaluationLimits
	// IDs mints each tuple's relationship_id at CreateRelationships (Q6). The
	// zero value is the nanoid default; a cryptids.Database generator yields ""
	// so the store omits the id column and the DDL DEFAULT fills it.
	IDs cryptids.IDGenerator
}

// Service is the sealed ReBAC evaluation engine. It evaluates permission checks
// and manages relationship tuples against a relationship.Storer and an immutable
// CompiledSchema. It retains NO caller schema data: Compile deep-copies the
// source at construction, so a later mutation of the caller's Schema cannot alter
// any decision.
type Service struct {
	store    relationship.Storer
	compiled *CompiledSchema
	ids      cryptids.IDGenerator
	limits   EvaluationLimits
}

// NewService compiles the schema (the sole boot gate) and validates the limits,
// then builds the engine over the immutable compiled artifact. An invalid schema
// is rejected loudly (wrapping ErrInvalidSchema); a negative limit is
// ErrInvalidLimits; zero limits resolve to the safe defaults. The caller's Schema
// is never retained — the engine reads only the deep-copied compilation.
func NewService(store relationship.Storer, schema Schema, cfg Config) (*Service, error) {
	compiled, err := Compile(schema)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
	}
	limits, err := cfg.Limits.Resolve()
	if err != nil {
		return nil, err
	}
	return &Service{
		store:    store,
		compiled: compiled,
		ids:      cfg.IDs,
		limits:   limits,
	}, nil
}

// =============================================================================
// Permission checks
// =============================================================================

// Check evaluates a permission check against the schema rules ONLY (direct
// relations + through-traversal with cycle detection). Policy short-circuits
// (platform-admin, self-access) are HOST composition, not engine behavior: a
// host runs them in its own Check closure before delegating here. This keeps
// the engine a pure schema evaluator and fails closed — a host that omits a
// bypass recipe simply gets no bypass.
func (s *Service) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	return s.check(ctx, req, newBudget(s.limits))
}

// check is the single evaluation funnel shared by Check and CheckExplain. The
// only difference between the two entry points is whether the budget carries a
// trace collector — there is deliberately no second evaluator, so an explain
// cannot reach a different decision or spend more work than the plain Check.
func (s *Service) check(ctx context.Context, req CheckRequest, b *budget) (CheckResult, error) {
	if err := req.Validate(); err != nil {
		return CheckResult{}, err
	}
	checks := s.compiled.permissionChecks(req.Resource.Type, req.Permission)
	if len(checks) == 0 {
		return CheckResult{Allowed: false, ReasonCode: ReasonDenied, Reason: "no rules defined"}, nil
	}
	return s.checkPermission(ctx, req, checks, 0, b, make(map[stateKey]bool))
}

// CheckBatch evaluates multiple checks. It uses an optimized batch query when
// all requests share subject, permission, and resource type with no
// through-relations; otherwise it falls back to sequential checks.
func (s *Service) CheckBatch(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	// Reject an over-size batch BEFORE any store call: an oversized request is
	// indeterminate work, not a decision. This is the MaxBatchSize dimension.
	if len(reqs) > s.limits.MaxBatchSize {
		return nil, ErrEvaluationLimit
	}

	for i := range reqs {
		if err := reqs[i].Validate(); err != nil {
			return nil, err
		}
	}

	canBatch := true
	first := reqs[0]
	for i := 1; i < len(reqs); i++ {
		if reqs[i].Principal != first.Principal ||
			reqs[i].Permission != first.Permission ||
			reqs[i].Resource.Type != first.Resource.Type {
			canBatch = false
			break
		}
	}

	if !canBatch {
		return s.checkBatchSequential(ctx, reqs)
	}

	checks := s.compiled.permissionChecks(first.Resource.Type, first.Permission)
	for _, check := range checks {
		if check.Through != "" {
			return s.checkBatchSequential(ctx, reqs)
		}
	}

	return s.checkBatchOptimized(ctx, reqs)
}

// FilterAuthorized returns only the resource IDs the subject can access, via
// CheckBatch.
func (s *Service) FilterAuthorized(ctx context.Context, principal PrincipalRef, permission, resourceType string, resourceIDs []string) ([]string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}

	reqs := make([]CheckRequest, len(resourceIDs))
	for i, id := range resourceIDs {
		reqs[i] = CheckRequest{
			Principal:  principal,
			Permission: permission,
			Resource:   Resource{Type: resourceType, ID: id},
		}
	}

	results, err := s.CheckBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}

	allowed := make([]string, 0, len(resourceIDs))
	for i, result := range results {
		if result.Allowed {
			allowed = append(allowed, resourceIDs[i])
		}
	}
	return allowed, nil
}

// =============================================================================
// Internal check logic
// =============================================================================

// checkPermission evaluates one (resource, permission) state. depth counts the
// Through hops taken to reach it (0 at the top resource). The depth boundary is
// pinned to `>`: MaxThroughDepth is the MAXIMUM number of Through hops, so
// depth == MaxThroughDepth is the last permitted hop and depth > MaxThroughDepth
// is exhaustion. stack holds the (resource, permission) states on the ACTIVE
// recursion path for path-local cycle detection; it is distinct from the shared
// budget, which tallies distinct graph states for cost across the whole decision.
func (s *Service) checkPermission(ctx context.Context, req CheckRequest, checks []PermissionCheck, depth int, b *budget, stack map[stateKey]bool) (CheckResult, error) {
	if err := ctx.Err(); err != nil {
		// Never begin the recursion (or any store call below it) after the caller
		// has canceled: fail closed on the context error, not a deny.
		return CheckResult{}, err
	}
	if depth > b.limits.MaxThroughDepth {
		// Budget exhaustion is INDETERMINATE, not a deny: the caller fails closed
		// on ErrEvaluationLimit (wrapping sdk.ErrUnavailable) rather than being
		// told "not allowed". A host that legitimately needs deeper traversal
		// sizes Config.Limits.MaxThroughDepth deliberately.
		return CheckResult{}, ErrEvaluationLimit
	}

	key := stateKey{req.Resource.Type, req.Resource.ID, req.Permission}
	if stack[key] {
		// PATH-LOCAL cycle: this state is already being evaluated up-stack. For a
		// permission rule's OR-semantics, the in-progress frame contributes no
		// additional grant, so this branch denies (it is NOT a budget error).
		return CheckResult{Allowed: false, ReasonCode: ReasonDenied, Reason: "cycle detected"}, nil
	}
	if err := b.chargeState(req.Resource.Type, req.Resource.ID, req.Permission); err != nil {
		return CheckResult{}, err
	}
	stack[key] = true
	defer delete(stack, key)

	for _, check := range checks {
		if check.Through != "" {
			result, err := s.checkThrough(ctx, req, check, depth, b, stack)
			if err != nil {
				return CheckResult{}, err
			}
			b.record(ExplainStep{
				ResourceType: req.Resource.Type, ResourceID: req.Resource.ID,
				Permission: req.Permission, Relation: check.Through,
				Kind: ExplainKindThrough, Depth: depth, Outcome: outcomeReason(result.Allowed),
			})
			if result.Allowed {
				return result, nil
			}
		} else {
			result, err := s.checkDirectRelation(ctx, req, check.Relation)
			if err != nil {
				return CheckResult{}, err
			}
			b.record(ExplainStep{
				ResourceType: req.Resource.Type, ResourceID: req.Resource.ID,
				Permission: req.Permission, Relation: check.Relation,
				Kind: ExplainKindDirect, Depth: depth, Outcome: outcomeReason(result.Allowed),
			})
			if result.Allowed {
				return result, nil
			}
		}
	}

	return CheckResult{Allowed: false, ReasonCode: ReasonDenied, Reason: "no matching rule"}, nil
}

// mapExpansionBudget translates the store-layer group-expansion overflow signal
// (relationship.ErrExpansionBudgetExceeded) into the engine's own indeterminate
// budget outcome (ErrEvaluationLimit) at the engine boundary — the domain never
// imports the engine sentinel. Both wrap sdk.ErrUnavailable, so the host-facing
// posture (503, fail closed, retryable) is identical. Any other error passes
// through unchanged.
func mapExpansionBudget(err error) error {
	if errors.Is(err, relationship.ErrExpansionBudgetExceeded) {
		return ErrEvaluationLimit
	}
	return err
}

func (s *Service) checkDirectRelation(ctx context.Context, req CheckRequest, relation string) (CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return CheckResult{}, err
	}
	allowed, err := s.store.CheckRelationWithGroupExpansion(
		ctx, req.Resource.Type, req.Resource.ID, relation, req.Principal.Type, req.Principal.ID, s.limits.MaxGraphStates,
	)
	if err != nil {
		return CheckResult{}, mapExpansionBudget(err)
	}
	if allowed {
		return CheckResult{Allowed: true, ReasonCode: ReasonGranted, Reason: fmt.Sprintf("direct:%s", relation)}, nil
	}
	return CheckResult{Allowed: false, ReasonCode: ReasonDenied}, nil
}

func (s *Service) checkThrough(ctx context.Context, req CheckRequest, check PermissionCheck, depth int, b *budget, stack map[stateKey]bool) (CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return CheckResult{}, err
	}
	targets, err := s.store.GetRelationTargets(ctx, req.Resource.Type, req.Resource.ID, check.Through)
	if err != nil {
		return CheckResult{}, err
	}
	// Bound the per-hop fan-out (MaxRelationTargets): a hop wider than the budget
	// is indeterminate, never a deny.
	if err := b.chargeFanout(len(targets)); err != nil {
		return CheckResult{}, err
	}

	for _, target := range targets {
		// A navigational Through edge points only at concrete resource references.
		// Compile is the boot gate: it rejects userset targets on any relation used
		// by a Through, so no such tuple can validly exist. A stored userset here is
		// therefore off-schema data — skip it, failing closed rather than traversing
		// into a userset the model forbids.
		if target.IsUserset() {
			continue
		}
		targetChecks := s.compiled.permissionChecks(target.Type, check.Permission)
		result, err := s.checkPermission(ctx, CheckRequest{
			Principal:  req.Principal,
			Permission: check.Permission,
			Resource:   Resource{Type: target.Type, ID: target.ID},
		}, targetChecks, depth+1, b, stack)
		if err != nil {
			return CheckResult{}, err
		}
		if result.Allowed {
			result.ReasonCode = ReasonGranted
			result.Reason = fmt.Sprintf("through:%s->%s", check.Through, result.Reason)
			return result, nil
		}
	}

	return CheckResult{Allowed: false, ReasonCode: ReasonDenied}, nil
}

// =============================================================================
// Batch internals
// =============================================================================

func (s *Service) checkBatchSequential(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	results := make([]CheckResult, len(reqs))
	for i, req := range reqs {
		result, err := s.Check(ctx, req)
		if err != nil {
			return nil, err
		}
		results[i] = result
	}
	return results, nil
}

func (s *Service) checkBatchOptimized(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	results := make([]CheckResult, len(reqs))
	first := reqs[0]

	checks := s.compiled.permissionChecks(first.Resource.Type, first.Permission)
	if len(checks) == 0 {
		for i := range results {
			results[i] = CheckResult{Allowed: false, ReasonCode: ReasonDenied, Reason: "no rules defined"}
		}
		return results, nil
	}

	resourceIDs := make([]string, 0, len(reqs))
	for _, req := range reqs {
		resourceIDs = append(resourceIDs, req.Resource.ID)
	}

	// grantedBy records the relation that ACTUALLY granted each resource — the
	// FIRST direct check (in the compiled, sorted check order) whose batch query
	// returned true. The earlier implementation reported checks[0]'s relation for
	// every grant, naming a relation that may not have granted (the audit's "batch
	// reasons can name a relation that did not grant"); this pins the debug Reason
	// to the real granting relation while ReasonCode stays the stable coarse code.
	grantedBy := make(map[string]string)
	for _, check := range checks {
		if check.Relation == "" {
			continue
		}
		batchResults, err := s.store.CheckBatchDirect(
			ctx, first.Resource.Type, resourceIDs, check.Relation, first.Principal.Type, first.Principal.ID, s.limits.MaxGraphStates,
		)
		if err != nil {
			return nil, mapExpansionBudget(err)
		}
		for resourceID, allowed := range batchResults {
			if allowed {
				if _, seen := grantedBy[resourceID]; !seen {
					grantedBy[resourceID] = check.Relation
				}
			}
		}
	}

	for i, req := range reqs {
		if relation, ok := grantedBy[req.Resource.ID]; ok {
			results[i] = CheckResult{Allowed: true, ReasonCode: ReasonGranted, Reason: fmt.Sprintf("direct:%s", relation)}
		} else {
			results[i] = CheckResult{Allowed: false, ReasonCode: ReasonDenied, Reason: "no matching rule"}
		}
	}

	return results, nil
}

// =============================================================================
// Relationship management
// =============================================================================

// CreateRelationships validates the batch against the schema, MINTS each tuple's
// relationship_id from the injected generator (Q6), and persists. The mint is
// all-or-none per batch (one generator): a cryptids.Database generator yields ""
// for every id so the store omits the column. The caller's slice is not mutated.
func (s *Service) CreateRelationships(ctx context.Context, relationships []relationship.CreateRelationship) error {
	if len(relationships) == 0 {
		return nil
	}
	if err := s.ValidateRelationships(relationships); err != nil {
		return err
	}

	out := make([]relationship.CreateRelationship, len(relationships))
	copy(out, relationships)
	for i := range out {
		out[i].RelationshipID = s.ids.MustGenerate()
	}
	return s.store.CreateRelationships(ctx, out)
}

// SetRelationTargets validates and atomically reconciles one resource+relation
// to the supplied target set. It is state based: no operation identity or replay
// ledger participates, so A -> B -> A restores A. Duplicate targets are folded
// before IDs are minted and before the store call.
func (s *Service) SetRelationTargets(ctx context.Context, resource Resource, relationName string, targets []relationship.SubjectRef) error {
	if err := relationship.ValidateRefField("resource type", resource.Type); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("resource id", resource.ID); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("relation", relationName); err != nil {
		return err
	}
	if err := s.ValidateRelationName(resource.Type, relationName); err != nil {
		return err
	}

	seen := make(map[relationship.SubjectRef]struct{}, len(targets))
	rows := make([]relationship.CreateRelationship, 0, len(targets))
	for _, target := range targets {
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		row := relationship.CreateRelationship{
			ResourceType:    resource.Type,
			ResourceID:      resource.ID,
			Relation:        relationName,
			SubjectType:     target.Type,
			SubjectID:       target.ID,
			SubjectRelation: target.Relation,
		}
		if err := row.Validate(); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s: %w",
				row.ResourceType, row.ResourceID, row.Relation, row.Subject(), err)
		}
		if err := s.ValidateRelation(row.ResourceType, row.Relation, row.SubjectType, row.SubjectRelation); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s: %w",
				row.ResourceType, row.ResourceID, row.Relation, row.Subject(), err)
		}
		row.RelationshipID = s.ids.MustGenerate()
		rows = append(rows, row)
	}
	return s.store.SetRelationTargets(ctx, resource.Type, resource.ID, relationName, rows)
}

// CheckRelationExists is a raw existence probe for a direct tuple: it does not
// consult the schema, traverse, or honor platform-admin/self bypasses. Use it
// for dedup before CreateRelationships, never for permission decisions.
func (s *Service) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return s.store.CheckRelationExists(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

// DeleteResourceRelationships removes all relationships for a resource.
func (s *Service) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	if err := relationship.ValidateRefField("resource type", resourceType); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("resource id", resourceID); err != nil {
		return err
	}
	return s.store.DeleteResourceRelationships(ctx, resourceType, resourceID)
}

// DeleteRelationshipTarget removes one exact tuple, including a userset's
// relation component. It deliberately performs structural rather than current
// schema validation so a tuple rejected by a newer schema remains removable.
func (s *Service) DeleteRelationshipTarget(ctx context.Context, resource Resource, relationName string, target relationship.SubjectRef) error {
	if err := relationship.ValidateRefField("resource type", resource.Type); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("resource id", resource.ID); err != nil {
		return err
	}
	if err := relationship.ValidateRefField("relation", relationName); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	return s.store.DeleteRelationshipTarget(ctx, resource.Type, resource.ID, relationName, target)
}

// DeleteRelationship removes a specific relationship tuple.
func (s *Service) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return s.store.DeleteRelationship(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

// DeleteByResourceAndSubject removes all relations a subject holds on a resource.
func (s *Service) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	return s.store.DeleteByResourceAndSubject(ctx, resourceType, resourceID, subjectType, subjectID)
}

// =============================================================================
// Relationship validation
// =============================================================================

// ValidateRelation checks a relationship is allowed by the schema against the
// full (subject type, subject relation) pair — the exact userset. subjectRelation
// is "" for a concrete subject and the userset relation otherwise. A concrete
// group is NOT accepted where only group#member is allowed, and group#admin never
// satisfies a group#member requirement: the pair must match an AllowedSubjects
// entry exactly.
func (s *Service) ValidateRelation(resourceType, relation, subjectType, subjectRelation string) error {
	subjects, resourceTypeOK, relationOK := s.compiled.relationSubjects(resourceType, relation)
	if !resourceTypeOK {
		return fmt.Errorf("unknown resource type %q: %w", resourceType, ErrInvalidRelation)
	}
	if !relationOK {
		return fmt.Errorf("unknown relation %q on %q: %w", relation, resourceType, ErrInvalidRelation)
	}

	for _, allowed := range subjects {
		if allowed.Type == subjectType && allowed.Relation == subjectRelation {
			return nil
		}
	}

	subj := subjectType
	if subjectRelation != "" {
		subj = subjectType + "#" + subjectRelation
	}
	return fmt.Errorf("subject %q not allowed for %q on %q: %w", subj, relation, resourceType, ErrInvalidRelation)
}

// ValidateRelationName reports whether a resource type and relation exist in
// the compiled schema. It is needed for an empty desired target set: there is no
// subject row through which ValidateRelation could otherwise validate the key.
func (s *Service) ValidateRelationName(resourceType, relationName string) error {
	_, resourceTypeOK, relationOK := s.compiled.relationSubjects(resourceType, relationName)
	if !resourceTypeOK {
		return fmt.Errorf("unknown resource type %q: %w", resourceType, ErrInvalidRelation)
	}
	if !relationOK {
		return fmt.Errorf("unknown relation %q on %q: %w", relationName, resourceType, ErrInvalidRelation)
	}
	return nil
}

// ValidateRelationships validates every relationship: first the structural
// shape (non-empty, bounded, UTF-8, control-char-free — CreateRelationship.Validate),
// then schema conformance. A non-empty stored userset relation is preserved, never
// erased, by the structural pass.
func (s *Service) ValidateRelationships(relationships []relationship.CreateRelationship) error {
	for _, r := range relationships {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s: %w",
				r.ResourceType, r.ResourceID, r.Relation, r.Subject(), err)
		}
		if err := s.ValidateRelation(r.ResourceType, r.Relation, r.SubjectType, r.SubjectRelation); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s: %w",
				r.ResourceType, r.ResourceID, r.Relation, r.Subject(), err)
		}
	}
	return nil
}

// =============================================================================
// Schema queries
// =============================================================================

// GetSchema returns a deep, read-only snapshot of the compiled schema. The
// snapshot shares no memory with the engine's compiled artifact, so a caller can
// neither reach the runtime policy maps nor race the engine.
func (s *Service) GetSchema() SchemaSnapshot {
	return s.compiled.Snapshot()
}

// SchemaDigest returns the compiled schema's stable digest. Two engines built
// from semantically equal schemas report the same digest.
func (s *Service) SchemaDigest() string {
	return s.compiled.Digest()
}

// GetPermissionsForRelation returns all permissions a relation grants on a
// resource type, read from the precomputed sorted index (no map ranging).
func (s *Service) GetPermissionsForRelation(resourceType, relation string) []string {
	return s.compiled.permissionsForRelation(resourceType, relation)
}

// =============================================================================
// Relationship queries (store delegation)
// =============================================================================

// GetRelationTargets returns all subjects with a specific relation to a resource.
func (s *Service) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	return s.store.GetRelationTargets(ctx, resourceType, resourceID, relation)
}

// ListRelationshipsBySubject pages the resources a subject relates to.
func (s *Service) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
	return s.store.ListRelationshipsBySubject(ctx, subjectType, subjectID, filter, req)
}

// ListRelationshipsByResource pages the subjects related to a resource.
func (s *Service) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
	return s.store.ListRelationshipsByResource(ctx, resourceType, resourceID, filter, req)
}

// CountByResourceAndRelation counts DIRECT tuples for a resource+relation.
func (s *Service) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	return s.store.CountByResourceAndRelation(ctx, resourceType, resourceID, relation)
}

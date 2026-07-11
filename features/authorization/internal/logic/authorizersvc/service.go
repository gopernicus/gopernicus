package authorizersvc

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// defaultMaxTraversalDepth bounds the engine's Go through-traversal recursion
// when Config.MaxTraversalDepth is unset. It is ENGINE-ONLY: it is never
// threaded into the store — group expansion in the store/memstore is
// unbounded-but-cycle-safe (2026-07-08 owner ruling).
const defaultMaxTraversalDepth = 10

var (
	// ErrInvalidRelation indicates a relationship is not allowed by the schema.
	ErrInvalidRelation = fmt.Errorf("authorization relation: %w", sdk.ErrInvalidInput)

	// ErrInvalidSchema indicates the schema has structural errors (undefined
	// references, circular through-relations, etc.).
	ErrInvalidSchema = fmt.Errorf("authorization schema: %w", sdk.ErrInvalidInput)

	// ErrCannotRemoveLastOwner indicates the operation would orphan a resource by
	// removing its only owner.
	ErrCannotRemoveLastOwner = fmt.Errorf("authorization last owner: %w", sdk.ErrConflict)
)

// Config holds the relationship engine's policy settings. Both fields are
// relationship-kind-scoped.
type Config struct {
	// MaxTraversalDepth bounds the engine's through-traversal recursion; <= 0
	// resolves to defaultMaxTraversalDepth. Engine-only — never passed to a store.
	//
	// D3 sizing: a host collapsing a hand-walked hierarchy into schema must size
	// this deliberately. checkThrough silently DENIES past the bound with reason
	// "max depth exceeded", and each Check hop costs one GetRelationTargets
	// round-trip. The bound governs ONLY this Go recursion — never the store's
	// descendant walk, which is unbounded-but-cycle-safe (2026-07-08 ruling, see
	// defaultMaxTraversalDepth above).
	MaxTraversalDepth int
	// IDs mints each tuple's relationship_id at CreateRelationships (Q6). The
	// zero value is the nanoid default; a cryptids.Database generator yields ""
	// so the store omits the id column and the DDL DEFAULT fills it.
	IDs cryptids.IDGenerator
}

// Service is the sealed ReBAC evaluation engine. It evaluates permission checks
// and manages relationship tuples against a relationship.Storer and a schema.
type Service struct {
	store    relationship.Storer
	schema   Schema
	ids      cryptids.IDGenerator
	maxDepth int
}

// NewService validates the schema and builds the engine. An invalid schema is
// rejected loudly (wrapping ErrInvalidSchema); MaxTraversalDepth <= 0 resolves
// to the default.
func NewService(store relationship.Storer, schema Schema, cfg Config) (*Service, error) {
	if err := ValidateSchema(schema); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
	}
	depth := cfg.MaxTraversalDepth
	if depth <= 0 {
		depth = defaultMaxTraversalDepth
	}
	return &Service{
		store:    store,
		schema:   schema,
		ids:      cfg.IDs,
		maxDepth: depth,
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
	rules := s.getPermissionRules(req.Resource.Type, req.Permission)
	if rules.AnyOf == nil {
		return CheckResult{Allowed: false, Reason: "no rules defined"}, nil
	}

	visited := make(map[string]bool)
	return s.checkPermission(ctx, req, rules, 0, visited)
}

// CheckBatch evaluates multiple checks. It uses an optimized batch query when
// all requests share subject, permission, and resource type with no
// through-relations; otherwise it falls back to sequential checks.
func (s *Service) CheckBatch(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	canBatch := true
	first := reqs[0]
	for i := 1; i < len(reqs); i++ {
		if reqs[i].Subject != first.Subject ||
			reqs[i].Permission != first.Permission ||
			reqs[i].Resource.Type != first.Resource.Type {
			canBatch = false
			break
		}
	}

	if !canBatch {
		return s.checkBatchSequential(ctx, reqs)
	}

	rules := s.getPermissionRules(first.Resource.Type, first.Permission)
	for _, check := range rules.AnyOf {
		if check.Through != "" {
			return s.checkBatchSequential(ctx, reqs)
		}
	}

	return s.checkBatchOptimized(ctx, reqs)
}

// FilterAuthorized returns only the resource IDs the subject can access, via
// CheckBatch.
func (s *Service) FilterAuthorized(ctx context.Context, subject Subject, permission, resourceType string, resourceIDs []string) ([]string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}

	reqs := make([]CheckRequest, len(resourceIDs))
	for i, id := range resourceIDs {
		reqs[i] = CheckRequest{
			Subject:    subject,
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

func (s *Service) checkPermission(ctx context.Context, req CheckRequest, rules PermissionRule, depth int, visited map[string]bool) (CheckResult, error) {
	if depth > s.maxDepth {
		return CheckResult{Allowed: false, Reason: "max depth exceeded"}, nil
	}

	visitKey := fmt.Sprintf("%s:%s#%s", req.Resource.Type, req.Resource.ID, req.Permission)
	if visited[visitKey] {
		return CheckResult{Allowed: false, Reason: "cycle detected"}, nil
	}
	visited[visitKey] = true

	for _, check := range rules.AnyOf {
		if check.Through != "" {
			result, err := s.checkThrough(ctx, req, check, depth, visited)
			if err != nil {
				return CheckResult{}, err
			}
			if result.Allowed {
				return result, nil
			}
		} else {
			result, err := s.checkDirectRelation(ctx, req, check.Relation)
			if err != nil {
				return CheckResult{}, err
			}
			if result.Allowed {
				return result, nil
			}
		}
	}

	return CheckResult{Allowed: false, Reason: "no matching rule"}, nil
}

func (s *Service) checkDirectRelation(ctx context.Context, req CheckRequest, relation string) (CheckResult, error) {
	allowed, err := s.store.CheckRelationWithGroupExpansion(
		ctx, req.Resource.Type, req.Resource.ID, relation, req.Subject.Type, req.Subject.ID,
	)
	if err != nil {
		return CheckResult{}, err
	}
	if allowed {
		return CheckResult{Allowed: true, Reason: fmt.Sprintf("direct:%s", relation)}, nil
	}
	return CheckResult{Allowed: false}, nil
}

func (s *Service) checkThrough(ctx context.Context, req CheckRequest, check PermissionCheck, depth int, visited map[string]bool) (CheckResult, error) {
	targets, err := s.store.GetRelationTargets(ctx, req.Resource.Type, req.Resource.ID, check.Through)
	if err != nil {
		return CheckResult{}, err
	}

	for _, target := range targets {
		targetRules := s.getPermissionRules(target.SubjectType, check.Permission)
		result, err := s.checkPermission(ctx, CheckRequest{
			Subject:    req.Subject,
			Permission: check.Permission,
			Resource:   Resource{Type: target.SubjectType, ID: target.SubjectID},
		}, targetRules, depth+1, visited)
		if err != nil {
			return CheckResult{}, err
		}
		if result.Allowed {
			result.Reason = fmt.Sprintf("through:%s->%s", check.Through, result.Reason)
			return result, nil
		}
	}

	return CheckResult{Allowed: false}, nil
}

func (s *Service) getPermissionRules(resourceType, permission string) PermissionRule {
	if rtDef, ok := s.schema.ResourceTypes[resourceType]; ok {
		if rule, ok := rtDef.Permissions[permission]; ok {
			return rule
		}
	}
	return PermissionRule{}
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

	results := make([]CheckResult, len(reqs))
	first := reqs[0]

	rules := s.getPermissionRules(first.Resource.Type, first.Permission)
	if len(rules.AnyOf) == 0 {
		for i := range results {
			results[i] = CheckResult{Allowed: false, Reason: "no rules defined"}
		}
		return results, nil
	}

	resourceIDs := make([]string, 0, len(reqs))
	for _, req := range reqs {
		resourceIDs = append(resourceIDs, req.Resource.ID)
	}

	allowedMap := make(map[string]bool)
	for _, check := range rules.AnyOf {
		if check.Relation == "" {
			continue
		}
		batchResults, err := s.store.CheckBatchDirect(
			ctx, first.Resource.Type, resourceIDs, check.Relation, first.Subject.Type, first.Subject.ID,
		)
		if err != nil {
			return nil, err
		}
		for resourceID, allowed := range batchResults {
			if allowed {
				allowedMap[resourceID] = true
			}
		}
	}

	for i, req := range reqs {
		resourceID := req.Resource.ID
		if allowedMap[resourceID] {
			for _, check := range rules.AnyOf {
				if check.Relation != "" {
					results[i] = CheckResult{Allowed: true, Reason: fmt.Sprintf("direct:%s", check.Relation)}
					break
				}
			}
		} else {
			results[i] = CheckResult{Allowed: false, Reason: "no matching rule"}
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

// CheckRelationExists is a raw existence probe for a direct tuple: it does not
// consult the schema, traverse, or honor platform-admin/self bypasses. Use it
// for dedup before CreateRelationships, never for permission decisions.
func (s *Service) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return s.store.CheckRelationExists(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

// DeleteResourceRelationships removes all relationships for a resource.
func (s *Service) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	return s.store.DeleteResourceRelationships(ctx, resourceType, resourceID)
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

// ValidateRelation checks a relationship is allowed by the schema.
func (s *Service) ValidateRelation(resourceType, relation, subjectType string) error {
	rtDef, ok := s.schema.ResourceTypes[resourceType]
	if !ok {
		return fmt.Errorf("unknown resource type %q: %w", resourceType, ErrInvalidRelation)
	}

	relDef, ok := rtDef.Relations[relation]
	if !ok {
		return fmt.Errorf("unknown relation %q on %q: %w", relation, resourceType, ErrInvalidRelation)
	}

	for _, allowed := range relDef.AllowedSubjects {
		if allowed.Type == subjectType {
			return nil
		}
	}

	return fmt.Errorf("subject type %q not allowed for %q on %q: %w", subjectType, relation, resourceType, ErrInvalidRelation)
}

// ValidateRelationships validates every relationship against the schema.
func (s *Service) ValidateRelationships(relationships []relationship.CreateRelationship) error {
	for _, r := range relationships {
		if err := s.ValidateRelation(r.ResourceType, r.Relation, r.SubjectType); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s:%s: %w",
				r.ResourceType, r.ResourceID, r.Relation, r.SubjectType, r.SubjectID, err)
		}
	}
	return nil
}

// =============================================================================
// Schema queries
// =============================================================================

// GetSchema returns the authorization schema.
func (s *Service) GetSchema() Schema {
	return s.schema
}

// GetPermissionsForRelation returns all permissions a relation grants on a
// resource type.
func (s *Service) GetPermissionsForRelation(resourceType, relation string) []string {
	rtDef, ok := s.schema.ResourceTypes[resourceType]
	if !ok {
		return nil
	}

	var permissions []string
	for permName, rule := range rtDef.Permissions {
		for _, check := range rule.AnyOf {
			if check.Relation == relation {
				permissions = append(permissions, permName)
				break
			}
		}
	}
	return permissions
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

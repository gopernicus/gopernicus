package authorization

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gopernicus/gopernicus/sdk/fop"
)

// Config holds authorization policy settings.
// Use sdk/environment.ParseEnvTags to populate from environment variables.
//
//	var cfg authorization.Config
//	environment.ParseEnvTags("APP", &cfg)
type Config struct {
	MaxTraversalDepth int `env:"AUTHORIZATION_TRAVERSAL_MAX_DEPTH" default:"10"`
}

// Authorizer provides permission checking and relationship management.
type Authorizer struct {
	store  Storer
	schema Schema
	log    *slog.Logger
	config Config
}

// Option configures an Authorizer during construction.
type Option func(*Authorizer)

// WithLogger sets a structured logger. If not provided, slog.Default() is used.
func WithLogger(log *slog.Logger) Option {
	return func(a *Authorizer) {
		a.log = log
	}
}

// NewAuthorizer creates a new Authorizer.
//
//	authz := authorization.NewAuthorizer(store, schema, cfg,
//	    authorization.WithLogger(log),
//	)
func NewAuthorizer(store Storer, schema Schema, cfg Config, opts ...Option) *Authorizer {
	a := &Authorizer{
		store:  store,
		schema: schema,
		config: cfg,
		log:    slog.Default(),
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.config.MaxTraversalDepth <= 0 {
		a.config.MaxTraversalDepth = 10
	}
	return a
}

// =============================================================================
// Permission Checks
// =============================================================================

// Check performs a permission check.
//
// Evaluation order: platform admin bypass → self-access → schema rules
// (direct relations + through-relation traversal with cycle detection).
func (a *Authorizer) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	// Platform admin bypasses all checks.
	if allowed, err := a.checkPlatformAdmin(ctx, req.Subject); err != nil {
		return CheckResult{}, err
	} else if allowed {
		return CheckResult{Allowed: true, Reason: "platform:admin"}, nil
	}

	// Self-access: user accessing their own record.
	if allowed, reason := a.checkSelf(req); allowed {
		return CheckResult{Allowed: true, Reason: reason}, nil
	}

	// Schema-driven evaluation.
	rules := a.getPermissionRules(req.Resource.Type, req.Permission)
	if rules.AnyOf == nil {
		return CheckResult{Allowed: false, Reason: "no rules defined"}, nil
	}

	visited := make(map[string]bool)
	return a.checkPermission(ctx, req, rules, 0, visited)
}

// CheckBatch performs multiple permission checks.
// Uses an optimized batch query when all requests share the same subject,
// permission, and resource type with no through-relations. Falls back to
// sequential checks otherwise.
func (a *Authorizer) CheckBatch(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	// Check if all requests share subject, permission, resource type.
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
		return a.checkBatchSequential(ctx, reqs)
	}

	// Through-relations need per-resource traversal.
	rules := a.getPermissionRules(first.Resource.Type, first.Permission)
	for _, check := range rules.AnyOf {
		if check.Through != "" {
			return a.checkBatchSequential(ctx, reqs)
		}
	}

	return a.checkBatchOptimized(ctx, reqs)
}

// FilterAuthorized returns only the resource IDs that the subject can access.
// Uses CheckBatch for efficient batch evaluation.
func (a *Authorizer) FilterAuthorized(ctx context.Context, subject Subject, permission, resourceType string, resourceIDs []string) ([]string, error) {
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

	results, err := a.CheckBatch(ctx, reqs)
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
// Internal Check Logic
// =============================================================================

func (a *Authorizer) checkPermission(
	ctx context.Context,
	req CheckRequest,
	rules PermissionRule,
	depth int,
	visited map[string]bool,
) (CheckResult, error) {
	if depth > a.config.MaxTraversalDepth {
		return CheckResult{Allowed: false, Reason: "max depth exceeded"}, nil
	}

	visitKey := fmt.Sprintf("%s:%s#%s", req.Resource.Type, req.Resource.ID, req.Permission)
	if visited[visitKey] {
		return CheckResult{Allowed: false, Reason: "cycle detected"}, nil
	}
	visited[visitKey] = true

	for _, check := range rules.AnyOf {
		if check.Through != "" {
			result, err := a.checkThrough(ctx, req, check, depth, visited)
			if err != nil {
				return CheckResult{}, err
			}
			if result.Allowed {
				return result, nil
			}
		} else {
			result, err := a.checkDirectRelation(ctx, req, check.Relation)
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

func (a *Authorizer) checkDirectRelation(ctx context.Context, req CheckRequest, relation string) (CheckResult, error) {
	allowed, err := a.store.CheckRelationWithGroupExpansion(
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

func (a *Authorizer) checkThrough(
	ctx context.Context,
	req CheckRequest,
	check PermissionCheck,
	depth int,
	visited map[string]bool,
) (CheckResult, error) {
	targets, err := a.store.GetRelationTargets(ctx, req.Resource.Type, req.Resource.ID, check.Through)
	if err != nil {
		return CheckResult{}, err
	}

	for _, target := range targets {
		targetRules := a.getPermissionRules(target.SubjectType, check.Permission)
		result, err := a.checkPermission(ctx, CheckRequest{
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

func (a *Authorizer) checkPlatformAdmin(ctx context.Context, subj Subject) (bool, error) {
	return a.store.CheckRelationExists(ctx, "platform", "main", "admin", subj.Type, subj.ID)
}

func (a *Authorizer) checkSelf(req CheckRequest) (bool, string) {
	if req.Resource.Type != req.Subject.Type {
		return false, ""
	}
	if req.Resource.Type != "user" && req.Resource.Type != "service_account" {
		return false, ""
	}
	if req.Subject.ID != req.Resource.ID {
		return false, ""
	}
	switch req.Permission {
	case "read", "update", "delete":
		return true, "self"
	default:
		return false, ""
	}
}

func (a *Authorizer) getPermissionRules(resourceType, permission string) PermissionRule {
	if rtDef, ok := a.schema.ResourceTypes[resourceType]; ok {
		if rule, ok := rtDef.Permissions[permission]; ok {
			return rule
		}
	}
	return PermissionRule{}
}

// =============================================================================
// Batch Internals
// =============================================================================

func (a *Authorizer) checkBatchSequential(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	results := make([]CheckResult, len(reqs))
	for i, req := range reqs {
		result, err := a.Check(ctx, req)
		if err != nil {
			return nil, err
		}
		results[i] = result
	}
	return results, nil
}

func (a *Authorizer) checkBatchOptimized(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	results := make([]CheckResult, len(reqs))
	first := reqs[0]

	// Platform admin bypass.
	if allowed, err := a.checkPlatformAdmin(ctx, first.Subject); err != nil {
		return nil, err
	} else if allowed {
		for i := range results {
			results[i] = CheckResult{Allowed: true, Reason: "platform:admin"}
		}
		return results, nil
	}

	// Handle self-access.
	var needsDBCheck []int
	for i, req := range reqs {
		if allowed, reason := a.checkSelf(req); allowed {
			results[i] = CheckResult{Allowed: true, Reason: reason}
		} else {
			needsDBCheck = append(needsDBCheck, i)
		}
	}
	if len(needsDBCheck) == 0 {
		return results, nil
	}

	// Get permission rules.
	rules := a.getPermissionRules(first.Resource.Type, first.Permission)
	if len(rules.AnyOf) == 0 {
		for _, i := range needsDBCheck {
			results[i] = CheckResult{Allowed: false, Reason: "no rules defined"}
		}
		return results, nil
	}

	// Collect resource IDs that need checking.
	resourceIDs := make([]string, 0, len(needsDBCheck))
	for _, i := range needsDBCheck {
		resourceIDs = append(resourceIDs, reqs[i].Resource.ID)
	}

	// Batch check each direct relation.
	allowedMap := make(map[string]bool)
	for _, check := range rules.AnyOf {
		if check.Relation == "" {
			continue
		}
		batchResults, err := a.store.CheckBatchDirect(
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

	// Apply results.
	for _, i := range needsDBCheck {
		resourceID := reqs[i].Resource.ID
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
// Relationship Management
// =============================================================================

// CreateRelationships creates authorization relationships for a resource.
// Validates all relationships against the schema before persisting.
func (a *Authorizer) CreateRelationships(ctx context.Context, relationships []CreateRelationship) error {
	if len(relationships) == 0 {
		return nil
	}
	if err := a.ValidateRelationships(relationships); err != nil {
		return err
	}
	return a.store.CreateRelationships(ctx, relationships)
}

// DeleteResourceRelationships removes all relationships for a resource.
func (a *Authorizer) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	return a.store.DeleteResourceRelationships(ctx, resourceType, resourceID)
}

// DeleteRelationship removes a specific relationship tuple.
func (a *Authorizer) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return a.store.DeleteRelationship(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

// DeleteBySubject removes all relationships between a resource and subject.
func (a *Authorizer) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	return a.store.DeleteByResourceAndSubject(ctx, resourceType, resourceID, subjectType, subjectID)
}

// =============================================================================
// Relationship Validation
// =============================================================================

// ValidateRelation checks if a relationship is allowed by the schema.
func (a *Authorizer) ValidateRelation(resourceType, relation, subjectType string) error {
	rtDef, ok := a.schema.ResourceTypes[resourceType]
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

// ValidateRelationships validates all relationships against the schema.
func (a *Authorizer) ValidateRelationships(relationships []CreateRelationship) error {
	for _, r := range relationships {
		if err := a.ValidateRelation(r.ResourceType, r.Relation, r.SubjectType); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s:%s: %w",
				r.ResourceType, r.ResourceID, r.Relation, r.SubjectType, r.SubjectID, err)
		}
	}
	return nil
}

// =============================================================================
// Schema Queries
// =============================================================================

// GetSchema returns the authorization schema.
func (a *Authorizer) GetSchema() Schema {
	return a.schema
}

// GetPermissionsForRelation returns all permissions granted by a relation
// on a resource type. Useful for building permission lists in API responses.
func (a *Authorizer) GetPermissionsForRelation(resourceType, relation string) []string {
	rtDef, ok := a.schema.ResourceTypes[resourceType]
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
// Relationship Queries (store delegation)
// =============================================================================

// GetRelationTargets returns all subjects with a specific relation to a resource.
func (a *Authorizer) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	return a.store.GetRelationTargets(ctx, resourceType, resourceID, relation)
}

// ListRelationshipsBySubject returns all resources a subject has relationships with.
func (a *Authorizer) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, orderBy fop.Order, page fop.PageStringCursor) ([]SubjectRelationship, fop.Pagination, error) {
	return a.store.ListRelationshipsBySubject(ctx, subjectType, subjectID, filter, orderBy, page)
}

// ListRelationshipsByResource returns all subjects with relationships to a resource.
func (a *Authorizer) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, orderBy fop.Order, page fop.PageStringCursor) ([]ResourceRelationship, fop.Pagination, error) {
	return a.store.ListRelationshipsByResource(ctx, resourceType, resourceID, filter, orderBy, page)
}

// CountByResourceAndRelation counts relationships by resource and relation.
func (a *Authorizer) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	return a.store.CountByResourceAndRelation(ctx, resourceType, resourceID, relation)
}

// =============================================================================
// LookupResources
// =============================================================================

// LookupResources returns all resource IDs of a given type that the subject
// can access with the given permission.
//
// When the subject is a platform admin, returns LookupResult{Unrestricted: true}
// — the caller must skip ID filtering entirely.
//
// Otherwise returns LookupResult{IDs: [...]}, where IDs is always a non-nil
// slice. An empty slice means no access. Callers must apply the ID filter.
//
// This powers the prefilter pattern: first look up authorized IDs, then pass
// them to the repository as WHERE id = ANY(@authorized_ids).
func (a *Authorizer) LookupResources(ctx context.Context, subject Subject, permission, resourceType string) (LookupResult, error) {
	if allowed, err := a.checkPlatformAdmin(ctx, subject); err != nil {
		return LookupResult{}, err
	} else if allowed {
		return LookupResult{Unrestricted: true}, nil
	}

	rules := a.getPermissionRules(resourceType, permission)
	if len(rules.AnyOf) == 0 {
		return LookupResult{IDs: []string{}}, nil
	}

	// Collect IDs from all permission checks (union/OR).
	seen := make(map[string]bool)
	var ids []string

	for _, check := range rules.AnyOf {
		if check.Through != "" {
			throughResult, err := a.lookupThrough(ctx, subject, check, resourceType)
			if err != nil {
				return LookupResult{}, err
			}
			if throughResult.Unrestricted {
				return LookupResult{Unrestricted: true}, nil
			}
			for _, id := range throughResult.IDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		} else {
			directIDs, err := a.store.LookupResourceIDs(ctx, resourceType, []string{check.Relation}, subject.Type, subject.ID)
			if err != nil {
				return LookupResult{}, err
			}
			for _, id := range directIDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
	}

	if ids == nil {
		ids = []string{} // guarantee non-nil when Unrestricted=false
	}
	return LookupResult{IDs: ids}, nil
}

// lookupThrough resolves through-relations for LookupResources.
// Example: permission "read" on "post" Through("org", "admin") means:
//  1. Find all orgs where subject is admin → [org:O1, org:O2]
//  2. Find all posts that have relation "org" pointing to those orgs
func (a *Authorizer) lookupThrough(ctx context.Context, subject Subject, check PermissionCheck, resourceType string) (LookupResult, error) {
	rtDef, ok := a.schema.ResourceTypes[resourceType]
	if !ok {
		return LookupResult{IDs: []string{}}, nil
	}
	relDef, ok := rtDef.Relations[check.Through]
	if !ok {
		return LookupResult{IDs: []string{}}, nil
	}

	seen := make(map[string]bool)
	var ids []string

	for _, ref := range relDef.AllowedSubjects {
		targetResult, err := a.LookupResources(ctx, subject, check.Permission, ref.Type)
		if err != nil {
			return LookupResult{}, err
		}
		// If subject has unrestricted access on the target type (admin),
		// they have unrestricted access through this relation too.
		if targetResult.Unrestricted {
			return LookupResult{Unrestricted: true}, nil
		}
		if len(targetResult.IDs) == 0 {
			continue
		}

		throughIDs, err := a.store.LookupResourceIDsByRelationTarget(ctx, resourceType, check.Through, ref.Type, targetResult.IDs)
		if err != nil {
			return LookupResult{}, err
		}
		for _, id := range throughIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}

	if ids == nil {
		ids = []string{}
	}
	return LookupResult{IDs: ids}, nil
}

// Package authorizer is the pocket's implementation of record for
// sdk/capabilities/authorization: Engine serves the Zanzibar read protocol
// (Check, CheckBatch, paged LookupResources) over the pocket's own decision
// engine, plus the optional SchemaReader and Limiter.
//
// Engine adds behaviour only; it never re-implements a decision:
//   - every request passes the protocol's identifier grammar
//     (sdkauthorization.ValidateRef / ValidateObject);
//   - an undeclared resource type, permission or relation is
//     sdk.ErrInvalidInput (components.Decisions instead denies it with a nil
//     error);
//   - Check answers a declared relation through decisions.Direct and, on the
//     configured global resource, an exact global role (WithGlobalResource);
//   - a mixed batch runs as ONE snapshot through decisions.EvaluateBatch;
//   - a universal grant cannot be enumerated: LookupResources returns
//     ErrUnrestricted;
//   - lookup cursors are the engine's own, inside the shared envelope.
//
// Unrestricted, reasons and explain, FilterPage and exact role reads at any
// scope stay on decisions.Service.
package authorizer

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauthorization "github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

// ErrUnrestricted reports that the principal holds the permission on every
// resource of the type (a satisfied global role branch), which the protocol
// cannot enumerate. It wraps sdk.ErrUnavailable but is NOT transient: do not
// retry. A host that relies on universal grants reads
// decisions.Service.LookupAllResourceIDs or LookupResourceIDPage, whose results
// carry Unrestricted.
var ErrUnrestricted = fmt.Errorf("authorization: universal grant cannot be enumerated through the protocol (do not retry; use the decision service): %w", sdk.ErrUnavailable)

var _ interface {
	sdkauthorization.Authorizer
	sdkauthorization.SchemaReader
	sdkauthorization.Limiter
} = (*Engine)(nil)

type nameKind uint8

const (
	kindUndeclared nameKind = iota
	kindPermission
	kindRelation
	kindGlobalRole
)

// Option configures an Engine.
type Option func(*Engine) error

// Engine implements sdkauthorization.Authorizer, SchemaReader and Limiter over
// a decisions.Service. It is safe for concurrent use; its schema view is taken
// at construction.
type Engine struct {
	decisions   *decisions.Service
	schema      decisions.ModelSnapshot
	permissions map[string]map[string]bool
	relations   map[string]map[string]bool
	limits      sdkauthorization.Limits
	global      *globalResource
}

type globalResource struct {
	resourceType string
	id           string
	relationFor  func(string) string
	labels       map[string]string
}

// WithGlobalResource names where global roles appear through the protocol:
// relationFor(label) on resourceType:id is the exact global role label
// (tuples.Global()), never a permission of that name. The engine inverts
// relationFor over the compiled model's global Role labels and the declared
// relations of resourceType; a declared relation on that resource that no
// label maps to stays an ordinary relation.
func WithGlobalResource(resourceType, id string, relationFor func(label string) string) Option {
	return func(e *Engine) error {
		if relationFor == nil {
			return fmt.Errorf("authorizer: WithGlobalResource needs relationFor: %w", sdk.ErrInvalidInput)
		}
		if err := sdkauthorization.ValidateObject(resourceType, id); err != nil {
			return err
		}
		e.global = &globalResource{resourceType: resourceType, id: id, relationFor: relationFor}
		return nil
	}
}

// New builds an Engine over svc.
func New(svc *decisions.Service, opts ...Option) (*Engine, error) {
	if svc == nil {
		return nil, fmt.Errorf("authorizer: nil decision service: %w", sdk.ErrInvalidInput)
	}
	limits := svc.Limits()
	e := &Engine{
		decisions:   svc,
		schema:      svc.GetSchema(),
		permissions: map[string]map[string]bool{},
		relations:   map[string]map[string]bool{},
		limits:      sdkauthorization.Limits{MaxCheckBatch: limits.MaxBatchSize, MaxLookupPage: limits.MaxLookupResults},
	}
	for _, rt := range e.schema.ResourceTypes() {
		e.permissions[rt] = setOf(e.schema.Permissions(rt))
		e.relations[rt] = setOf(e.schema.Relations(rt))
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authorizer: nil option: %w", sdk.ErrInvalidInput)
		}
		if err := opt(e); err != nil {
			return nil, err
		}
	}
	if e.global != nil {
		labels, err := e.globalLabels()
		if err != nil {
			return nil, err
		}
		e.global.labels = labels
	}
	return e, nil
}

// Limits reports MaxBatchSize and MaxLookupResults of the decision service.
func (e *Engine) Limits() sdkauthorization.Limits { return e.limits }

// DeclaresResource reports a resource type of the compiled model.
func (e *Engine) DeclaresResource(resourceType string) bool {
	_, ok := e.permissions[resourceType]
	return ok
}

// DeclaresPermission reports a declared permission or relation.
func (e *Engine) DeclaresPermission(resourceType, permissionOrRelation string) bool {
	return e.permissions[resourceType][permissionOrRelation] || e.relations[resourceType][permissionOrRelation]
}

// DeclaresSubject reports whether relation admits subjects of subjectType
// (concrete or userset).
func (e *Engine) DeclaresSubject(resourceType, relation, subjectType string) bool {
	for _, s := range e.schema.AllowedSubjects(resourceType, relation) {
		if s.Type == subjectType {
			return true
		}
	}
	return false
}

// Check answers a permission or relation on one resource.
func (e *Engine) Check(ctx context.Context, req sdkauthorization.CheckRequest) (sdkauthorization.Decision, error) {
	kind, label, err := e.admit(req)
	if err != nil {
		return sdkauthorization.Decision{}, err
	}
	switch kind {
	case kindPermission:
		return e.decisions.Check(ctx, req)
	default:
		result, err := e.decisions.Evaluate(ctx, req.Principal, expression(req, kind, label))
		if err != nil {
			return sdkauthorization.Decision{}, err
		}
		return sdkauthorization.Decision{Allowed: result.Allowed}, nil
	}
}

// CheckBatch answers every request in one snapshot, all-or-nothing, in
// request order.
func (e *Engine) CheckBatch(ctx context.Context, reqs []sdkauthorization.CheckRequest) ([]sdkauthorization.Decision, error) {
	if len(reqs) > e.limits.MaxCheckBatch {
		return nil, fmt.Errorf("authorization: batch of %d exceeds MaxCheckBatch %d: %w", len(reqs), e.limits.MaxCheckBatch, sdk.ErrInvalidInput)
	}
	if len(reqs) == 0 {
		return []sdkauthorization.Decision{}, nil
	}
	checks := make([]decisions.ExpressionCheck, len(reqs))
	permissionsOnly := true
	for i, req := range reqs {
		kind, label, err := e.admit(req)
		if err != nil {
			return nil, err
		}
		permissionsOnly = permissionsOnly && kind == kindPermission
		checks[i] = decisions.ExpressionCheck{Principal: req.Principal, Expression: expression(req, kind, label)}
	}
	if permissionsOnly {
		return e.decisions.CheckBatch(ctx, reqs)
	}
	return e.decisions.EvaluateBatch(ctx, checks)
}

// LookupResources pages the resource IDs on which the principal holds a
// declared permission. A universal grant is ErrUnrestricted.
func (e *Engine) LookupResources(ctx context.Context, req sdkauthorization.LookupRequest) (sdkauthorization.LookupPage, error) {
	if err := req.Validate(); err != nil {
		return sdkauthorization.LookupPage{}, err
	}
	if req.Limit > e.limits.MaxLookupPage {
		return sdkauthorization.LookupPage{}, fmt.Errorf("authorization: lookup limit %d exceeds MaxLookupPage %d: %w", req.Limit, e.limits.MaxLookupPage, sdk.ErrInvalidInput)
	}
	switch e.classify(sdkauthorization.Resource{Type: req.ResourceType}, req.Permission) {
	case kindPermission:
	case kindUndeclared:
		return sdkauthorization.LookupPage{}, undeclared(req.ResourceType, req.Permission)
	default:
		return sdkauthorization.LookupPage{}, fmt.Errorf("authorization: lookup enumerates permissions, and %q is a relation of %q: %w", req.Permission, req.ResourceType, sdk.ErrInvalidInput)
	}
	inner, err := sdkauthorization.DecodeCursor(req)
	if err != nil {
		return sdkauthorization.LookupPage{}, err
	}
	engineReq := req
	engineReq.After = inner
	page, err := e.decisions.LookupResourceIDPage(ctx, engineReq)
	if err != nil {
		return sdkauthorization.LookupPage{}, err
	}
	if page.Unrestricted {
		return sdkauthorization.LookupPage{}, ErrUnrestricted
	}
	out := sdkauthorization.LookupPage{IDs: page.IDs}
	if page.NextCursor != "" {
		out.NextCursor = sdkauthorization.EncodeCursor(req, page.NextCursor)
	}
	return out, nil
}

// admit applies the identifier grammar and classifies the name.
func (e *Engine) admit(req sdkauthorization.CheckRequest) (nameKind, string, error) {
	if err := req.Validate(); err != nil {
		return kindUndeclared, "", err
	}
	kind := e.classify(req.Resource, req.Permission)
	switch kind {
	case kindUndeclared:
		return kind, "", undeclared(req.Resource.Type, req.Permission)
	case kindRelation:
		if label, ok := e.globalLabel(req.Resource, req.Permission); ok {
			return kindGlobalRole, label, nil
		}
	}
	return kind, "", nil
}

func (e *Engine) classify(resource sdkauthorization.Resource, name string) nameKind {
	switch {
	case e.permissions[resource.Type][name]:
		return kindPermission
	case e.relations[resource.Type][name]:
		return kindRelation
	}
	return kindUndeclared
}

func (e *Engine) globalLabel(resource sdkauthorization.Resource, relation string) (string, bool) {
	g := e.global
	if g == nil || resource.Type != g.resourceType || resource.ID != g.id {
		return "", false
	}
	label, ok := g.labels[relation]
	return label, ok
}

// globalLabels inverts relationFor: a declared relation R of the global type
// holds label L when relationFor(L) == R, for L among the model's global Role
// labels and R itself. Two labels for one relation are a configuration error.
func (e *Engine) globalLabels() (map[string]string, error) {
	g := e.global
	candidates := map[string]bool{}
	for relation := range e.relations[g.resourceType] {
		candidates[relation] = true
	}
	for _, rt := range e.schema.ResourceTypes() {
		for _, permission := range e.schema.Permissions(rt) {
			expr, _ := e.schema.Expression(rt, permission)
			collectGlobalRoles(expr, candidates)
		}
	}
	labels := map[string]string{}
	for label := range candidates {
		relation := g.relationFor(label)
		if !e.relations[g.resourceType][relation] {
			continue
		}
		if previous, ok := labels[relation]; ok && previous != label {
			return nil, fmt.Errorf("authorizer: global relation %q maps from both %q and %q: %w", relation, previous, label, sdk.ErrInvalidInput)
		}
		labels[relation] = label
	}
	return labels, nil
}

func collectGlobalRoles(expr decisions.Expression, into map[string]bool) {
	if expr.RoleName != "" && !expr.CurrentResource && expr.Resource == nil && expr.ResourceSlot == nil {
		into[expr.RoleName] = true
	}
	for _, child := range expr.AnyOf {
		collectGlobalRoles(child, into)
	}
	for _, child := range expr.AllOf {
		collectGlobalRoles(child, into)
	}
}

func expression(req sdkauthorization.CheckRequest, kind nameKind, label string) decisions.Expression {
	switch kind {
	case kindGlobalRole:
		return decisions.Role(label)
	case kindRelation:
		return decisions.On(req.Resource, decisions.Direct(req.Permission))
	default:
		return decisions.On(req.Resource, decisions.Permission(req.Permission))
	}
}

func undeclared(resourceType, name string) error {
	return fmt.Errorf("authorization: %q is not a declared permission or relation of %q: %w", name, resourceType, sdk.ErrInvalidInput)
}

func setOf(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[v] = true
	}
	return out
}

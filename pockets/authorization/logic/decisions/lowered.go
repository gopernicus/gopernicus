package decisions

import (
	"context"
	"errors"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauthorization "github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

// defaultLoweredRun bounds one speculative CheckBatch when the Checker
// declares no Limiter.
const defaultLoweredRun = sdkauthorization.DefaultCheckChunk

// ErrLoweredPolicyMisconfigured is server-class: the Checker rejected, as
// invalid input, a name a lowered policy asked about. Every client-controlled
// input is admitted before it reaches the Checker, so the rejection is a host
// misconfiguration (typically an undeclared name with no SchemaReader to catch
// it at mount) and never a client 400. It wraps no sdk class.
var ErrLoweredPolicyMisconfigured = errors.New("authorization: a lowered policy names something the authorization backend rejects")

// LoweredOption configures a LoweredEvaluator.
type LoweredOption func(*LoweredEvaluator) error

// LoweredEvaluator evaluates guard policies (Expression trees) over any
// sdkauthorization.Checker: no engine, no stores and no Components. It
// implements the HTTP guard's DecisionService.
//
// Each leaf lowers to one Check: Permission and Direct name a permission or
// relation on the leaf's resource; RoleIn/HasRole on a resource names the
// relation of that label; a global Role names relationFor(label) on the
// WithGlobalResource resource. Through and current-resource selectors cannot
// be lowered and are rejected by ValidateExpression.
//
// The walk keeps the native declaration order and short circuit, and only a
// reached leaf may surface an error. Consecutive leaves whose resource is known
// without running a non-pure resolver (Fixed, Global, a pure Path input, or an
// input already resolved) go out as one CheckBatch; answers past the deciding
// leaf are ignored, and a failed batch is re-walked one Check at a time. A
// pure input resolves speculatively and holds its error until its leaf is
// reached; a Resource resolver runs only when its leaf is reached.
//
// Unlike the native evaluator, separate CheckBatch calls are separate reads:
// a lowered policy has no single snapshot. Require therefore refuses it unless
// the route opts in with WithLoweredPolicy, which it detects through
// LoweredPolicy. A decorator over this evaluator must forward LoweredPolicy.
type LoweredEvaluator struct {
	checker  sdkauthorization.Checker
	schema   sdkauthorization.SchemaReader
	maxBatch int
	global   *loweredGlobal
}

type loweredGlobal struct {
	resourceType string
	id           string
	relationFor  func(string) string
}

// WithGlobalResource maps HasRole(label, Global()) to the relation
// relationFor(label) on resourceType:id, never to a permission named label.
func WithGlobalResource(resourceType, id string, relationFor func(label string) string) LoweredOption {
	return func(e *LoweredEvaluator) error {
		if relationFor == nil {
			return fmt.Errorf("lowered evaluator: WithGlobalResource needs relationFor: %w", sdk.ErrInvalidInput)
		}
		if err := sdkauthorization.ValidateObject(resourceType, id); err != nil {
			return err
		}
		e.global = &loweredGlobal{resourceType: resourceType, id: id, relationFor: relationFor}
		return nil
	}
}

// NewLoweredEvaluator builds an evaluator over checker. A SchemaReader on
// checker is captured here, so ValidateExpression checks every declaration at
// mount without I/O; a Limiter bounds each speculative batch.
func NewLoweredEvaluator(checker sdkauthorization.Checker, opts ...LoweredOption) (*LoweredEvaluator, error) {
	if checker == nil {
		return nil, fmt.Errorf("lowered evaluator: nil checker: %w", sdk.ErrInvalidInput)
	}
	e := &LoweredEvaluator{checker: checker, maxBatch: defaultLoweredRun}
	if s, ok := checker.(sdkauthorization.SchemaReader); ok {
		e.schema = s
	}
	if l, ok := checker.(sdkauthorization.Limiter); ok {
		if n := l.Limits().MaxCheckBatch; n > 0 {
			e.maxBatch = n
		}
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("lowered evaluator: nil option: %w", sdk.ErrInvalidInput)
		}
		if err := opt(e); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// LoweredPolicy marks this evaluator for Require's opt-in check.
func (e *LoweredEvaluator) LoweredPolicy() bool { return true }

// ValidateExpression checks the tree's structure, the protocol's identifier
// grammar for every name, type and fixed resource, and, when the Checker is a
// SchemaReader, every declaration. It performs no I/O.
func (e *LoweredEvaluator) ValidateExpression(expr Expression) error {
	if err := e.validateLowered(expr); err != nil {
		return err
	}
	_, err := validateExpressionTree(expr, e.declarations(), "", true)
	return err
}

// EvaluateResolved evaluates expr for principal over the Checker.
func (e *LoweredEvaluator) EvaluateResolved(ctx context.Context, principal authmodel.PrincipalRef, expr Expression, resolve ResourceResolver) (authmodel.CheckResult, error) {
	if err := authmodel.ValidatePrincipal(principal); err != nil {
		return authmodel.CheckResult{}, err
	}
	if err := e.validateLowered(expr); err != nil {
		return authmodel.CheckResult{}, err
	}
	hasSlots, err := validateExpressionTree(expr, e.declarations(), "", true)
	if err != nil {
		return authmodel.CheckResult{}, err
	}
	if hasSlots && resolve == nil {
		return authmodel.CheckResult{}, fmt.Errorf("resource resolver required: %w", sdk.ErrInvalidInput)
	}
	w := &loweredWalk{
		e: e, ctx: ctx, principal: principal, resolve: resolve, slots: map[string]*loweredSlot{},
		// A principal the protocol cannot name holds no grant through it.
		nameable: sdkauthorization.ValidateObject(principal.Type, principal.ID) == nil,
	}
	allowed, err := w.node(expr)
	if err != nil {
		return authmodel.CheckResult{}, err
	}
	return decision(allowed, "lowered"), nil
}

func (e *LoweredEvaluator) declarations() expressionDeclarations {
	if e.schema == nil {
		return expressionDeclarations{
			resource:   func(string) bool { return true },
			subjects:   func(string, string) ([]SubjectTypeRef, bool) { return nil, true },
			permission: func(string, string) bool { return true },
		}
	}
	return expressionDeclarations{
		resource: e.schema.DeclaresResource,
		subjects: func(rt, relation string) ([]SubjectTypeRef, bool) {
			return nil, e.schema.DeclaresPermission(rt, relation)
		},
		permission: e.schema.DeclaresPermission,
	}
}

// validateLowered applies what only a lowered policy needs: no Through, the
// identifier grammar, the global resource for a global role, and declarations
// for role labels (which the native validator leaves to the role store).
func (e *LoweredEvaluator) validateLowered(expr Expression) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("lowered expression: "+format+": %w", append(args, sdk.ErrInvalidInput)...)
	}
	declared := func(resourceType, name string) error {
		if e.schema == nil {
			return nil
		}
		if !e.schema.DeclaresResource(resourceType) {
			return invalid("resource type %q is undeclared", resourceType)
		}
		if !e.schema.DeclaresPermission(resourceType, name) {
			return invalid("%s#%s is not a declared permission or relation", resourceType, name)
		}
		return nil
	}
	nodes := 0
	var visit func(Expression, int) error
	visit = func(x Expression, depth int) error {
		nodes++
		if depth > MaxExpressionDepth || nodes > MaxExpressionNodes {
			return authmodel.ErrEvaluationLimit
		}
		if x.Through != "" {
			return invalid("Through is a model construct and cannot be lowered; name the permission that uses it")
		}
		if children := groupChildren(x); children != nil {
			for _, child := range children {
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		name := loweredName(x)
		if name == "" {
			return nil
		}
		if err := sdkauthorization.ValidateRef(sdkauthorization.KindRelation, name); err != nil {
			return err
		}
		switch {
		case x.Resource != nil:
			if err := sdkauthorization.ValidateObject(x.Resource.Type, x.Resource.ID); err != nil {
				return err
			}
			return declared(x.Resource.Type, name)
		case x.ResourceSlot != nil:
			if err := sdkauthorization.ValidateRef(sdkauthorization.KindType, x.ResourceSlot.Type); err != nil {
				return err
			}
			return declared(x.ResourceSlot.Type, name)
		case x.RoleName != "" && !x.CurrentResource:
			g := e.global
			if g == nil {
				return invalid("HasRole(%q, Global()) needs WithGlobalResource", x.RoleName)
			}
			relation := g.relationFor(x.RoleName)
			if err := sdkauthorization.ValidateRef(sdkauthorization.KindRelation, relation); err != nil {
				return err
			}
			return declared(g.resourceType, relation)
		}
		return nil
	}
	return visit(expr, 0)
}

func groupChildren(x Expression) []Expression {
	if x.AllOf != nil {
		return x.AllOf
	}
	return x.AnyOf
}

func loweredName(x Expression) string {
	switch {
	case x.RoleName != "":
		return x.RoleName
	case x.Relation != "":
		return x.Relation
	}
	return x.NamedPermission
}

// loweredSlot is one memoized input outcome.
type loweredSlot struct {
	resource authmodel.Resource
	err      error
	// skip makes the leaf false: the input does not apply, or the protocol
	// cannot name it (no grant can exist on it through the Checker).
	skip bool
}

type loweredWalk struct {
	e         *LoweredEvaluator
	ctx       context.Context
	principal authmodel.PrincipalRef
	resolve   ResourceResolver
	slots     map[string]*loweredSlot
	nameable  bool
}

// leafState is one leaf before its Check.
type leafState struct {
	req   sdkauthorization.CheckRequest
	ready bool  // req must be checked
	err   error // held; surfaces only if the leaf is reached
}

func (w *loweredWalk) node(x Expression) (bool, error) {
	if err := w.ctx.Err(); err != nil {
		return false, err
	}
	if x.AllOf != nil {
		return w.group(x.AllOf, true)
	}
	if x.AnyOf != nil {
		return w.group(x.AnyOf, false)
	}
	return w.group([]Expression{x}, false)
}

// group walks children in declaration order with the native short circuit.
func (w *loweredWalk) group(children []Expression, all bool) (bool, error) {
	for i := 0; i < len(children); {
		if !isLeaf(children[i]) {
			result, err := w.node(children[i])
			if err != nil {
				return false, err
			}
			if result != all {
				return result, nil
			}
			i++
			continue
		}
		// The first leaf is reached, so resolving its input is legal.
		w.prepare(children[i], true)
		j := i + 1
		for j < len(children) && j-i < w.e.maxBatch && isLeaf(children[j]) && w.prepare(children[j], false) {
			j++
		}
		decided, result, err := w.run(children[i:j], all)
		if err != nil || decided {
			return result, err
		}
		i = j
	}
	return all, nil
}

func isLeaf(x Expression) bool { return x.AnyOf == nil && x.AllOf == nil }

// prepare reports whether a leaf's resource is known now. It resolves the
// leaf's input when it is reached or pure, never a non-pure unreached one.
func (w *loweredWalk) prepare(x Expression, reached bool) bool {
	slot := x.ResourceSlot
	if slot == nil {
		return true
	}
	if _, done := w.slots[slot.Key]; done {
		return true
	}
	if !reached && !slot.Pure {
		return false
	}
	w.resolveSlot(*slot)
	return true
}

func (w *loweredWalk) resolveSlot(slot ResourceSlot) {
	out := &loweredSlot{}
	w.slots[slot.Key] = out
	if w.resolve == nil {
		out.err = fmt.Errorf("resource resolver required: %w", sdk.ErrInvalidInput)
		return
	}
	resource, err := w.resolve(w.ctx, slot.Key)
	switch {
	case errors.Is(err, ErrResourceNotApplicable):
		out.skip = true
	case err != nil:
		out.err = err
	default:
		// What the pocket grammar rejects is an error, exactly as natively.
		if err := tuples.On(resource.Type, resource.ID).Validate(); err != nil {
			out.err = err
			return
		}
		if resource.Type != slot.Type {
			out.err = fmt.Errorf("resource slot %q resolved type %q, want %q: %w", slot.Key, resource.Type, slot.Type, sdk.ErrInvalidInput)
			return
		}
		out.resource = resource
		out.skip = sdkauthorization.ValidateObject(resource.Type, resource.ID) != nil
	}
}

func (w *loweredWalk) state(x Expression) leafState {
	var slot *loweredSlot
	if x.ResourceSlot != nil {
		slot = w.slots[x.ResourceSlot.Key]
		if slot.err != nil {
			return leafState{err: slot.err}
		}
		if slot.skip {
			return leafState{}
		}
	}
	if !w.nameable {
		return leafState{}
	}
	name := loweredName(x)
	var resource sdkauthorization.Resource
	switch {
	case x.Resource != nil:
		resource = *x.Resource
	case slot != nil:
		resource = slot.resource
	default:
		g := w.e.global
		name, resource = g.relationFor(x.RoleName), sdkauthorization.Resource{Type: g.resourceType, ID: g.id}
	}
	return leafState{ready: true, req: sdkauthorization.CheckRequest{Principal: w.principal, Permission: name, Resource: resource}}
}

// run decides a run of leaves. decided reports a short circuit; only a
// reached leaf surfaces an error.
func (w *loweredWalk) run(leaves []Expression, all bool) (decided, result bool, err error) {
	states := make([]leafState, len(leaves))
	var reqs []sdkauthorization.CheckRequest
	var index []int
	for k, leaf := range leaves {
		states[k] = w.state(leaf)
		if states[k].ready {
			reqs = append(reqs, states[k].req)
			index = append(index, k)
		}
	}
	answers := make(map[int]bool, len(reqs))
	batched := false
	if len(reqs) > 1 {
		if got, err := w.e.checker.CheckBatch(w.ctx, reqs); err == nil && len(got) == len(reqs) {
			for n, k := range index {
				answers[k] = got[n].Allowed
			}
			batched = true
		}
	}
	for k, s := range states {
		allowed := false
		switch {
		case s.err != nil:
			return true, false, s.err
		case !s.ready:
		case batched:
			allowed = answers[k]
		default:
			d, err := w.e.checker.Check(w.ctx, s.req)
			if err != nil {
				return true, false, classifyLowered(err)
			}
			allowed = d.Allowed
		}
		if allowed != all {
			return true, allowed, nil
		}
	}
	return false, all, nil
}

// classifyLowered keeps a Checker's invalid-input rejection server-class.
func classifyLowered(err error) error {
	if errors.Is(err, sdk.ErrInvalidInput) {
		return fmt.Errorf("%w: %v", ErrLoweredPolicyMisconfigured, err)
	}
	return err
}

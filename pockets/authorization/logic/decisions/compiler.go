package decisions

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/sdk"
)

// ModelEncodingVersion identifies the canonical schema encoding hashed into a
// digest. The digest hashes this prefix plus the canonical bytes, so bumping it
// changes every digest — a deliberate, visible break. It is published beside the
// digest (CompiledModel.EncodingVersion, ModelSnapshot.EncodingVersion) so a
// stored digest can be interpreted against the encoding that produced it.
const ModelEncodingVersion = "gopernicus.authorization.model/2"

// relationKind classifies a compiled relation by its use in the schema. It is a
// resolved property folded into the canonical encoding: a relation referenced by
// any Through traversal is navigational (it must carry concrete resource
// subjects only); every other relation is a direct-subject relation.
type relationKind uint8

const (
	relationDirect relationKind = iota
	relationNavigational
)

// ModelCompileError aggregates every structural error found while compiling a
// schema. Its Errors slice is sorted and de-duplicated deterministically, so the
// same invalid schema always reports the same ordered list. It wraps
// sdk.ErrInvalidInput, so callers may classify it with errors.Is.
type ModelCompileError struct {
	Errors []string
}

func (e *ModelCompileError) Error() string {
	return fmt.Sprintf("schema compilation failed with %d error(s):\n  - %s",
		len(e.Errors), strings.Join(e.Errors, "\n  - "))
}

func (e *ModelCompileError) Unwrap() error { return sdk.ErrInvalidInput }

// compiledResourceType is the immutable, deep-copied compilation of one resource
// type. Subject declarations and Through target types are canonical sets;
// permission expressions retain their order. All values are owned copies of
// the source Model.
type compiledResourceType struct {
	relations   map[string]compiledRelation
	permissions map[string]compiledPermission

	// relationTargets maps a relation to the sorted concrete resource-target type
	// names among its subjects — the compiled Through target definitions the
	// lookup engine walks.
	relationTargets map[string][]string
}

type compiledRelation struct {
	subjects []SubjectTypeRef // sorted, duplicate-free
	kind     relationKind
}

type compiledPermission struct {
	checks     []Expression
	expression Expression
}

// CompiledModel is the immutable, strictly-validated compilation of a source
// Model, identified by one stable digest. It is built by Compile, which
// deep-copies every source map and slice, so a later mutation of the caller's
// Model cannot alter a compiled schema, its digest, or any decision derived
// from it. Its internal maps are never exposed: callers read policy through
// Snapshot and identity through Digest / EncodingVersion.
type CompiledModel struct {
	resourceTypes map[string]compiledResourceType
	digest        string
}

// Compile strictly validates a source Model and returns its immutable
// compilation plus a deterministic digest, or a *ModelCompileError listing
// every structural problem. Names follow tuples.ValidateRefField, the
// same opaque reference contract used by requests. It rejects empty permission rules,
// duplicate declarations, ambiguous checks (both or neither of Direct/Through),
// unknown direct relations, unknown userset relations, userset targets on a
// navigational (Through) relation, Through permissions absent from every
// possible target, genuine cycles, and globally unsatisfiable permission graphs,
// while preserving the sanctioned self-referential hierarchy shape.
//
// The returned CompiledModel shares no memory with schema.
func Compile(schema Model) (*CompiledModel, error) {
	errs := append([]string(nil), schema.assemblyErrors...)
	invalidExpression := false
	for rt, definition := range schema.ResourceTypes {
		if err := tuples.ValidateRefField("resource type name", rt); err != nil {
			errs = append(errs, err.Error())
		}
		for name := range definition.Relations {
			if err := tuples.ValidateRefField("relation name", name); err != nil {
				errs = append(errs, err.Error())
			}
		}
		for name, expression := range definition.Permissions {
			if err := tuples.ValidateRefField("permission name", name); err != nil {
				errs = append(errs, err.Error())
			}
			if err := validateExpression(expression, schema, rt, false); err != nil {
				invalidExpression = true
				errs = append(errs, fmt.Sprintf("%s.%s: %v", rt, name, err))
			}
		}
	}
	if invalidExpression {
		return nil, &ModelCompileError{Errors: dedupeSortStrings(errs)}
	}

	// A relation is navigational if any Through traversal on its own resource
	// type references it. Classification is by compiled use, not declaration.
	navRefs := make(map[[2]string]bool)
	for rtName, rt := range schema.ResourceTypes {
		for _, rule := range rt.Permissions {
			for _, chk := range expressionLeaves(rule) {
				if chk.Through != "" {
					navRefs[[2]string{rtName, chk.Through}] = true
				}
			}
		}
	}

	for rtName, rt := range schema.ResourceTypes {
		if err := tuples.ValidateRefField("resource type name", rtName); err != nil {
			errs = append(errs, err.Error())
		}
		errs = append(errs, compileRelations(schema, rtName, rt, navRefs)...)
		errs = append(errs, compilePermissions(schema, rtName, rt)...)
	}

	errs = append(errs, detectCircularThrough(schema)...)
	errs = append(errs, detectUnsatisfiable(schema)...)

	errs = dedupeSortStrings(errs)
	if len(errs) > 0 {
		return nil, &ModelCompileError{Errors: errs}
	}

	isResourceType := make(map[string]bool, len(schema.ResourceTypes))
	for name := range schema.ResourceTypes {
		isResourceType[name] = true
	}

	compiled := make(map[string]compiledResourceType, len(schema.ResourceTypes))
	for rtName, rt := range schema.ResourceTypes {
		crt := compiledResourceType{
			relations:   make(map[string]compiledRelation, len(rt.Relations)),
			permissions: make(map[string]compiledPermission, len(rt.Permissions)),
		}
		for relName, rel := range rt.Relations {
			kind := relationDirect
			if navRefs[[2]string{rtName, relName}] {
				kind = relationNavigational
			}
			crt.relations[relName] = compiledRelation{
				subjects: dedupeSortSubjects(rel.AllowedSubjects),
				kind:     kind,
			}
		}
		for permName, rule := range rt.Permissions {
			crt.permissions[permName] = compiledPermission{checks: graphChecks(copyExpression(rule)), expression: copyExpression(rule)}
		}
		crt.relationTargets = buildRelationTargets(crt.relations, isResourceType)
		compiled[rtName] = crt
	}

	cs := &CompiledModel{resourceTypes: compiled}
	cs.digest = computeDigest(compiled)
	return cs, nil
}

// Digest returns the schema's stable SHA-256 digest (lowercase hex). Two
// compilations of semantically equal schemas — regardless of source map
// iteration order or duplicate declarations removed by validation — yield the
// same digest; any policy change yields a different one.
func (c *CompiledModel) Digest() string { return c.digest }

// EncodingVersion returns the canonical encoding version the digest was computed
// under (ModelEncodingVersion), published so a stored digest is interpretable.
func (c *CompiledModel) EncodingVersion() string { return ModelEncodingVersion }

// =============================================================================
// Runtime accessors — the engine reads policy through these immutable, sorted
// projections, never by ranging a source (mutable) map.
// =============================================================================

// permissionChecks returns a flat graph projection when the permission can
// use graph batch/lookup optimizations. Mixed and nested expressions return nil.
// The returned slice preserves expression order and is never mutated in place.
func (c *CompiledModel) permissionChecks(resourceType, permission string) []Expression {
	rt, ok := c.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return rt.permissions[permission].checks
}

// declaresPermission reports whether the compiled model declares permission on
// resourceType. Expression validation uses it before evaluating named leaves.
func (c *CompiledModel) declaresPermission(resourceType, permission string) bool {
	rt, ok := c.resourceTypes[resourceType]
	if !ok {
		return false
	}
	_, ok = rt.permissions[permission]
	return ok
}

// relationSubjects returns a relation's compiled allowed subjects. resourceTypeOK
// reports whether the resource type is declared and relationOK whether the
// relation exists on it, so a caller can name the exact gap.
func (c *CompiledModel) relationSubjects(resourceType, relation string) (subjects []SubjectTypeRef, resourceTypeOK, relationOK bool) {
	rt, ok := c.resourceTypes[resourceType]
	if !ok {
		return nil, false, false
	}
	rel, ok := rt.relations[relation]
	if !ok {
		return nil, true, false
	}
	return rel.subjects, true, true
}

// relationResourceTargets returns the precomputed sorted concrete resource-target
// type names of a relation — the compiled Through target definitions the lookup
// engine walks. It returns the stored slice as the engine's read-only view.
func (c *CompiledModel) relationResourceTargets(resourceType, relation string) []string {
	rt, ok := c.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return rt.relationTargets[relation]
}

// buildRelationTargets indexes each relation to the sorted concrete
// resource-target type names among its subjects (a subject that is a declared
// resource type carried as a concrete reference, not a userset).
func buildRelationTargets(relations map[string]compiledRelation, isResourceType map[string]bool) map[string][]string {
	idx := make(map[string][]string)
	for relName, rel := range relations {
		var targets []string
		for _, sub := range rel.subjects {
			if sub.Relation == "" && isResourceType[sub.Type] {
				targets = append(targets, sub.Type)
			}
		}
		if len(targets) > 0 {
			idx[relName] = dedupeSortStrings(targets)
		}
	}
	return idx
}

// =============================================================================
// Strict validation passes
// =============================================================================

func compileRelations(schema Model, rtName string, rt ResourceTypeDef, navRefs map[[2]string]bool) []string {
	var errs []string
	for relName, rel := range rt.Relations {
		if err := tuples.ValidateRefField("relation name", relName); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", rtName, err))
		}
		if len(rel.AllowedSubjects) == 0 {
			errs = append(errs, fmt.Sprintf("%s.%s: relation has no allowed subjects", rtName, relName))
		}
		navigational := navRefs[[2]string{rtName, relName}]

		seen := make(map[SubjectTypeRef]bool, len(rel.AllowedSubjects))
		for _, sub := range rel.AllowedSubjects {
			if err := tuples.ValidateRefField("allowed subject type", sub.Type); err != nil {
				errs = append(errs, fmt.Sprintf("%s.%s: %s", rtName, relName, err))
				continue
			}
			if sub.Relation != "" {
				if err := tuples.ValidateRefField("allowed subject relation", sub.Relation); err != nil {
					errs = append(errs, fmt.Sprintf("%s.%s: %s", rtName, relName, err))
				}
			}
			if seen[sub] {
				errs = append(errs, fmt.Sprintf("%s.%s: duplicate allowed subject %s", rtName, relName, subjectString(sub)))
			}
			seen[sub] = true

			if sub.Relation != "" {
				// A userset subject Type:...#Relation must reference a declared
				// resource type whose Relation exists — meaningful as a userset.
				tdef, ok := schema.ResourceTypes[sub.Type]
				if !ok {
					errs = append(errs, fmt.Sprintf("%s.%s: userset subject %s references unknown resource type %q",
						rtName, relName, subjectString(sub), sub.Type))
				} else if _, ok := tdef.Relations[sub.Relation]; !ok {
					errs = append(errs, fmt.Sprintf("%s.%s: userset subject %s references unknown relation %q on %q",
						rtName, relName, subjectString(sub), sub.Relation, sub.Type))
				}
			}

			if navigational {
				// A relation used by a Through traversal must contain concrete
				// resource subjects only; v3 never traverses into a userset.
				if sub.Relation != "" {
					errs = append(errs, fmt.Sprintf("%s.%s: relation is used by a Through traversal and must contain concrete resource subjects only, but allows userset %s",
						rtName, relName, subjectString(sub)))
				} else if _, ok := schema.ResourceTypes[sub.Type]; !ok {
					errs = append(errs, fmt.Sprintf("%s.%s: relation is used by a Through traversal and must target a resource type, but allows %q",
						rtName, relName, sub.Type))
				}
			}
		}
	}
	return errs
}

func compilePermissions(schema Model, rtName string, rt ResourceTypeDef) []string {
	var errs []string
	for name, expr := range rt.Permissions {
		if err := tuples.ValidateRefField("permission name", name); err != nil {
			errs = append(errs, err.Error())
		}
		if err := validateExpression(expr, schema, rtName, false); err != nil {
			errs = append(errs, fmt.Sprintf("%s.%s: %v", rtName, name, err))
		}
	}
	return errs
}

// =============================================================================
// Canonical encoding + digest
// =============================================================================

// computeDigest streams the version prefix and the canonical bytes of the
// compiled schema into SHA-256. The encoding is length-prefixed so opaque names
// (which may legally contain any non-control rune, including delimiters) cannot
// alias across field boundaries; every collection is emitted in sorted order so
// the digest is independent of source map iteration order.
func computeDigest(rts map[string]compiledResourceType) string {
	h := sha256.New()
	io.WriteString(h, ModelEncodingVersion)
	h.Write([]byte{0}) // separate the version prefix from the canonical bytes

	rtNames := sortedMapKeys(rts)
	writeUint(h, len(rtNames))
	for _, rtName := range rtNames {
		writeString(h, rtName)
		rt := rts[rtName]

		relNames := sortedMapKeys(rt.relations)
		writeUint(h, len(relNames))
		for _, relName := range relNames {
			rel := rt.relations[relName]
			writeString(h, relName)
			writeUint(h, int(rel.kind))
			writeUint(h, len(rel.subjects))
			for _, sub := range rel.subjects {
				writeString(h, sub.Type)
				writeString(h, sub.Relation)
			}
		}

		permNames := sortedMapKeys(rt.permissions)
		writeUint(h, len(permNames))
		for _, permName := range permNames {
			perm := rt.permissions[permName]
			writeString(h, permName)
			encoded, _ := json.Marshal(perm.expression)
			writeString(h, string(encoded))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeString(h io.Writer, s string) {
	writeUint(h, len(s))
	io.WriteString(h, s)
}

func writeUint(h io.Writer, v int) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	h.Write(b[:])
}

// =============================================================================
// Deterministic helpers
// =============================================================================

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func dedupeSortStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	j := 0
	for i := 1; i < len(out); i++ {
		if out[i] != out[j] {
			j++
			out[j] = out[i]
		}
	}
	return out[:j+1]
}

// dedupeSortSubjects sorts AllowedSubjects into a canonical, duplicate-free set.
// Exact duplicates are rejected during validation; the dedup here defends the
// encoding contract's "duplicate-free sorted semantic set" independently.
func dedupeSortSubjects(in []SubjectTypeRef) []SubjectTypeRef {
	if len(in) == 0 {
		return nil
	}
	out := append([]SubjectTypeRef(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Relation < out[j].Relation
	})
	j := 0
	for i := 1; i < len(out); i++ {
		if out[i] != out[j] {
			j++
			out[j] = out[i]
		}
	}
	return out[:j+1]
}

func subjectString(s SubjectTypeRef) string {
	if s.Relation == "" {
		return s.Type
	}
	return s.Type + "#" + s.Relation
}

func (c *CompiledModel) ReadModel() ReadModel { return c.readModel() }
func (c *CompiledModel) DeclaresPermission(rt, permission string) bool {
	return c.declaresPermission(rt, permission)
}
func (c *CompiledModel) expression(rt, permission string) Expression {
	return c.resourceTypes[rt].permissions[permission].expression
}

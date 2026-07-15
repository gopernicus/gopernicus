package authorizersvc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// SchemaEncodingVersion identifies the canonical schema encoding hashed into a
// digest. The digest hashes this prefix plus the canonical bytes, so bumping it
// changes every digest — a deliberate, visible break. It is published beside the
// digest (CompiledSchema.EncodingVersion, SchemaSnapshot.EncodingVersion) so a
// stored digest can be interpreted against the encoding that produced it.
const SchemaEncodingVersion = "gopernicus.authorization.schema/1"

// relationKind classifies a compiled relation by its use in the schema. It is a
// resolved property folded into the canonical encoding: a relation referenced by
// any Through traversal is navigational (it must carry concrete resource
// subjects only); every other relation is a direct-subject relation.
type relationKind uint8

const (
	relationDirect relationKind = iota
	relationNavigational
)

// SchemaCompileError aggregates every structural error found while compiling a
// schema. Its Errors slice is sorted and de-duplicated deterministically, so the
// same invalid schema always reports the same ordered list. It wraps
// sdk.ErrInvalidInput, so callers may classify it with errors.Is.
type SchemaCompileError struct {
	Errors []string
}

func (e *SchemaCompileError) Error() string {
	return fmt.Sprintf("schema compilation failed with %d error(s):\n  - %s",
		len(e.Errors), strings.Join(e.Errors, "\n  - "))
}

func (e *SchemaCompileError) Unwrap() error { return sdk.ErrInvalidInput }

// compiledResourceType is the immutable, deep-copied compilation of one resource
// type. Its slices are sorted, de-duplicated, and share no memory with the
// source Schema. relationPermissions and relationTargets are precomputed sorted
// indexes derived from relations/permissions, so the runtime resolves reverse
// lookups and Through targets without ranging any (mutable) source map.
type compiledResourceType struct {
	relations   map[string]compiledRelation
	permissions map[string]compiledPermission

	// relationPermissions maps a relation to the sorted permission names that
	// grant it via a Direct check — the reverse index behind
	// GetPermissionsForRelation.
	relationPermissions map[string][]string
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
	checks []PermissionCheck // sorted, duplicate-free
}

// CompiledSchema is the immutable, strictly-validated compilation of a source
// Schema, identified by one stable digest. It is built by Compile, which
// deep-copies every source map and slice, so a later mutation of the caller's
// Schema cannot alter a compiled schema, its digest, or any decision derived
// from it. Its internal maps are never exposed: callers read policy through
// Snapshot and identity through Digest / EncodingVersion.
type CompiledSchema struct {
	resourceTypes map[string]compiledResourceType
	digest        string
}

// Compile strictly validates a source Schema and returns its immutable
// compilation plus a deterministic digest, or a *SchemaCompileError listing
// every structural problem. It rejects empty names, empty permission rules,
// duplicate declarations, ambiguous checks (both or neither of Direct/Through),
// unknown direct relations, unknown userset relations, userset targets on a
// navigational (Through) relation, Through permissions absent from every
// possible target, genuine cycles, and globally unsatisfiable permission graphs,
// while preserving the sanctioned self-referential hierarchy shape.
//
// The returned CompiledSchema shares no memory with schema.
func Compile(schema Schema) (*CompiledSchema, error) {
	var errs []string

	if len(schema.ResourceTypes) == 0 {
		errs = append(errs, "schema declares no resource types")
	}

	// A relation is navigational if any Through traversal on its own resource
	// type references it. Classification is by compiled use, not declaration.
	navRefs := make(map[[2]string]bool)
	for rtName, rt := range schema.ResourceTypes {
		for _, rule := range rt.Permissions {
			for _, chk := range rule.AnyOf {
				if chk.Through != "" {
					navRefs[[2]string{rtName, chk.Through}] = true
				}
			}
		}
	}

	for rtName, rt := range schema.ResourceTypes {
		if rtName == "" {
			errs = append(errs, "resource type name must not be empty")
		}
		errs = append(errs, compileRelations(schema, rtName, rt, navRefs)...)
		errs = append(errs, compilePermissions(schema, rtName, rt)...)
	}

	errs = append(errs, detectCircularThrough(schema)...)
	errs = append(errs, detectUnsatisfiable(schema)...)

	errs = dedupeSortStrings(errs)
	if len(errs) > 0 {
		return nil, &SchemaCompileError{Errors: errs}
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
			crt.permissions[permName] = compiledPermission{checks: dedupeSortChecks(rule.AnyOf)}
		}
		crt.relationPermissions = buildRelationPermissions(crt.permissions)
		crt.relationTargets = buildRelationTargets(crt.relations, isResourceType)
		compiled[rtName] = crt
	}

	cs := &CompiledSchema{resourceTypes: compiled}
	cs.digest = computeDigest(compiled)
	return cs, nil
}

// Digest returns the schema's stable SHA-256 digest (lowercase hex). Two
// compilations of semantically equal schemas — regardless of source map
// iteration order or duplicate declarations removed by validation — yield the
// same digest; any policy change yields a different one.
func (c *CompiledSchema) Digest() string { return c.digest }

// EncodingVersion returns the canonical encoding version the digest was computed
// under (SchemaEncodingVersion), published so a stored digest is interpretable.
func (c *CompiledSchema) EncodingVersion() string { return SchemaEncodingVersion }

// =============================================================================
// Runtime accessors — the engine reads policy through these immutable, sorted
// projections, never by ranging a source (mutable) map.
// =============================================================================

// permissionChecks returns the compiled, sorted, duplicate-free checks of a
// permission on a resource type, or nil if either is undeclared. The returned
// slice is the engine's read-only view; it is never mutated in place.
func (c *CompiledSchema) permissionChecks(resourceType, permission string) []PermissionCheck {
	rt, ok := c.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return rt.permissions[permission].checks
}

// relationSubjects returns a relation's compiled allowed subjects. resourceTypeOK
// reports whether the resource type is declared and relationOK whether the
// relation exists on it, so a caller can name the exact gap.
func (c *CompiledSchema) relationSubjects(resourceType, relation string) (subjects []SubjectTypeRef, resourceTypeOK, relationOK bool) {
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

// permissionsForRelation returns a copy of the precomputed sorted permission
// names a relation grants via a Direct check, or nil if the type is unknown.
func (c *CompiledSchema) permissionsForRelation(resourceType, relation string) []string {
	rt, ok := c.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return append([]string(nil), rt.relationPermissions[relation]...)
}

// relationResourceTargets returns the precomputed sorted concrete resource-target
// type names of a relation — the compiled Through target definitions the lookup
// engine walks. It returns the stored slice as the engine's read-only view.
func (c *CompiledSchema) relationResourceTargets(resourceType, relation string) []string {
	rt, ok := c.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return rt.relationTargets[relation]
}

// buildRelationPermissions inverts the permission→checks map into a
// relation→sorted-permissions index for GetPermissionsForRelation.
func buildRelationPermissions(perms map[string]compiledPermission) map[string][]string {
	idx := make(map[string][]string)
	for permName, perm := range perms {
		for _, chk := range perm.checks {
			if chk.Relation != "" {
				idx[chk.Relation] = append(idx[chk.Relation], permName)
			}
		}
	}
	for rel, names := range idx {
		idx[rel] = dedupeSortStrings(names)
	}
	return idx
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

func compileRelations(schema Schema, rtName string, rt ResourceTypeDef, navRefs map[[2]string]bool) []string {
	var errs []string
	for relName, rel := range rt.Relations {
		if relName == "" {
			errs = append(errs, fmt.Sprintf("%s: relation name must not be empty", rtName))
		}
		if len(rel.AllowedSubjects) == 0 {
			errs = append(errs, fmt.Sprintf("%s.%s: relation has no allowed subjects", rtName, relName))
		}
		navigational := navRefs[[2]string{rtName, relName}]

		seen := make(map[SubjectTypeRef]bool, len(rel.AllowedSubjects))
		for _, sub := range rel.AllowedSubjects {
			if sub.Type == "" {
				errs = append(errs, fmt.Sprintf("%s.%s: allowed subject has an empty type", rtName, relName))
				continue
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

func compilePermissions(schema Schema, rtName string, rt ResourceTypeDef) []string {
	var errs []string
	for permName, rule := range rt.Permissions {
		if permName == "" {
			errs = append(errs, fmt.Sprintf("%s: permission name must not be empty", rtName))
		}
		if len(rule.AnyOf) == 0 {
			errs = append(errs, fmt.Sprintf("%s.%s: permission rule is empty", rtName, permName))
		}

		seen := make(map[PermissionCheck]bool, len(rule.AnyOf))
		for _, chk := range rule.AnyOf {
			if seen[chk] {
				errs = append(errs, fmt.Sprintf("%s.%s: duplicate check %s", rtName, permName, checkString(chk)))
			}
			seen[chk] = true

			hasDirect := chk.Relation != ""
			hasThrough := chk.Through != ""
			switch {
			case hasDirect && hasThrough:
				errs = append(errs, fmt.Sprintf("%s.%s: ambiguous check names both a direct relation %q and a through relation %q",
					rtName, permName, chk.Relation, chk.Through))
			case !hasDirect && !hasThrough:
				errs = append(errs, fmt.Sprintf("%s.%s: empty check names neither a direct relation nor a through traversal", rtName, permName))
			case hasDirect:
				if _, ok := rt.Relations[chk.Relation]; !ok {
					errs = append(errs, fmt.Sprintf("%s.%s: direct relation %q is not defined on %s", rtName, permName, chk.Relation, rtName))
				}
			case hasThrough:
				if chk.Permission == "" {
					errs = append(errs, fmt.Sprintf("%s.%s: through traversal via %q names no permission", rtName, permName, chk.Through))
					continue
				}
				errs = append(errs, validateThrough(schema, rtName, permName, chk, rt)...)
			}
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
	io.WriteString(h, SchemaEncodingVersion)
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
			writeUint(h, len(perm.checks))
			for _, chk := range perm.checks {
				writeString(h, chk.Relation)
				writeString(h, chk.Through)
				writeString(h, chk.Permission)
			}
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

// dedupeSortChecks sorts an AnyOf rule into a canonical, duplicate-free set. As
// with subjects, duplicates are rejected during validation; the dedup defends
// the encoding contract independently.
func dedupeSortChecks(in []PermissionCheck) []PermissionCheck {
	if len(in) == 0 {
		return nil
	}
	out := append([]PermissionCheck(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Relation != out[j].Relation {
			return out[i].Relation < out[j].Relation
		}
		if out[i].Through != out[j].Through {
			return out[i].Through < out[j].Through
		}
		return out[i].Permission < out[j].Permission
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

func checkString(c PermissionCheck) string {
	if c.Through != "" {
		return "through:" + c.Through + "#" + c.Permission
	}
	return "direct:" + c.Relation
}

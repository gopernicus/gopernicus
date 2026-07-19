package primitives

import "github.com/a-h/templ"

// classNames joins a primitive's stable base class with an optional caller Class
// (Base.Class). The stable class always comes first so callers append utilities
// without dropping the compatibility class.
func classNames(base, extra string) string {
	if extra == "" {
		return base
	}
	return base + " " + extra
}

// templAttrsWith returns a copy of caller with key set to value, never mutating the
// caller's map. It is used by facade primitives (Sonner) to add a presentational
// data-* marker onto the props they forward to the primitive they compose.
func templAttrsWith(caller templ.Attributes, key, value string) templ.Attributes {
	out := templ.Attributes{}
	for k, v := range caller {
		out[k] = v
	}
	out[key] = value
	return out
}

// ownedAttrs merges a primitive's behavior-critical owned attributes over the
// caller's Base.Attributes through the single documented MergeAttributes order,
// folding Base.ID into the owned set so a caller-supplied stable id is honored
// while the primitive keeps ownership of data-slot/role/state hooks. The result
// is applied as ONE spread on the element (README §7).
func ownedAttrs(b Base, owned templ.Attributes) templ.Attributes {
	if owned == nil {
		owned = templ.Attributes{}
	}
	if b.ID != "" {
		if _, set := owned["id"]; !set {
			owned["id"] = b.ID
		}
	}
	return MergeAttributes(b.Attributes, owned)
}

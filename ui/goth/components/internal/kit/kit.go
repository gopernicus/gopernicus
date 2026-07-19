// Package kit holds the shared class/attribute helpers every ui/goth component
// package reuses. It exists so the four component packages (layouts, forms,
// feedback, data) apply the SAME frozen grammar as the primitives package —
// Base.Class appended after the stable component class, and Base.Attributes
// funnelled through primitives.MergeAttributes so a component's owned
// behavior-critical keys always win in one merged spread. It imports only the
// primitives package and templ; it is internal to ui/goth/components and never a
// public surface.
package kit

import (
	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// Class joins a component's stable base class with an optional caller Class
// (Base.Class). The stable class always comes first so callers append utilities
// without dropping the compatibility class — the same rule the primitives use.
func Class(base primitives.Base, stable string) string {
	if base.Class == "" {
		return stable
	}
	return stable + " " + base.Class
}

// RootAttrs merges a component's behavior-critical owned attributes over the
// caller's Base.Attributes through the single documented primitives.MergeAttributes
// order, folding Base.ID into the owned set so a caller-supplied stable id is
// honored while the component keeps ownership of data-slot/role/state hooks. The
// result is applied as ONE spread on the element (README §7).
func RootAttrs(base primitives.Base, owned templ.Attributes) templ.Attributes {
	if owned == nil {
		owned = templ.Attributes{}
	}
	if base.ID != "" {
		if _, set := owned["id"]; !set {
			owned["id"] = base.ID
		}
	}
	return primitives.MergeAttributes(base.Attributes, owned)
}

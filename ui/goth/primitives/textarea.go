package primitives

import (
	"strconv"

	"github.com/a-h/templ"
)

// TextareaProps configures Textarea (P25, family F1): a native <textarea>. The
// zero value is a valid empty textarea. Its value is the element's text content
// (native), so it submits in an ordinary HTML form with no JavaScript. Autosize
// is an explicit enhancement boundary: the primitive stays a plain resizable
// native control; a host may attach an Alpine controller later without changing
// this markup. data-slot="textarea" and, when invalid, data-invalid/aria-invalid
// are the stable emitted surface.
type TextareaProps struct {
	Base
	Name        string
	Value       string
	Placeholder string
	// Rows is the native visible row count; zero omits the attribute (UA default).
	Rows     int
	Required bool
	Disabled bool
	Readonly bool
	// Invalid marks the field invalid (data-invalid + aria-invalid="true"); wire
	// the describing message with aria-describedby via Base.Attributes (see Field).
	Invalid bool
}

func textareaClass(p TextareaProps) string { return classNames("goth-textarea", p.Class) }

func textareaAttrs(p TextareaProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "textarea"}
	if p.Name != "" {
		owned["name"] = p.Name
	}
	if p.Placeholder != "" {
		owned["placeholder"] = p.Placeholder
	}
	if p.Rows > 0 {
		owned["rows"] = strconv.Itoa(p.Rows)
	}
	if p.Required {
		owned["required"] = true
	}
	if p.Disabled {
		owned["disabled"] = true
	}
	if p.Readonly {
		owned["readonly"] = true
	}
	if p.Invalid {
		owned["aria-invalid"] = "true"
		owned["data-invalid"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

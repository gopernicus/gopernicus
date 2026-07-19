// Package feedback holds the opinionated, domain-neutral status compositions
// built from ui/goth primitives (GOTH-7.1): the empty-state, loading, and error
// panels a list/detail page swaps between, plus the destructive-confirmation
// workflow. EmptyPanel/LoadingPanel/ErrorPanel share one bordered surface and
// compose primitives.Empty/Spinner; ConfirmDialog composes the gothDialog-backed
// primitives.AlertDialog family with NO new controller. None imports a feature
// domain, adds a primitive, or emits a server-rendered style.
package feedback

import (
	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/components/internal/kit"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// EmptyPanelProps configures EmptyPanel, a bordered empty-state surface composing
// primitives.Empty: an optional Media glyph, a Title, a Description, and an
// optional Action (e.g. a create button). The zero value renders a blank panel.
// data-slot hooks: feedback-panel (with data-tone="empty") plus the composed
// empty slots.
type EmptyPanelProps struct {
	primitives.Base
	// Media is an optional leading illustration/icon slot. nil omits it.
	Media templ.Component
	// Title is the short empty-state heading.
	Title string
	// Description is the supporting explanation.
	Description string
	// Action is an optional trailing action slot (e.g. a button). nil omits it.
	Action templ.Component
}

// LoadingPanelProps configures LoadingPanel, a centered busy surface composing
// primitives.Spinner with a label and optional description. The spinner is a
// named live status by default (Spinner owns role="status"); an external live
// region can suppress it via Decorative. The zero value renders a spinner
// labelled "Loading". data-slot hooks: feedback-panel (with data-tone="loading"),
// feedback-panel-title, feedback-panel-description.
type LoadingPanelProps struct {
	primitives.Base
	// Label is the visible/accessible busy message; empty uses "Loading".
	Label string
	// Description is optional supporting text under the label. Empty omits it.
	Description string
	// Decorative hides the spinner from assistive tech when an external live
	// region already announces the busy state.
	Decorative bool
}

func (p LoadingPanelProps) label() string {
	if p.Label != "" {
		return p.Label
	}
	return "Loading"
}

// ErrorPanelProps configures ErrorPanel, a bordered error-state surface composing
// primitives.Empty with a destructive tone: an optional Media glyph, a Title, a
// Description, and an optional Action (e.g. a retry button). It is a role="alert"
// region so assistive tech announces the failure. The zero value renders a bare
// error panel. data-slot hooks: feedback-panel (with data-tone="error").
type ErrorPanelProps struct {
	primitives.Base
	// Media is an optional leading error glyph. nil omits it.
	Media templ.Component
	// Title is the short error heading; empty uses "Something went wrong".
	Title string
	// Description is the supporting explanation.
	Description string
	// Action is an optional trailing action slot (e.g. a retry button). nil omits
	// it.
	Action templ.Component
}

func (p ErrorPanelProps) title() string {
	if p.Title != "" {
		return p.Title
	}
	return "Something went wrong"
}

// ConfirmDialogProps configures ConfirmDialog, the destructive-confirmation
// workflow composing the primitives.AlertDialog family: a trigger button
// (TriggerLabel), a titled/described modal, and a footer with a cancel button and
// a confirm button that submits a form to Action. It reuses the frozen gothDialog
// controller (no new controller). Base.ID is REQUIRED — it seeds the deterministic
// title/description ids the panel references for its accessible name/description.
//
// The confirm button submits an inline <form> (method FormMethod, action Action),
// so the mutation is server-owned and works progressively; add hx-* through
// ConfirmAttributes for an HTMX-enhanced swap. Destructive styles the confirm
// button as destructive (the common case; it defaults true).
type ConfirmDialogProps struct {
	primitives.Base
	// TriggerLabel is the visible text of the button that opens the dialog.
	TriggerLabel string
	// Title is the modal heading (e.g. "Delete project?").
	Title string
	// Description explains the consequence of confirming.
	Description string
	// ConfirmLabel is the confirm button text; empty uses "Confirm".
	ConfirmLabel string
	// CancelLabel is the cancel button text; empty uses "Cancel".
	CancelLabel string
	// Action is the form action URL the confirm button submits to.
	Action primitives.URL
	// Method is the form method (default "post").
	Method string
	// Destructive styles the confirm button destructive. It defaults true (the
	// dominant confirm-workflow case); set NonDestructive to opt out.
	NonDestructive bool
	// Open renders the dialog server-open (readable with no JavaScript).
	Open bool
	// ConfirmAttributes are extra attributes (e.g. explicit hx-*) merged onto the
	// confirm button so a host can enhance the submit without forking the workflow.
	ConfirmAttributes templ.Attributes
	// FormAttributes are extra attributes merged onto the inline <form> (e.g. a
	// CSRF hidden field is a child; hx-* belongs here or on the confirm button).
	FormAttributes templ.Attributes
}

func (p ConfirmDialogProps) confirmLabel() string {
	if p.ConfirmLabel != "" {
		return p.ConfirmLabel
	}
	return "Confirm"
}

func (p ConfirmDialogProps) cancelLabel() string {
	if p.CancelLabel != "" {
		return p.CancelLabel
	}
	return "Cancel"
}

func (p ConfirmDialogProps) method() string {
	if p.Method != "" {
		return p.Method
	}
	return "post"
}

func (p ConfirmDialogProps) titleID() string {
	if p.ID == "" {
		return ""
	}
	return p.ID + "-title"
}

func (p ConfirmDialogProps) descriptionID() string {
	if p.ID == "" {
		return ""
	}
	return p.ID + "-description"
}

// confirmAttrs returns the confirm button's Base.Attributes: a destructive
// data-variant (unless opted out) merged with the caller's ConfirmAttributes
// (e.g. explicit hx-*). AlertDialogAction owns the data-slot and dismiss handler
// and wins the merge.
func (p ConfirmDialogProps) confirmAttrs() templ.Attributes {
	out := templ.Attributes{}
	for k, v := range p.ConfirmAttributes {
		out[k] = v
	}
	if !p.NonDestructive {
		if _, set := out["data-variant"]; !set {
			out["data-variant"] = "destructive"
		}
	}
	return out
}

// confirmFormAttrs returns the inline <form>'s attributes as ONE merged spread:
// the native method/action plus the caller's FormAttributes (owned method/action
// win so the workflow's submit target cannot be silently dropped).
func (p ConfirmDialogProps) confirmFormAttrs() templ.Attributes {
	out := templ.Attributes{}
	for k, v := range p.FormAttributes {
		out[k] = v
	}
	out["method"] = p.method()
	out["action"] = p.Action.String()
	out["data-slot"] = "confirm-form"
	return out
}

func emptyPanelAttrs(p EmptyPanelProps) templ.Attributes {
	return kit.RootAttrs(p.Base, templ.Attributes{
		"data-slot": "feedback-panel",
		"data-tone": "empty",
	})
}

func loadingPanelAttrs(p LoadingPanelProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "feedback-panel",
		"data-tone": "loading",
		"aria-busy": "true",
	}
	// The panel is the single live region announcing the label/description once
	// (the composed spinner renders decorative so AT hears no duplicate). Decorative
	// silences the panel when an external live region already announces the busy
	// state.
	if !p.Decorative {
		owned["role"] = "status"
	}
	return kit.RootAttrs(p.Base, owned)
}

func errorPanelAttrs(p ErrorPanelProps) templ.Attributes {
	return kit.RootAttrs(p.Base, templ.Attributes{
		"data-slot": "feedback-panel",
		"data-tone": "error",
		"role":      "alert",
	})
}

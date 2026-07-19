package primitives

import (
	"strconv"

	"github.com/a-h/templ"
)

// Attachment (P55, family F3) is a compound file/media card: the caller composes
// AttachmentMedia (image preview or file-type icon), AttachmentContent (holding
// AttachmentTitle/AttachmentDescription metadata), AttachmentActions (trailing
// controls), and optionally AttachmentTrigger (a full-card link/button). Multiple
// cards live inside a scrolling AttachmentGroup.
//
// State ownership (README §8 invariant 1). The primitive DISPLAYS caller-owned
// upload state — it does not select files, own an upload route, storage,
// progress, retries, or authorization. The server decides each attachment's
// AttachmentState (idle/uploading/processing/error/done) and the amount of
// progress; the card renders the matching presentation. Because presentation
// carries a state glyph AND a text status label (never color alone), the state is
// conveyed with meaning beyond color, and the error state's label is the
// accessible error description.
//
// Independently focusable trigger/actions. AttachmentTrigger stretches over the
// whole card via a CSS ::after (no inline style); AttachmentActions sit above it
// (position/z-index in components.css) so each action button remains a separate
// tab stop with its own accessible name. Keyboard order follows DOM order: place
// the trigger before the actions.
//
// data-slot hooks: attachment, attachment-media, attachment-content,
// attachment-title, attachment-description, attachment-actions, attachment-status,
// attachment-trigger, attachment-group.

// AttachmentSize is the typed size enum for Attachment (P55). The zero value is
// the medium (default) size; sm and lg are the compact and expanded sizes.
type AttachmentSize string

const (
	// AttachmentMedium is the zero value and the documented default.
	AttachmentMedium AttachmentSize = ""
	AttachmentSmall  AttachmentSize = "sm"
	AttachmentLarge  AttachmentSize = "lg"
)

// Valid reports whether s is a known AttachmentSize.
func (s AttachmentSize) Valid() bool {
	switch s {
	case AttachmentMedium, AttachmentSmall, AttachmentLarge:
		return true
	default:
		return false
	}
}

func (s AttachmentSize) attr() string {
	switch s {
	case AttachmentSmall:
		return "sm"
	case AttachmentLarge:
		return "lg"
	default:
		return "md"
	}
}

// AttachmentOrientation is the typed orientation enum. The zero value lays the
// card out horizontally (media beside content); vertical stacks media above it.
type AttachmentOrientation string

const (
	// AttachmentHorizontal is the zero value and the documented default.
	AttachmentHorizontal AttachmentOrientation = ""
	AttachmentVertical   AttachmentOrientation = "vertical"
)

// Valid reports whether o is a known AttachmentOrientation.
func (o AttachmentOrientation) Valid() bool {
	switch o {
	case AttachmentHorizontal, AttachmentVertical:
		return true
	default:
		return false
	}
}

func (o AttachmentOrientation) attr() string {
	if o == AttachmentVertical {
		return "vertical"
	}
	return "horizontal"
}

// AttachmentState is the caller-owned upload state the card displays. The zero
// value is idle (a settled attachment with no in-flight upload).
type AttachmentState string

const (
	// AttachmentIdle is the zero value: a settled attachment, no status affordance.
	AttachmentIdle       AttachmentState = ""
	AttachmentUploading  AttachmentState = "uploading"
	AttachmentProcessing AttachmentState = "processing"
	AttachmentError      AttachmentState = "error"
	AttachmentDone       AttachmentState = "done"
)

// Valid reports whether s is a known AttachmentState.
func (s AttachmentState) Valid() bool {
	switch s {
	case AttachmentIdle, AttachmentUploading, AttachmentProcessing, AttachmentError, AttachmentDone:
		return true
	default:
		return false
	}
}

func (s AttachmentState) attr() string {
	if s.Valid() && s != AttachmentIdle {
		return string(s)
	}
	return "idle"
}

func (s AttachmentState) defaultLabel() string {
	switch s {
	case AttachmentUploading:
		return "Uploading"
	case AttachmentProcessing:
		return "Processing"
	case AttachmentError:
		return "Error"
	case AttachmentDone:
		return "Done"
	default:
		return ""
	}
}

// AttachmentProps configures the Attachment root card (P55, family F3). The caller
// composes the parts as children. The zero value is a valid idle, horizontal,
// medium card.
type AttachmentProps struct {
	Base
	// Size selects the card scale. Zero value is medium.
	Size AttachmentSize
	// Orientation selects the layout axis. Zero value is horizontal.
	Orientation AttachmentOrientation
	// State is the caller-owned upload state the card displays. Zero value is idle
	// (no status affordance).
	State AttachmentState
	// StatusLabel is the accessible text describing State (e.g. "Uploading 60%",
	// "Upload failed: file too large"). When State is not idle and StatusLabel is
	// empty, a per-state default label is used so the state is never conveyed by
	// color alone.
	StatusLabel string
	// Progress renders a determinate value (0..100) in the uploading state; 0 (or a
	// non-uploading state) renders the indeterminate busy treatment. The value is a
	// native <progress> attribute, never an inline style.
	Progress int
}

func (p AttachmentProps) statusLabel() string {
	if p.StatusLabel != "" {
		return p.StatusLabel
	}
	return p.State.defaultLabel()
}

func (p AttachmentProps) clampedProgress() int {
	switch {
	case p.Progress < 0:
		return 0
	case p.Progress > 100:
		return 100
	default:
		return p.Progress
	}
}

// AttachmentMediaProps configures AttachmentMedia (the leading image/icon slot).
type AttachmentMediaProps struct{ Base }

// AttachmentContentProps configures AttachmentContent (the title/description column).
type AttachmentContentProps struct{ Base }

// AttachmentTitleProps configures AttachmentTitle.
type AttachmentTitleProps struct{ Base }

// AttachmentDescriptionProps configures AttachmentDescription (file metadata).
type AttachmentDescriptionProps struct{ Base }

// AttachmentActionsProps configures AttachmentActions (the trailing controls slot).
type AttachmentActionsProps struct{ Base }

// AttachmentTriggerProps configures AttachmentTrigger, the full-card affordance.
// With URL set it is an <a> (the no-JS baseline, e.g. open/preview the file);
// empty renders a <button>. Because the trigger is commonly icon-only or a
// stretched overlay, Label is the required accessible name (enforced by a contract
// test); it is never left to the Base.Attributes escape hatch.
type AttachmentTriggerProps struct {
	Base
	// URL is the destination. When set the trigger is a real link; empty renders a
	// <button type="button">.
	URL URL
	// Label is the accessible name (aria-label). Required so a stretched/icon-only
	// trigger never ships nameless.
	Label string
}

// AttachmentGroupProps configures AttachmentGroup, the scrolling container for
// multiple cards. Label is the optional accessible name (aria-label) for the
// region.
type AttachmentGroupProps struct {
	Base
	// Label is the group's accessible name (aria-label). Recommended so assistive
	// technology can distinguish it from other regions.
	Label string
}

func attachmentClass(p AttachmentProps) string { return classNames("goth-attachment", p.Class) }

func attachmentAttrs(p AttachmentProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":        "attachment",
		"data-size":        p.Size.attr(),
		"data-orientation": p.Orientation.attr(),
		"data-state":       p.State.attr(),
	}
	return ownedAttrs(p.Base, owned)
}

func attachmentPartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func attachmentPartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}

func attachmentTriggerAttrs(p AttachmentTriggerProps, link bool) templ.Attributes {
	owned := templ.Attributes{"data-slot": "attachment-trigger"}
	if !link {
		owned["type"] = "button"
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func attachmentGroupAttrs(p AttachmentGroupProps) templ.Attributes {
	// tabindex=0 makes the scrolling shelf keyboard-operable (a scrollable region
	// must be reachable so a keyboard user can scroll it) and satisfies the
	// scrollable-region-focusable rule when the cards themselves hold no controls.
	owned := templ.Attributes{
		"data-slot": "attachment-group",
		"role":      "group",
		"tabindex":  "0",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func attachmentProgressText(p AttachmentProps) string {
	return strconv.Itoa(p.clampedProgress()) + "%"
}

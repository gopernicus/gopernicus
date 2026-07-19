package primitives

import "github.com/a-h/templ"

// Shared modal/panel (overlay) helpers for the GOTH-4.2 gothDialog-backed
// primitives: Dialog (P39), Alert Dialog (P37), Drawer (P40), and Sheet (P47).
// All four compose the frozen GOTH-4.1 overlay mechanics (scrim/panel, focus
// trap/restore, background scroll lock, background inert, nested-aware
// Escape/outside dismissal) through the single gothDialog controller — no
// primitive forks the mechanics and no new controller name is introduced
// (README §8). The div-based overlay is deliberate over a native <dialog>: native
// modal behavior (top layer, backdrop, focus trap) needs showModal() JS anyway,
// so it gives no honest no-JS advantage, and it cannot deliver the frozen GOTH-4.1
// nested overlay-stack semantics, class-based CSP-safe scroll lock, or the shared
// trap stack without forking them. The no-JS baseline is server-owned: CSS shows
// the overlay only when the root renders data-state="open", so a server-open
// overlay is readable without JavaScript and a closed one shows only its trigger.

// overlayRootAttrs builds the gothDialog root attributes shared by the modal/panel
// primitives. The server-owned data-state is the no-JS baseline. modal=false emits
// data-modal="false" (the controller skips background scroll lock + inert and
// asserts no aria-modal); dismissOutside=false emits data-dismiss-outside="false"
// (the controller keeps Escape but ignores an outside/scrim press — the Alert
// Dialog decision contract).
func overlayRootAttrs(b Base, slot string, open, modal, dismissOutside bool) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  slot,
		"data-state": openState(open),
		"x-data":     "gothDialog",
	}
	if !modal {
		owned["data-modal"] = "false"
	}
	if !dismissOutside {
		owned["data-dismiss-outside"] = "false"
	}
	return ownedAttrs(b, owned)
}

// overlayTriggerAttrs builds a trigger button that opens the overlay. haspopup is
// the aria-haspopup token (always "dialog" for these primitives).
func overlayTriggerAttrs(b Base, haspopup string) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot":     "trigger",
		"type":          "button",
		"aria-haspopup": haspopup,
		"x-on:click":    "show($event)",
	})
}

// overlayCloseAttrs builds a close button that dismisses the overlay.
func overlayCloseAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot":  "close",
		"type":       "button",
		"x-on:click": "hide($event)",
	})
}

// overlayPanelAttrs builds the role=dialog/alertdialog panel (data-slot="content",
// the region the controller traps focus within). aria-modal is NOT emitted here:
// the gothDialog controller sets it only while it actually enforces modality
// (background inert), so the assertion is honest under the no-JS baseline (nothing
// is inert without JavaScript). ARIA name/description linkage is caller-passed via
// Base.ID on the title/description parts.
func overlayPanelAttrs(b Base, role, labelledby, describedby string) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "content",
		"role":      role,
	}
	if labelledby != "" {
		owned["aria-labelledby"] = labelledby
	}
	if describedby != "" {
		owned["aria-describedby"] = describedby
	}
	return ownedAttrs(b, owned)
}

// overlayEdgePanelAttrs builds a Sheet/Drawer edge panel (role=dialog,
// data-slot="content") carrying the resolved data-side the CSS anchors and
// animates from. Otherwise identical to overlayPanelAttrs.
func overlayEdgePanelAttrs(b Base, side, labelledby, describedby string) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "content",
		"role":      "dialog",
		"data-side": side,
	}
	if labelledby != "" {
		owned["aria-labelledby"] = labelledby
	}
	if describedby != "" {
		owned["aria-describedby"] = describedby
	}
	return ownedAttrs(b, owned)
}

// overlayPartAttrs builds a structural sub-part (header/footer/title/description)
// carrying only its stable data-slot plus any caller Base.ID/attributes.
func overlayPartAttrs(b Base, slot string) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{"data-slot": slot})
}

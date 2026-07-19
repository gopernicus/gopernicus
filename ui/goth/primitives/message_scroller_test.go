package primitives

import (
	"strings"
	"testing"
)

// GOTH-6.2 Message Scroller (P60). These tests prove the compound F4 parts: the
// controller binding + initial scroll-state hooks on the root, the role=log viewport
// with a required accessible name, the history trigger (link vs button + the
// data-goth-history prepend marker + required name), the content swap target, the
// unread status affordance (data-goth-status + default label), the jump-to-message
// anchor, the attribute-merge ownership, and the no-inline-style invariant. The
// controller behaviors (live-edge follow, prepend anchoring, jump focus, unread
// exposure) are proven in the 3-engine message-scroller e2e spec.

// TestNoMessageScrollerPrimitiveEmitsInlineStyle proves invariant (a).
func TestNoMessageScrollerPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, MessageScroller(MessageScrollerProps{}), "x"),
		renderKids(t, MessageScrollerViewport(MessageScrollerViewportProps{Label: "Transcript"}), "x"),
		renderKids(t, MessageScrollerHistory(MessageScrollerHistoryProps{Label: "Earlier"}), "x"),
		renderKids(t, MessageScrollerContent(MessageScrollerContentProps{}), "x"),
		render(t, MessageScrollerStatus(MessageScrollerStatusProps{})),
		renderKids(t, MessageScrollerJump(MessageScrollerJumpProps{TargetID: "m-1", Label: "Jump"}), "x"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("message-scroller primitive emitted an inline style=: %s", o)
		}
	}
}

// TestMessageScrollerRootHooks proves the root binds the controller and carries the
// initial at-edge/unread scroll state.
func TestMessageScrollerRootHooks(t *testing.T) {
	out := renderKids(t, MessageScroller(MessageScrollerProps{Base: Base{ID: "chat"}}), "body")
	mustContain(t, out,
		`class="goth-message-scroller"`, `data-slot="message-scroller"`, `id="chat"`,
		`x-data="gothMessageScroller"`, `data-at-edge="true"`, `data-unread="0"`, `body`)
}

// TestMessageScrollerViewport proves the role=log region carries the required
// accessible name and is keyboard-scrollable.
func TestMessageScrollerViewport(t *testing.T) {
	out := renderKids(t, MessageScrollerViewport(MessageScrollerViewportProps{Label: "Support thread"}), "turns")
	mustContain(t, out,
		`class="goth-message-scroller-viewport"`, `data-slot="message-scroller-viewport"`,
		`role="log"`, `tabindex="0"`, `aria-label="Support thread"`, `turns`)
}

// TestMessageScrollerHistory proves the earlier-history trigger is a link when URL is
// set (the no-JS reload) and a button otherwise, always marked data-goth-history so the
// controller anchors the prepend, and always named.
func TestMessageScrollerHistory(t *testing.T) {
	link := renderKids(t, MessageScrollerHistory(MessageScrollerHistoryProps{
		URL: mustParseURL(t, "/thread?before=42"), Label: "Load earlier messages",
	}), "Earlier")
	mustContain(t, link,
		`<a`, `href="/thread?before=42"`, `data-slot="message-scroller-history"`,
		`data-goth-history="true"`, `aria-label="Load earlier messages"`, `Earlier`)
	mustNotContain(t, link, `type="button"`)

	btn := renderKids(t, MessageScrollerHistory(MessageScrollerHistoryProps{Label: "Earlier"}), "Earlier")
	mustContain(t, btn, `<button`, `type="button"`, `data-goth-history="true"`, `aria-label="Earlier"`)
}

// TestMessageScrollerContentAndStatus proves the content swap target and the unread
// affordance (a named button marked data-goth-status carrying a default label).
func TestMessageScrollerContentAndStatus(t *testing.T) {
	content := renderKids(t, MessageScrollerContent(MessageScrollerContentProps{}), "turns")
	mustContain(t, content, `class="goth-message-scroller-content"`, `data-slot="message-scroller-content"`, `turns`)

	// With no Label the button is named by its visible label text (the default);
	// no redundant aria-label is emitted.
	status := render(t, MessageScrollerStatus(MessageScrollerStatusProps{}))
	mustContain(t, status,
		`<button`, `type="button"`, `data-slot="message-scroller-status"`, `data-goth-status="true"`,
		`data-slot="message-scroller-status-label"`, `Jump to latest messages`)
	mustNotContain(t, status, `aria-label=`)

	custom := render(t, MessageScrollerStatus(MessageScrollerStatusProps{Label: "See newest"}))
	mustContain(t, custom, `aria-label="See newest"`, `See newest`)
}

// TestMessageScrollerJump proves the jump link targets #id and is named.
func TestMessageScrollerJump(t *testing.T) {
	out := renderKids(t, MessageScrollerJump(MessageScrollerJumpProps{TargetID: "msg-7", Label: "Go to reply"}), "↑")
	mustContain(t, out,
		`<a`, `href="#msg-7"`, `class="goth-message-scroller-jump"`, `data-slot="message-scroller-jump"`,
		`data-goth-jump="true"`, `aria-label="Go to reply"`, `↑`)
}

// TestMessageScrollerMergeHonorsOwnership proves a caller cannot overwrite a
// behavior-critical owned attribute (including the controller binding) or drop the
// compatibility class through the escape hatch, while still adding hx-* for the swap.
func TestMessageScrollerMergeHonorsOwnership(t *testing.T) {
	out := renderKids(t, MessageScroller(MessageScrollerProps{Base: Base{
		Class: "custom-c",
		Attributes: map[string]any{
			"x-data":      "evil",
			"data-slot":   "hijack",
			"data-unread": "9",
			"class":       "dropped",
		},
	}}), "x")
	mustContain(t, out, `x-data="gothMessageScroller"`, `data-slot="message-scroller"`,
		`data-unread="0"`, `goth-message-scroller custom-c`)
	mustNotContain(t, out, `evil`, `hijack`, `data-unread="9"`, `dropped`)

	// The history trigger still accepts caller hx-* through Base.Attributes.
	hist := renderKids(t, MessageScrollerHistory(MessageScrollerHistoryProps{
		Label: "Earlier",
		Base: Base{Attributes: map[string]any{
			"hx-get":  "/thread/earlier",
			"hx-swap": "afterbegin",
		}},
	}), "Earlier")
	mustContain(t, hist, `hx-get="/thread/earlier"`, `hx-swap="afterbegin"`, `data-goth-history="true"`)
}

package primitives

import (
	"strings"
	"testing"
)

// GOTH-6.2 Bubble (P56) + Message (P59). These tests prove the compound F3 parts:
// the alignment/variant hooks, the seven bubble variants, the interactive
// reaction chips (button vs link + required accessible name + toggled state), the
// grouped-message slots, the enum defaults, the attribute-merge ownership, and the
// no-inline-style invariant.

// TestNoMessagingPrimitiveEmitsInlineStyle proves invariant (a): no Bubble or
// Message part emits an inline style= in any state.
func TestNoMessagingPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Bubble(BubbleProps{Align: BubbleEnd}), "x"),
		renderKids(t, BubbleContent(BubbleContentProps{Variant: BubblePrimary}), "x"),
		renderKids(t, BubbleReactions(BubbleReactionsProps{Label: "Reactions"}), "x"),
		renderKids(t, BubbleReaction(BubbleReactionProps{Emoji: "👍", Count: 3, Label: "Thumbs up", Pressed: true}), ""),
		renderKids(t, BubbleGroup(BubbleGroupProps{Align: BubbleEnd}), "x"),
		renderKids(t, Message(MessageProps{Align: MessageEnd}), "x"),
		renderKids(t, MessageAvatar(MessageAvatarProps{}), "x"),
		renderKids(t, MessageContent(MessageContentProps{}), "x"),
		renderKids(t, MessageHeader(MessageHeaderProps{}), "x"),
		renderKids(t, MessageFooter(MessageFooterProps{}), "x"),
		renderKids(t, MessageGroup(MessageGroupProps{Align: MessageEnd}), "x"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("messaging primitive emitted an inline style=: %s", o)
		}
	}
}

// TestBubbleRootAndContentHooks proves the root owns alignment and BubbleContent
// owns the variant, with the zero-value defaults.
func TestBubbleRootAndContentHooks(t *testing.T) {
	out := renderKids(t, Bubble(BubbleProps{Base: Base{ID: "b-1"}, Align: BubbleEnd}),
		`<div data-slot="bubble-content">hi</div>`)
	mustContain(t, out,
		`class="goth-bubble"`, `data-slot="bubble"`, `id="b-1"`, `data-align="end"`, `hi`)

	def := renderKids(t, Bubble(BubbleProps{}), "x")
	mustContain(t, def, `data-align="start"`)

	content := renderKids(t, BubbleContent(BubbleContentProps{Variant: BubblePrimary}), "hello")
	mustContain(t, content, `class="goth-bubble-content"`, `data-slot="bubble-content"`, `data-variant="primary"`, `hello`)

	defContent := renderKids(t, BubbleContent(BubbleContentProps{}), "x")
	mustContain(t, defContent, `data-variant="default"`)
}

// TestBubbleSevenVariants proves all seven variants render their data-variant hook
// and are Valid, and an unknown variant falls back to the default.
func TestBubbleSevenVariants(t *testing.T) {
	variants := []struct {
		v    BubbleVariant
		attr string
	}{
		{BubbleDefault, "default"},
		{BubblePrimary, "primary"},
		{BubbleSecondary, "secondary"},
		{BubbleMuted, "muted"},
		{BubbleAccent, "accent"},
		{BubbleDestructive, "destructive"},
		{BubbleOutline, "outline"},
	}
	if len(variants) != 7 {
		t.Fatalf("expected seven bubble variants, got %d", len(variants))
	}
	for _, c := range variants {
		if !c.v.Valid() {
			t.Errorf("variant %q should be Valid", c.v)
		}
		out := renderKids(t, BubbleContent(BubbleContentProps{Variant: c.v}), "x")
		mustContain(t, out, `data-variant="`+c.attr+`"`)
	}
	if BubbleVariant("neon").Valid() {
		t.Error("unknown variant should not be Valid")
	}
	if BubbleVariant("neon").attr() != "default" {
		t.Error("unknown variant should render the default")
	}
}

// TestBubbleReactionInteractive proves a reaction is a button by default (with a
// toggled aria-pressed) or a link when URL is set, always carries the required
// accessible name, and announces the count in text while keeping the emoji
// decorative.
func TestBubbleReactionInteractive(t *testing.T) {
	btn := render(t, BubbleReaction(BubbleReactionProps{Emoji: "👍", Count: 3, Label: "Thumbs up", Pressed: true}))
	mustContain(t, btn,
		`<button`, `type="button"`, `class="goth-bubble-reaction"`, `data-slot="bubble-reaction"`,
		`aria-label="Thumbs up"`, `aria-pressed="true"`, `data-pressed="true"`,
		`aria-hidden="true"`, `3`)

	unpressed := render(t, BubbleReaction(BubbleReactionProps{Emoji: "❤️", Label: "Heart"}))
	mustContain(t, unpressed, `aria-pressed="false"`, `data-pressed="false"`)
	// Zero count renders no count span.
	mustNotContain(t, unpressed, `goth-bubble-reaction-count`)

	link := render(t, BubbleReaction(BubbleReactionProps{
		Emoji: "🎉", Count: 1, Label: "Party", URL: URL{raw: "/reactions/party"},
	}))
	mustContain(t, link, `<a`, `href="/reactions/party"`, `aria-label="Party"`, `data-pressed="false"`)
	mustNotContain(t, link, `type="button"`, `aria-pressed`)
}

// TestBubbleReactionsAndGroup proves the labelled reactions group and the aligned
// bubble group.
func TestBubbleReactionsAndGroup(t *testing.T) {
	reactions := renderKids(t, BubbleReactions(BubbleReactionsProps{Label: "Reactions"}), "chips")
	mustContain(t, reactions,
		`class="goth-bubble-reactions"`, `data-slot="bubble-reactions"`, `role="group"`,
		`aria-label="Reactions"`, `chips`)

	group := renderKids(t, BubbleGroup(BubbleGroupProps{Align: BubbleEnd}), "bubbles")
	mustContain(t, group,
		`class="goth-bubble-group"`, `data-slot="bubble-group"`, `data-align="end"`, `bubbles`)
}

// TestMessageParts proves the aligned row, the avatar/content/header/footer slots,
// and the grouped-message container with the zero-value default alignment.
func TestMessageParts(t *testing.T) {
	row := renderKids(t, Message(MessageProps{Base: Base{ID: "m-1"}, Align: MessageEnd}), "body")
	mustContain(t, row,
		`class="goth-message"`, `data-slot="message"`, `id="m-1"`, `data-align="end"`, `body`)

	def := renderKids(t, Message(MessageProps{}), "x")
	mustContain(t, def, `data-align="start"`)

	parts := map[string]string{
		"message-avatar":  renderKids(t, MessageAvatar(MessageAvatarProps{}), "av"),
		"message-content": renderKids(t, MessageContent(MessageContentProps{}), "co"),
		"message-header":  renderKids(t, MessageHeader(MessageHeaderProps{}), "he"),
		"message-footer":  renderKids(t, MessageFooter(MessageFooterProps{}), "fo"),
	}
	for slot, out := range parts {
		mustContain(t, out, `data-slot="`+slot+`"`, `class="goth-`+slot+`"`)
	}

	group := renderKids(t, MessageGroup(MessageGroupProps{Align: MessageEnd}), "msgs")
	mustContain(t, group,
		`class="goth-message-group"`, `data-slot="message-group"`, `data-align="end"`, `msgs`)
}

// TestMessagingEnums proves the zero-value defaults and Valid membership for the
// bubble/message alignment and variant enums.
func TestMessagingEnums(t *testing.T) {
	if BubbleStart.attr() != "start" || BubbleEnd.attr() != "end" {
		t.Error("bubble align zero value should be start")
	}
	if MessageStart.attr() != "start" || MessageEnd.attr() != "end" {
		t.Error("message align zero value should be start")
	}
	if !BubbleStart.Valid() || !BubbleEnd.Valid() || !MessageStart.Valid() || !MessageEnd.Valid() {
		t.Error("known align values should be Valid")
	}
	if BubbleAlign("center").Valid() || MessageAlign("center").Valid() {
		t.Error("unknown align values should not be Valid")
	}
	if BubbleAlign("center").attr() != "start" || MessageAlign("center").attr() != "start" {
		t.Error("unknown align should render the safe default")
	}
}

// TestMessagingMergeHonorsOwnership proves a caller cannot overwrite a
// behavior-critical owned attribute or drop the compatibility class through the
// Base.Attributes escape hatch, on both a Bubble and a Message.
func TestMessagingMergeHonorsOwnership(t *testing.T) {
	bubble := renderKids(t, Bubble(BubbleProps{Base: Base{
		Class: "custom-b",
		Attributes: map[string]any{
			"data-slot":  "hijack",
			"data-align": "end",
			"class":      "dropped",
		},
	}}), "x")
	mustContain(t, bubble, `data-slot="bubble"`, `data-align="start"`, `goth-bubble custom-b`)
	mustNotContain(t, bubble, `hijack`, `data-align="end"`, `dropped`)

	msg := renderKids(t, Message(MessageProps{Base: Base{
		Class: "custom-m",
		Attributes: map[string]any{
			"data-slot": "hijack",
			"class":     "dropped",
		},
	}}), "x")
	mustContain(t, msg, `data-slot="message"`, `goth-message custom-m`)
	mustNotContain(t, msg, `hijack`, `dropped`)
}

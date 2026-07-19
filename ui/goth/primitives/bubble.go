package primitives

import (
	"strconv"

	"github.com/a-h/templ"
)

// Bubble (P56, family F3) is a compound chat/message bubble: the caller composes
// BubbleContent (the coloured speech surface holding text or interactive
// link/button content), an optional BubbleReactions row of BubbleReaction chips,
// and — when several consecutive bubbles come from one sender — wraps them in a
// BubbleGroup. The Bubble root owns alignment (start = received, end = sent);
// BubbleContent owns the visual variant.
//
// The primitive is presentational (StylesOnly, no controller). Interactive content
// inside a bubble is ordinary caller markup (links/buttons); a BubbleReaction is a
// real <button> (or <a> when a URL is set) with a required accessible name, so an
// emoji-only reaction never ships nameless.
//
// data-slot hooks: bubble, bubble-content, bubble-reactions, bubble-reaction,
// bubble-group. data-align (start/end) on bubble/bubble-group; data-variant on
// bubble-content; data-pressed on a toggled bubble-reaction.

// BubbleVariant is the typed visual variant enum for BubbleContent. There are
// seven variants; the zero value is the default (neutral) surface. Each maps to a
// frozen semantic theme role so a host theme can restyle it without touching
// markup.
type BubbleVariant string

const (
	// BubbleDefault is the zero value and the documented default (neutral surface).
	BubbleDefault     BubbleVariant = ""
	BubblePrimary     BubbleVariant = "primary"
	BubbleSecondary   BubbleVariant = "secondary"
	BubbleMuted       BubbleVariant = "muted"
	BubbleAccent      BubbleVariant = "accent"
	BubbleDestructive BubbleVariant = "destructive"
	BubbleOutline     BubbleVariant = "outline"
)

// Valid reports whether v is a known BubbleVariant.
func (v BubbleVariant) Valid() bool {
	switch v {
	case BubbleDefault, BubblePrimary, BubbleSecondary, BubbleMuted,
		BubbleAccent, BubbleDestructive, BubbleOutline:
		return true
	default:
		return false
	}
}

func (v BubbleVariant) attr() string {
	if v.Valid() && v != BubbleDefault {
		return string(v)
	}
	return "default"
}

// BubbleAlign is the typed alignment enum. The zero value is start (a received
// bubble, leading edge); end aligns a sent bubble to the trailing edge. Alignment
// is expressed with logical properties so it follows the writing direction (RTL).
type BubbleAlign string

const (
	// BubbleStart is the zero value and the documented default (received).
	BubbleStart BubbleAlign = ""
	BubbleEnd   BubbleAlign = "end"
)

// Valid reports whether a is a known BubbleAlign.
func (a BubbleAlign) Valid() bool {
	switch a {
	case BubbleStart, BubbleEnd:
		return true
	default:
		return false
	}
}

func (a BubbleAlign) attr() string {
	if a == BubbleEnd {
		return "end"
	}
	return "start"
}

// BubbleProps configures the Bubble root (the aligned column wrapper). The zero
// value is a valid start-aligned bubble.
type BubbleProps struct {
	Base
	// Align selects the leading (start, received) or trailing (end, sent) edge.
	Align BubbleAlign
}

// BubbleContentProps configures BubbleContent, the coloured speech surface. Its
// children are the message text or interactive link/button content.
type BubbleContentProps struct {
	Base
	// Variant selects the visual surface. Zero value is the neutral default.
	Variant BubbleVariant
}

// BubbleReactionsProps configures BubbleReactions, the row of reaction chips. Label
// is the optional accessible name (aria-label) for the group.
type BubbleReactionsProps struct {
	Base
	// Label is the group's accessible name (aria-label).
	Label string
}

// BubbleReactionProps configures one BubbleReaction chip. A reaction is interactive
// by default (a real <button>, toggled via Pressed/aria-pressed) or a link when URL
// is set. Because the emoji is decorative, Label is the REQUIRED accessible name
// (enforced by a contract test); it is never left to the Base.Attributes escape
// hatch.
type BubbleReactionProps struct {
	Base
	// Emoji is the decorative reaction glyph (aria-hidden). The accessible meaning
	// comes from Label + Count.
	Emoji string
	// Count is the reaction tally; rendered when > 0.
	Count int
	// Label is the accessible name (aria-label), e.g. "Thumbs up". Required.
	Label string
	// Pressed marks the caller as having reacted (aria-pressed) on the button form.
	Pressed bool
	// URL, when set, renders the reaction as a real link instead of a button.
	URL URL
}

// BubbleGroupProps configures BubbleGroup, the wrapper for consecutive bubbles from
// one sender. It shares the Align of its bubbles and tightens their inner corner
// radii via CSS.
type BubbleGroupProps struct {
	Base
	// Align selects the shared edge for the grouped bubbles.
	Align BubbleAlign
}

func bubbleClass(p BubbleProps) string { return classNames("goth-bubble", p.Class) }

func bubbleAttrs(p BubbleProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "bubble",
		"data-align": p.Align.attr(),
	})
}

func bubbleContentClass(p BubbleContentProps) string {
	return classNames("goth-bubble-content", p.Class)
}

func bubbleContentAttrs(p BubbleContentProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":    "bubble-content",
		"data-variant": p.Variant.attr(),
	})
}

func bubbleReactionsClass(p BubbleReactionsProps) string {
	return classNames("goth-bubble-reactions", p.Class)
}

func bubbleReactionsAttrs(p BubbleReactionsProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "bubble-reactions",
		"role":      "group",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func bubbleReactionClass(p BubbleReactionProps) string {
	return classNames("goth-bubble-reaction", p.Class)
}

func bubbleReactionAttrs(p BubbleReactionProps, link bool) templ.Attributes {
	owned := templ.Attributes{"data-slot": "bubble-reaction"}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	if link {
		owned["data-pressed"] = boolAttr(p.Pressed)
	} else {
		owned["type"] = "button"
		owned["aria-pressed"] = boolAttr(p.Pressed)
		owned["data-pressed"] = boolAttr(p.Pressed)
	}
	return ownedAttrs(p.Base, owned)
}

func bubbleReactionCount(p BubbleReactionProps) string {
	if p.Count <= 0 {
		return ""
	}
	return strconv.Itoa(p.Count)
}

func bubbleGroupClass(p BubbleGroupProps) string {
	return classNames("goth-bubble-group", p.Class)
}

func bubbleGroupAttrs(p BubbleGroupProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "bubble-group",
		"data-align": p.Align.attr(),
	})
}

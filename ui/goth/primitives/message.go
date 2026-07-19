package primitives

import "github.com/a-h/templ"

// Message (P59, family F3) is a compound aligned message row: the caller composes
// MessageAvatar (a leading avatar slot, typically holding an Avatar primitive),
// MessageContent (the column), and — inside the column — MessageHeader (author +
// timestamp), the message body (ordinary children, often a Bubble), and
// MessageFooter (status/actions/timestamp). When several consecutive messages come
// from one sender, MessageGroup heads them with a single avatar column and stacks
// the message contents ("grouped-message slots").
//
// The primitive is presentational (StylesOnly, no controller). It composes Avatar
// (P03) and Bubble (P56) naturally but requires neither. The Message row is honest
// server-rendered markup readable with no JavaScript.
//
// data-slot hooks: message, message-avatar, message-content, message-header,
// message-footer, message-group. data-align (start/end) on message/message-group.

// MessageAlign is the typed alignment enum. The zero value is start (an incoming
// message on the leading edge); end aligns an outgoing message to the trailing
// edge. Alignment uses logical properties so it follows the writing direction (RTL).
type MessageAlign string

const (
	// MessageStart is the zero value and the documented default (incoming).
	MessageStart MessageAlign = ""
	MessageEnd   MessageAlign = "end"
)

// Valid reports whether a is a known MessageAlign.
func (a MessageAlign) Valid() bool {
	switch a {
	case MessageStart, MessageEnd:
		return true
	default:
		return false
	}
}

func (a MessageAlign) attr() string {
	if a == MessageEnd {
		return "end"
	}
	return "start"
}

// MessageProps configures the Message root row. The zero value is a valid
// start-aligned row.
type MessageProps struct {
	Base
	// Align selects the leading (start, incoming) or trailing (end, outgoing) edge.
	Align MessageAlign
}

// MessageAvatarProps configures MessageAvatar, the leading avatar slot. The caller
// places an Avatar (or spacer) inside.
type MessageAvatarProps struct{ Base }

// MessageContentProps configures MessageContent, the column holding the header,
// body, and footer.
type MessageContentProps struct{ Base }

// MessageHeaderProps configures MessageHeader (author name + timestamp).
type MessageHeaderProps struct{ Base }

// MessageFooterProps configures MessageFooter (status/actions/timestamp).
type MessageFooterProps struct{ Base }

// MessageGroupProps configures MessageGroup, the container that heads several
// consecutive messages from one sender with a single avatar column. It shares the
// Align of its messages.
type MessageGroupProps struct {
	Base
	// Align selects the shared edge for the grouped messages.
	Align MessageAlign
}

func messageClass(p MessageProps) string { return classNames("goth-message", p.Class) }

func messageAttrs(p MessageProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "message",
		"data-align": p.Align.attr(),
	})
}

func messagePartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func messagePartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}

func messageGroupClass(p MessageGroupProps) string {
	return classNames("goth-message-group", p.Class)
}

func messageGroupAttrs(p MessageGroupProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "message-group",
		"data-align": p.Align.attr(),
	})
}

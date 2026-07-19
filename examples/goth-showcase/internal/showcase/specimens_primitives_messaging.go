package showcase

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/htmx"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// GOTH-6.2 messaging specimens. Both StylesOnly (P56 Bubble and P59 Message bind
// no controller and need no JavaScript — a message list is honest server-rendered
// markup readable with no JS):
//
//   - primitive-bubble (P56): the seven variants, start/end alignment, a grouped
//     thread, a reactions row of interactive chips (button + link), and interactive
//     link content inside a bubble.
//   - primitive-message (P59): the aligned row with avatar/header/body/footer, an
//     end-aligned outgoing row, a Message composing a Bubble, and a grouped-message
//     block heading several messages with one avatar.
//
//   - primitive-message-scroller (P60): the Full profile. A server-rendered role=log
//     transcript readable with no JS (real "load earlier" link + native #id jump
//     anchors) enhanced by the gothMessageScroller controller (added to §8 by the
//     2026-07-18 gate-b-review.md addendum) for live-edge following, HTMX
//     prepend-without-jump, jump-to-message focus, and unread/scroll-state exposure.

func registerMessagingSpecimens(r *Registry) {
	r.Register(Specimen{
		ID:        "primitive-bubble",
		Title:     "Bubble (P56)",
		Section:   SectionPrimitive,
		Primitive: "P56",
		Profile:   goth.StylesOnly,
		Body:      bubbleSpecimen,
	})
	r.Register(Specimen{
		ID:        "primitive-message",
		Title:     "Message (P59)",
		Section:   SectionPrimitive,
		Primitive: "P59",
		Profile:   goth.StylesOnly,
		Body:      messageSpecimen,
	})
	// Message Scroller (P60) is the Full profile: the no-JS transcript is enhanced by
	// the gothMessageScroller controller (live-edge follow, prepend anchoring,
	// jump-to-message, unread state) and drives HTMX prepend/append fragments.
	r.Register(Specimen{
		ID:           "primitive-message-scroller",
		Title:        "Message Scroller (P60)",
		Section:      SectionPrimitive,
		Primitive:    "P60",
		Profile:      goth.Full,
		AllowConnect: true,
		Body:         messageScrollerSpecimen,
	})
}

// bubble composes a Bubble root + coloured content, optionally with a reactions row.
func bubble(align primitives.BubbleAlign, variant primitives.BubbleVariant, text, reactionsHTML string) string {
	inner := compKids(primitives.BubbleContent(primitives.BubbleContentProps{Variant: variant}), text)
	if reactionsHTML != "" {
		inner += reactionsHTML
	}
	return compKids(primitives.Bubble(primitives.BubbleProps{Align: align}), inner)
}

func bubbleSpecimen() string {
	var b strings.Builder

	// The seven variants.
	b.WriteString(`<h2>Variants</h2><section data-slot="bubble-variants">`)
	for _, v := range []struct {
		variant primitives.BubbleVariant
		label   string
	}{
		{primitives.BubbleDefault, "default"},
		{primitives.BubblePrimary, "primary"},
		{primitives.BubbleSecondary, "secondary"},
		{primitives.BubbleMuted, "muted"},
		{primitives.BubbleAccent, "accent"},
		{primitives.BubbleDestructive, "destructive"},
		{primitives.BubbleOutline, "outline"},
	} {
		b.WriteString(bubble(primitives.BubbleStart, v.variant, "This is a "+v.label+" bubble.", ""))
	}
	b.WriteString(`</section>`)

	// Alignment: an incoming (start) and outgoing (end) bubble.
	b.WriteString(`<h2>Alignment</h2><section data-slot="bubble-alignment">`)
	b.WriteString(bubble(primitives.BubbleStart, primitives.BubbleDefault, "Hey — did the deploy go out?", ""))
	b.WriteString(bubble(primitives.BubbleEnd, primitives.BubblePrimary, "Yep, all green.", ""))
	b.WriteString(`</section>`)

	// A grouped thread from one sender.
	var groupInner strings.Builder
	groupInner.WriteString(bubble(primitives.BubbleEnd, primitives.BubblePrimary, "Pushing the fix now.", ""))
	groupInner.WriteString(bubble(primitives.BubbleEnd, primitives.BubblePrimary, "Give it a minute to build.", ""))
	groupInner.WriteString(bubble(primitives.BubbleEnd, primitives.BubblePrimary, "Done — try again?", ""))
	group := compKids(primitives.BubbleGroup(primitives.BubbleGroupProps{Align: primitives.BubbleEnd}), groupInner.String())
	b.WriteString(`<h2>Grouped thread</h2><section data-slot="bubble-group-sample">` + group + `</section>`)

	// Reactions: interactive chips (a pressed button, an unpressed button, and a
	// permalink form).
	reactions := compKids(primitives.BubbleReactions(primitives.BubbleReactionsProps{Label: "Reactions"}),
		comp(primitives.BubbleReaction(primitives.BubbleReactionProps{Emoji: "👍", Count: 3, Label: "Thumbs up", Pressed: true}))+
			comp(primitives.BubbleReaction(primitives.BubbleReactionProps{Emoji: "🎉", Count: 1, Label: "Celebrate"}))+
			comp(primitives.BubbleReaction(primitives.BubbleReactionProps{Emoji: "❤️", Count: 2, Label: "Loved this", URL: mustURL("/specimen/primitive-bubble")})))
	b.WriteString(`<h2>Reactions</h2><section data-slot="bubble-reactions-sample">`)
	b.WriteString(bubble(primitives.BubbleStart, primitives.BubbleDefault, "Shipped it!", reactions))
	b.WriteString(`</section>`)

	// Interactive link content inside a bubble.
	linkContent := compKids(primitives.BubbleContent(primitives.BubbleContentProps{Variant: primitives.BubbleDefault}),
		`See the <a href="/specimen/primitive-message">release notes</a> for details.`)
	interactive := compKids(primitives.Bubble(primitives.BubbleProps{Align: primitives.BubbleStart}), linkContent)
	b.WriteString(`<h2>Interactive content</h2><section data-slot="bubble-interactive">` + interactive + `</section>`)

	return page("Bubble", `<section data-slot="bubble-specimen">`+b.String()+`</section>`)
}

// avatar composes an Avatar with initials fallback and (optionally) an image.
func messageAvatar(initials, imgSrc, name string) string {
	inner := compKids(primitives.AvatarFallback(primitives.AvatarFallbackProps{}), initials)
	if imgSrc != "" {
		inner += comp(primitives.AvatarImage(primitives.AvatarImageProps{Src: mustURL(imgSrc), Alt: name}))
	}
	avatar := compKids(primitives.Avatar(primitives.AvatarProps{}), inner)
	return compKids(primitives.MessageAvatar(primitives.MessageAvatarProps{}), avatar)
}

// messageRow composes a full Message: avatar + content (header, body, footer).
func messageRow(align primitives.MessageAlign, avatarHTML, author, time, body, footer string) string {
	content := compKids(primitives.MessageHeader(primitives.MessageHeaderProps{}),
		`<span>`+author+`</span><time class="goth-message-time">`+time+`</time>`)
	content += `<div data-slot="message-body">` + body + `</div>`
	if footer != "" {
		content += compKids(primitives.MessageFooter(primitives.MessageFooterProps{}), footer)
	}
	inner := avatarHTML + compKids(primitives.MessageContent(primitives.MessageContentProps{}), content)
	return compKids(primitives.Message(primitives.MessageProps{Align: align}), inner)
}

func messageSpecimen() string {
	var b strings.Builder

	// A standard incoming row: avatar + header + body + footer.
	b.WriteString(`<h2>Message row</h2><section data-slot="message-rows">`)
	b.WriteString(messageRow(primitives.MessageStart,
		messageAvatar("AL", "/avatar-ada.png", "Ada Lovelace"),
		"Ada Lovelace", "09:24",
		"The analytical engine weaves algebraic patterns.",
		"Delivered"))
	// An outgoing (end-aligned) row.
	b.WriteString(messageRow(primitives.MessageEnd,
		messageAvatar("GT", "", "Grace Hopper"),
		"Grace Hopper", "09:26",
		"Ship it.",
		"Read"))
	b.WriteString(`</section>`)

	// A Message composing a Bubble as its body.
	bubbleBody := bubble(primitives.BubbleStart, primitives.BubblePrimary, "This message body is a Bubble (P56).", "")
	msgWithBubble := messageRow(primitives.MessageStart,
		messageAvatar("AL", "/avatar-ada.png", "Ada Lovelace"),
		"Ada Lovelace", "09:30", bubbleBody, "")
	b.WriteString(`<h2>Message composing a Bubble</h2><section data-slot="message-bubble">` + msgWithBubble + `</section>`)

	// A grouped-message block: one avatar heads three consecutive messages.
	var groupBodies strings.Builder
	for _, txt := range []string{
		"First of a few notes.",
		"Second — still me.",
		"Third and last.",
	} {
		groupBodies.WriteString(compKids(primitives.MessageContent(primitives.MessageContentProps{}),
			`<div data-slot="message-body">`+txt+`</div>`))
	}
	group := compKids(primitives.MessageGroup(primitives.MessageGroupProps{Align: primitives.MessageStart}),
		messageAvatar("GT", "", "Grace Hopper")+groupBodies.String())
	b.WriteString(`<h2>Grouped messages (one avatar)</h2><section data-slot="message-group-sample">` + group + `</section>`)

	return page("Message", `<section data-slot="message-specimen">`+b.String()+`</section>`)
}

// scrollerMessage renders one transcript turn: a Message row (with a stable id so
// MessageScrollerJump/the controller can target and focus it) whose body is a Bubble.
func scrollerMessage(id, author, when, text string, align primitives.MessageAlign, variant primitives.BubbleVariant) string {
	body := bubble(primitives.BubbleStart, variant, text, "")
	content := compKids(primitives.MessageHeader(primitives.MessageHeaderProps{}),
		`<span>`+author+`</span><time>`+when+`</time>`) +
		`<div data-slot="message-body">` + body + `</div>`
	inner := messageAvatar(initials(author), "", author) +
		compKids(primitives.MessageContent(primitives.MessageContentProps{}), content)
	return compKids(primitives.Message(primitives.MessageProps{Align: align, Base: primitives.Base{ID: id}}), inner)
}

func initials(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "?"
	}
	if len(parts) == 1 {
		return strings.ToUpper(parts[0][:1])
	}
	return strings.ToUpper(parts[0][:1] + parts[len(parts)-1][:1])
}

// scrollerTurns renders the initial deterministic transcript (ids msg-1..msg-8).
func scrollerTurns() string {
	var b strings.Builder
	turns := []struct{ author, when, text string }{
		{"Ada Lovelace", "09:01", "Kicking off the release thread."},
		{"Grace Hopper", "09:02", "Standing by."},
		{"Ada Lovelace", "09:03", "Running the migration now."},
		{"Grace Hopper", "09:04", "Logs look clean on my end."},
		{"Ada Lovelace", "09:05", "Deploy is out."},
		{"Grace Hopper", "09:06", "Smoke tests green."},
		{"Ada Lovelace", "09:07", "Marking the release done."},
		{"Grace Hopper", "09:08", "Nice work — closing out."},
	}
	for i, t := range turns {
		align := primitives.MessageStart
		variant := primitives.BubbleDefault
		if i%2 == 1 {
			align = primitives.MessageEnd
			variant = primitives.BubblePrimary
		}
		b.WriteString(scrollerMessage("msg-"+strconv.Itoa(i+1), t.author, t.when, t.text, align, variant))
	}
	return b.String()
}

func messageScrollerSpecimen() string {
	// The history trigger degrades to a full-document reload with no JS and prepends a
	// fragment under HTMX (afterbegin). The incoming trigger appends a fragment
	// (beforeend) to drive live-edge follow / unread. Both target the content region.
	// The history trigger sits ABOVE the scroll viewport (in the scroller root, not
	// inside the scroll region) so clicking it never steals the viewport's scroll
	// position — the controller relies on that position to anchor the prepend.
	history := compKids(primitives.MessageScrollerHistory(primitives.MessageScrollerHistoryProps{
		URL:   mustURL("/message-scroller/earlier"),
		Label: "Load earlier messages",
		Base: primitives.Base{Attributes: map[string]any{
			"hx-get":    "/message-scroller/earlier",
			"hx-target": "#scroller-content",
			"hx-swap":   "afterbegin show:none",
		}},
	}), "Load earlier messages")

	viewport := compKids(primitives.MessageScrollerViewport(primitives.MessageScrollerViewportProps{
		Label: "Release thread transcript",
	}), compKids(primitives.MessageScrollerContent(primitives.MessageScrollerContentProps{
		Base: primitives.Base{ID: "scroller-content"},
	}), scrollerTurns()))

	status := comp(primitives.MessageScrollerStatus(primitives.MessageScrollerStatusProps{
		Label: "Jump to latest messages",
	}))

	// Jump-to-message links (native #id anchors, controller-focused). They live inside
	// the scroller root so the controller's delegated click handler enhances them.
	jump := compKids(primitives.MessageScrollerJump(primitives.MessageScrollerJumpProps{
		TargetID: "msg-1", Label: "Jump to the first message",
	}), "First message") + " " + compKids(primitives.MessageScrollerJump(primitives.MessageScrollerJumpProps{
		TargetID: "msg-5", Label: "Jump to the deploy message",
	}), "Deploy message")
	jumpNav := `<nav data-slot="scroller-jumps">` + jump + `</nav>`

	scroller := compKids(primitives.MessageScroller(primitives.MessageScrollerProps{
		Base: primitives.Base{ID: "release-scroller"},
	}), jumpNav+history+viewport+status)

	// An incoming trigger that appends a message under HTMX (drives live-edge follow).
	incoming := `<button type="button" data-slot="scroller-incoming" hx-get="/message-scroller/incoming" hx-target="#scroller-content" hx-swap="beforeend show:none">Simulate incoming message</button>`

	body := `<section data-slot="message-scroller-specimen">` +
		`<p>The transcript below is a server-rendered <code>role=log</code>. With no JavaScript it is readable, its "Load earlier" control is a real link, and its jump links are native anchors. Enhanced, the gothMessageScroller controller sticks to the live edge, anchors prepends without jumping, focuses jumped messages, and exposes unread/scroll state.</p>` +
		scroller +
		`<p data-slot="scroller-controls">` + incoming + `</p>` +
		`</section>`
	return page("Message Scroller", body)
}

// incomingCounter drives deterministic ids for appended messages across e2e runs.
var incomingCounter = struct {
	n int
}{}

// registerMessageScrollerFixtures wires the HTMX prepend (earlier) and append
// (incoming) fragment routes. The earlier route degrades to a full document on a
// non-HTMX request (the no-JS history reload); the incoming route is HTMX-only.
func (s *Server) registerMessageScrollerFixtures() {
	s.handler.Handle(http.MethodGet, "/message-scroller/earlier", func(w http.ResponseWriter, r *http.Request) {
		earlier := scrollerMessage("msg-earlier-1", "Ada Lovelace", "08:58", "Earlier: prepping the branch.", primitives.MessageStart, primitives.BubbleDefault) +
			scrollerMessage("msg-earlier-2", "Grace Hopper", "08:59", "Earlier: reviewed the diff.", primitives.MessageEnd, primitives.BubblePrimary)

		if htmx.FromRequest(r).IsHTMX() {
			writeFragment(w, http.StatusOK, earlier)
			return
		}
		// No-JS history reload: a full document with the earlier turns prepended.
				bundle := s.bundles[goth.Full]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, true))
		content := compKids(primitives.MessageScrollerContent(primitives.MessageScrollerContentProps{
			Base: primitives.Base{ID: "scroller-content"},
		}), earlier+scrollerTurns())
		docBody := `<main data-slot="message-scroller-history-page"><h1>Earlier history</h1>` +
			compKids(primitives.MessageScrollerViewport(primitives.MessageScrollerViewportProps{Label: "Release thread transcript"}), content) +
			`<p><a href="/specimen/primitive-message-scroller">Back to the live transcript</a></p></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — earlier history"}, rawComponent(docBody))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})

	s.handler.Handle(http.MethodGet, "/message-scroller/incoming", func(w http.ResponseWriter, r *http.Request) {
		incomingCounter.n++
		id := "msg-incoming-" + strconv.Itoa(incomingCounter.n)
		msg := scrollerMessage(id, "Grace Hopper", "09:09", "Incoming update #"+strconv.Itoa(incomingCounter.n)+".", primitives.MessageEnd, primitives.BubblePrimary)
		writeFragment(w, http.StatusOK, msg)
	})
}

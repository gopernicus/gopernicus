package views

import (
	"html"
	"strings"
)

// Rendering stored bodies is a view concern, so it lives in the inbound layer
// (never in the feature's services). Per FS10 (feature-standard charter) content
// is plain text for now: no markdown, no embedded markup. A future rich-text
// pipeline enters through its own port and decision.

// RenderPlainText renders a stored body as safe HTML. The source is
// HTML-escaped first, so any tags an author typed (e.g. <script>) render as
// visible text, never live markup — this is the property bluemonday used to
// guarantee. Blank lines separate paragraphs, each wrapped in <p>…</p>; a single
// newline within a paragraph becomes <br> so authored line breaks survive
// rendering. Only these tags are emitted; everything from the source is escaped.
func RenderPlainText(source string) string {
	source = strings.ReplaceAll(source, "\r\n", "\n")

	var b strings.Builder
	for _, para := range strings.Split(source, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(html.EscapeString(para), "\n", "<br>"))
		b.WriteString("</p>")
	}
	return b.String()
}

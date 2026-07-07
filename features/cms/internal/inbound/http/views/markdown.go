package views

import (
	"bytes"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// Markdown rendering is a view concern, so it lives in the inbound layer (never
// in the feature's services). Bodies are stored as raw markdown and rendered
// to sanitized HTML at view time: goldmark → HTML, then bluemonday strips
// anything unsafe.

var (
	md       = goldmark.New(goldmark.WithExtensions(extension.GFM))
	policy   = bluemonday.UGCPolicy()
	mdInitMu sync.Mutex
)

// RenderMarkdown converts markdown source to sanitized HTML. On any render
// failure it falls back to the (escaped) raw text via the sanitizer.
func RenderMarkdown(source string) string {
	mdInitMu.Lock()
	defer mdInitMu.Unlock()

	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return policy.Sanitize(source)
	}
	return string(policy.SanitizeBytes(buf.Bytes()))
}

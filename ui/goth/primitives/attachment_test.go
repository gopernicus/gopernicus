package primitives

import (
	"strings"
	"testing"
)

// GOTH-6.1 Attachment (P55). These tests prove the compound F3 parts: the root's
// size/orientation/state hooks, the per-state status affordance (glyph + text
// label, never color alone; the error label is the accessible error description),
// the determinate upload progress, the full-card trigger (link vs button + required
// accessible name), the scrolling group, the enum defaults, the attribute-merge
// ownership, and the no-inline-style invariant.

// TestNoAttachmentPrimitiveEmitsInlineStyle proves invariant (a): no Attachment
// part emits an inline style= in any state.
func TestNoAttachmentPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Attachment(AttachmentProps{State: AttachmentUploading, Progress: 60}), "x"),
		renderKids(t, Attachment(AttachmentProps{State: AttachmentError, StatusLabel: "File too large"}), "x"),
		renderKids(t, Attachment(AttachmentProps{Size: AttachmentLarge, Orientation: AttachmentVertical}), "x"),
		renderKids(t, AttachmentMedia(AttachmentMediaProps{}), "x"),
		renderKids(t, AttachmentContent(AttachmentContentProps{}), "x"),
		renderKids(t, AttachmentActions(AttachmentActionsProps{}), "x"),
		renderKids(t, AttachmentTrigger(AttachmentTriggerProps{Label: "Open"}), "x"),
		renderKids(t, AttachmentGroup(AttachmentGroupProps{Label: "Files"}), "x"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("attachment primitive emitted an inline style=: %s", o)
		}
	}
}

// TestAttachmentRootHooks proves the root emits the size/orientation/state hooks
// and the caller composes the parts as children.
func TestAttachmentRootHooks(t *testing.T) {
	out := renderKids(t, Attachment(AttachmentProps{
		Base:        Base{ID: "att-1"},
		Size:        AttachmentLarge,
		Orientation: AttachmentVertical,
		State:       AttachmentDone,
		StatusLabel: "Uploaded",
	}), `<span data-slot="attachment-title">report.pdf</span>`)
	mustContain(t, out,
		`class="goth-attachment"`, `data-slot="attachment"`, `id="att-1"`,
		`data-size="lg"`, `data-orientation="vertical"`, `data-state="done"`,
		`report.pdf`)

	// The default (zero-value) root is idle/horizontal/medium with no status.
	def := renderKids(t, Attachment(AttachmentProps{}), "x")
	mustContain(t, def, `data-size="md"`, `data-orientation="horizontal"`, `data-state="idle"`)
	mustNotContain(t, def, `data-slot="attachment-status"`)
}

// TestAttachmentStatusMeaningBeyondColor proves each non-idle state renders a
// status region carrying a text label (a per-state default when StatusLabel is
// empty) so state is never conveyed by color alone.
func TestAttachmentStatusMeaningBeyondColor(t *testing.T) {
	cases := []struct {
		state AttachmentState
		label string
	}{
		{AttachmentUploading, "Uploading"},
		{AttachmentProcessing, "Processing"},
		{AttachmentError, "Error"},
		{AttachmentDone, "Done"},
	}
	for _, c := range cases {
		out := renderKids(t, Attachment(AttachmentProps{State: c.state}), "x")
		mustContain(t, out,
			`data-slot="attachment-status"`, `data-state="`+string(c.state)+`"`,
			`class="goth-attachment-status-label"`, c.label)
	}

	// A caller-supplied label overrides the default and, for error, is the
	// accessible error description.
	errOut := renderKids(t, Attachment(AttachmentProps{
		State: AttachmentError, StatusLabel: "Upload failed: file too large",
	}), "x")
	mustContain(t, errOut, `data-state="error"`, "Upload failed: file too large")
}

// TestAttachmentUploadingProgress proves the uploading state renders a determinate
// native <progress> (value/max, no inline style) when Progress > 0 and a text
// percentage otherwise.
func TestAttachmentUploadingProgress(t *testing.T) {
	determinate := renderKids(t, Attachment(AttachmentProps{State: AttachmentUploading, Progress: 60}), "x")
	mustContain(t, determinate, `<progress`, `value="60"`, `max="100"`, `goth-attachment-progress`)

	// Over-range progress clamps to 100.
	clamped := renderKids(t, Attachment(AttachmentProps{State: AttachmentUploading, Progress: 250}), "x")
	mustContain(t, clamped, `value="100"`)

	indeterminate := renderKids(t, Attachment(AttachmentProps{State: AttachmentUploading}), "x")
	mustContain(t, indeterminate, `goth-attachment-progress-text`, `0%`)
	mustNotContain(t, indeterminate, `<progress`)
}

// TestAttachmentTrigger proves the full-card affordance: a link when URL is set, a
// button otherwise, and the required accessible name is emitted as aria-label
// (never left to the escape hatch).
func TestAttachmentTrigger(t *testing.T) {
	link := renderKids(t, AttachmentTrigger(AttachmentTriggerProps{
		URL: mustParseURL(t, "/files/report.pdf"), Label: "Open report.pdf",
	}), "")
	mustContain(t, link,
		`<a`, `href="/files/report.pdf"`, `class="goth-attachment-trigger"`,
		`data-slot="attachment-trigger"`, `aria-label="Open report.pdf"`)
	mustNotContain(t, link, `type="button"`)

	btn := renderKids(t, AttachmentTrigger(AttachmentTriggerProps{Label: "Preview"}), "")
	mustContain(t, btn, `<button`, `type="button"`, `aria-label="Preview"`, `data-slot="attachment-trigger"`)
}

// TestAttachmentActionsAndGroup proves the actions slot and the labelled scrolling
// group region.
func TestAttachmentActionsAndGroup(t *testing.T) {
	actions := renderKids(t, AttachmentActions(AttachmentActionsProps{}), `<button type="button" aria-label="Remove">x</button>`)
	mustContain(t, actions, `class="goth-attachment-actions"`, `data-slot="attachment-actions"`, `aria-label="Remove"`)

	group := renderKids(t, AttachmentGroup(AttachmentGroupProps{Label: "Uploaded files"}), "cards")
	mustContain(t, group,
		`class="goth-attachment-group"`, `data-slot="attachment-group"`,
		`role="group"`, `tabindex="0"`, `aria-label="Uploaded files"`, "cards")
}

// TestAttachmentEnums proves the zero-value defaults and Valid membership for the
// size/orientation/state enums.
func TestAttachmentEnums(t *testing.T) {
	if AttachmentMedium.attr() != "md" || AttachmentHorizontal.attr() != "horizontal" || AttachmentIdle.attr() != "idle" {
		t.Error("zero-value enums should map to md/horizontal/idle")
	}
	if !AttachmentSmall.Valid() || !AttachmentVertical.Valid() || !AttachmentDone.Valid() {
		t.Error("known enum values should be Valid")
	}
	if AttachmentSize("xl").Valid() || AttachmentOrientation("diagonal").Valid() || AttachmentState("stalled").Valid() {
		t.Error("unknown enum values should not be Valid")
	}
	if AttachmentSize("xl").attr() != "md" || AttachmentState("stalled").attr() != "idle" {
		t.Error("unknown enum values should render the safe default")
	}
}

// TestAttachmentMergeHonorsOwnership proves a caller cannot overwrite a
// behavior-critical owned attribute or drop the compatibility class through the
// Base.Attributes escape hatch.
func TestAttachmentMergeHonorsOwnership(t *testing.T) {
	out := renderKids(t, Attachment(AttachmentProps{Base: Base{
		Class: "custom-x",
		Attributes: map[string]any{
			"data-slot":  "hijack",
			"data-state": "open",
			"class":      "dropped",
		},
	}}), "x")
	mustContain(t, out, `data-slot="attachment"`, `data-state="idle"`, `goth-attachment custom-x`)
	mustNotContain(t, out, `hijack`, `data-state="open"`, `dropped`)
}

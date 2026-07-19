package feedback

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func mustNotContain(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(out, u) {
			t.Errorf("output unexpectedly contains %q\n---\n%s", u, out)
		}
	}
}

func mustURL(t *testing.T, s string) primitives.URL {
	t.Helper()
	u, err := primitives.ParseURL(s)
	if err != nil {
		t.Fatalf("ParseURL(%q): %v", s, err)
	}
	return u
}

func TestNoFeedbackComponentEmitsInlineStyle(t *testing.T) {
	outs := []string{
		render(t, EmptyPanel(EmptyPanelProps{Title: "No results", Description: "Try again", Action: templ.Raw(`<button>New</button>`)})),
		render(t, LoadingPanel(LoadingPanelProps{Label: "Loading people", Description: "one moment"})),
		render(t, ErrorPanel(ErrorPanelProps{Description: "The request failed", Action: templ.Raw(`<button>Retry</button>`)})),
		render(t, ConfirmDialog(ConfirmDialogProps{Base: primitives.Base{ID: "del"}, TriggerLabel: "Delete", Title: "Delete?", Description: "Permanent.", Action: mustURL(t, "/delete")})),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") || strings.Contains(o, "<style") {
			t.Errorf("feedback component emitted inline style: %s", o)
		}
	}
}

func TestEmptyPanelComposesEmpty(t *testing.T) {
	out := render(t, EmptyPanel(EmptyPanelProps{
		Title:       "No people yet",
		Description: "Invite your first teammate.",
		Action:      templ.Raw(`<a href="/new">Invite</a>`),
	}))
	mustContain(t, out,
		`class="goth-feedback-panel"`, `data-slot="feedback-panel"`, `data-tone="empty"`,
		`goth-empty`, `data-slot="empty-title"`, "No people yet",
		`data-slot="empty-description"`, "Invite your first teammate.",
		`data-slot="empty-action"`, `<a href="/new">Invite</a>`)
}

func TestLoadingPanelAnnouncesOnceViaPanel(t *testing.T) {
	out := render(t, LoadingPanel(LoadingPanelProps{Label: "Loading people", Description: "one moment"}))
	mustContain(t, out,
		`data-tone="loading"`, `role="status"`, `aria-busy="true"`,
		`data-slot="feedback-panel-title"`, "Loading people",
		`data-slot="feedback-panel-description"`, "one moment",
		// The composed spinner is decorative so AT does not double-announce.
		`goth-spinner`, `aria-hidden="true"`)
	mustNotContain(t, out, `role="status" data-slot="spinner"`)

	// Default label + Decorative silences the panel's own live region.
	dec := render(t, LoadingPanel(LoadingPanelProps{Decorative: true}))
	mustContain(t, dec, "Loading")
	mustNotContain(t, dec, `role="status"`)
}

func TestErrorPanelIsAlert(t *testing.T) {
	out := render(t, ErrorPanel(ErrorPanelProps{Description: "The request failed."}))
	mustContain(t, out,
		`data-tone="error"`, `role="alert"`,
		`data-slot="empty-title"`, "Something went wrong", // default title
		"The request failed.")
}

func TestConfirmDialogComposesAlertDialog(t *testing.T) {
	out := render(t, ConfirmDialog(ConfirmDialogProps{
		Base:         primitives.Base{ID: "delete-project"},
		TriggerLabel: "Delete project",
		Title:        "Delete project?",
		Description:  "This permanently removes the project.",
		ConfirmLabel: "Delete",
		Action:       mustURL(t, "/projects/1/delete"),
	}))
	mustContain(t, out,
		`goth-alert-dialog`, `id="delete-project"`,
		`data-slot="trigger"`, "Delete project",
		`role="alertdialog"`,
		`aria-labelledby="delete-project-title"`, `aria-describedby="delete-project-description"`,
		`id="delete-project-title"`, "Delete project?",
		`id="delete-project-description"`, "This permanently removes the project.",
		`<form`, `method="post"`, `action="/projects/1/delete"`,
		`data-slot="cancel"`, "Cancel", // default cancel label
		`data-slot="action"`, "Delete",
		// Destructive by default.
		`data-variant="destructive"`)
}

func TestConfirmDialogNonDestructiveAndOpen(t *testing.T) {
	out := render(t, ConfirmDialog(ConfirmDialogProps{
		Base:           primitives.Base{ID: "publish"},
		TriggerLabel:   "Publish",
		Title:          "Publish now?",
		Action:         mustURL(t, "/publish"),
		NonDestructive: true,
		Open:           true,
	}))
	mustNotContain(t, out, `data-variant="destructive"`)
	// Server-open state is readable with no JavaScript.
	mustContain(t, out, `data-state="open"`)
}

func TestConfirmDialogMergesHTMXOntoConfirm(t *testing.T) {
	out := render(t, ConfirmDialog(ConfirmDialogProps{
		Base:         primitives.Base{ID: "arch"},
		TriggerLabel: "Archive",
		Title:        "Archive?",
		Action:       mustURL(t, "/archive"),
		ConfirmAttributes: templ.Attributes{
			"hx-post": "/archive",
			"hx-swap": "outerHTML",
		},
	}))
	mustContain(t, out, `hx-post="/archive"`, `hx-swap="outerHTML"`, `data-slot="action"`)
}

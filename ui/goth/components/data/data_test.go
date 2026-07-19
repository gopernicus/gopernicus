package data

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

func mustURL(t *testing.T, s string) primitives.URL {
	t.Helper()
	u, err := primitives.ParseURL(s)
	if err != nil {
		t.Fatalf("ParseURL(%q): %v", s, err)
	}
	return u
}

func TestNoDataComponentEmitsInlineStyle(t *testing.T) {
	out := render(t, TableToolbar(TableToolbarProps{
		Action:      mustURL(t, "/people"),
		SearchValue: "ada",
		Filters:     templ.Raw(`<select name="role"></select>`),
		Actions:     templ.Raw(`<a href="/people/new">New</a>`),
	}))
	if strings.Contains(out, "style=") || strings.Contains(out, "<style") {
		t.Errorf("data component emitted inline style: %s", out)
	}
}

func TestTableToolbarFormGET(t *testing.T) {
	out := render(t, TableToolbar(TableToolbarProps{
		Action:      mustURL(t, "/people"),
		SearchValue: "ada",
		SearchLabel: "Filter people",
		Filters:     templ.Raw(`<select name="role"></select>`),
		Actions:     templ.Raw(`<a href="/people/new">New person</a>`),
	}))
	mustContain(t, out,
		`<form`, `method="get"`, `action="/people"`, `role="search"`, `data-slot="table-toolbar-form"`,
		`goth-data-table-toolbar`,
		`goth-input`, `type="search"`, `name="q"`, `value="ada"`, `id="table-toolbar-search"`,
		`aria-label="Filter people"`, `placeholder="Filter people"`,
		`goth-button`, `type="submit"`, "Search", // no-JS submit
		`data-slot="table-toolbar-filters"`, `<select name="role">`,
		`data-slot="table-toolbar-actions"`, `New person`)
}

func TestTableToolbarDefaultsAndHTMX(t *testing.T) {
	out := render(t, TableToolbar(TableToolbarProps{
		SearchName:  "query",
		SubmitLabel: "Filter",
		SearchAttributes: templ.Attributes{
			"hx-get":     "/people",
			"hx-trigger": "input changed delay:300ms",
		},
	}))
	mustContain(t, out,
		`name="query"`, `hx-get="/people"`, `hx-trigger="input changed delay:300ms"`,
		"Filter") // custom submit label
}

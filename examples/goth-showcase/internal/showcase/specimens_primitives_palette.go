package showcase

import (
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/htmx"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// GOTH-5.2 command/combobox specimens (P50 Combobox, P51 Command). The SERVER owns
// the option data and, in the async specimen, the filtering and empty-state markup:
// /combobox/options returns a freshly filtered option list, and /combobox/pick and
// /command/run round-trip the selection. Each specimen renders the REAL primitive so
// the browser/axe harness exercises the actual emitted surface.

type paletteEntry struct {
	Value string
	Label string
}

var comboboxFruits = []paletteEntry{
	{"apple", "Apple"},
	{"apricot", "Apricot"},
	{"banana", "Banana"},
	{"blueberry", "Blueberry"},
	{"cherry", "Cherry"},
	{"grape", "Grape"},
	{"mango", "Mango"},
	{"orange", "Orange"},
	{"peach", "Peach"},
	{"pear", "Pear"},
}

// filterFruits returns the entries whose label contains q (case-insensitive). An
// empty query returns the full list — the server owns the match.
func filterFruits(q string) []paletteEntry {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return comboboxFruits
	}
	var out []paletteEntry
	for _, f := range comboboxFruits {
		if strings.Contains(strings.ToLower(f.Label), q) {
			out = append(out, f)
		}
	}
	return out
}

// comboboxOptions renders the option buttons (+ empty state when none) for the
// given entries. selected marks the committed value. This is the server-owned
// fragment the async listbox swaps in.
func comboboxOptions(list []paletteEntry, selected string) string {
	var b strings.Builder
	for _, f := range list {
		b.WriteString(compKids(primitives.ComboboxOption(primitives.ComboboxOptionProps{
			Name:     "fruit",
			Value:    f.Value,
			Selected: f.Value == selected,
		}), templ.EscapeString(f.Label)))
	}
	empty := compKids(primitives.ComboboxEmpty(primitives.ComboboxEmptyProps{Open: len(list) == 0}), "No fruits found.")
	return b.String() + empty
}

func registerPaletteSpecimens(r *Registry) {
	// Combobox (P50), client filter, Interactive: the controller hides non-matching
	// options and moves the active option; selecting one submits (GET) the value.
	r.Register(Specimen{
		ID:        "primitive-combobox",
		Title:     "Combobox (P50)",
		Section:   SectionPrimitive,
		Primitive: "P50",
		Profile:   goth.Interactive,
		Body:      comboboxSpecimen,
	})

	// Combobox (P50), server filter, Full: typing swaps the listbox with a
	// server-filtered option list (async option replacement) via HTMX.
	r.Register(Specimen{
		ID:           "primitive-combobox-async",
		Title:        "Combobox async (P50)",
		Section:      SectionPrimitive,
		Primitive:    "P50",
		Profile:      goth.Full,
		AllowConnect: true,
		Body:         comboboxAsyncSpecimen,
	})

	// Combobox (P50) no-JS: a server-open listbox of submit buttons in a POST form —
	// picking an option submits the value with Alpine and HTMX entirely absent.
	r.Register(Specimen{
		ID:        "primitive-combobox-nojs",
		Title:     "Combobox no-JS (P50)",
		Section:   SectionPrimitive,
		Primitive: "P50",
		Profile:   goth.StylesOnly,
		Body:      comboboxNoJSSpecimen,
	})

	// Command (P51), client filter, Interactive: an always-visible grouped palette;
	// the arrow keys loop the active item and Enter activates it.
	r.Register(Specimen{
		ID:        "primitive-command",
		Title:     "Command (P51)",
		Section:   SectionPrimitive,
		Primitive: "P51",
		Profile:   goth.Interactive,
		Body:      commandSpecimen,
	})

	// Command (P51) no-JS: the grouped list is fully visible and its items are real
	// links/buttons that navigate/submit with no JavaScript.
	r.Register(Specimen{
		ID:        "primitive-command-nojs",
		Title:     "Command no-JS (P51)",
		Section:   SectionPrimitive,
		Primitive: "P51",
		Profile:   goth.StylesOnly,
		Body:      commandNoJSSpecimen,
	})
}

func comboboxComponent(open bool, filter primitives.ComboboxFilter, inputAttrs templ.Attributes, options string) string {
	input := comp(primitives.ComboboxInput(primitives.ComboboxInputProps{
		Base:        primitives.Base{ID: "cb-input", Attributes: inputAttrs},
		Name:        "q",
		Placeholder: "Search fruits…",
		Listbox:     "cb-listbox",
	}))
	listbox := compKids(primitives.ComboboxListbox(primitives.ComboboxListboxProps{
		Base:  primitives.Base{ID: "cb-listbox"},
		Open:  open,
		Label: "Fruits",
	}), options)
	return compKids(primitives.Combobox(primitives.ComboboxProps{
		Base:   primitives.Base{ID: "cb"},
		Filter: filter,
		Open:   open,
	}), input+listbox)
}

func comboboxSpecimen() string {
	cb := comboboxComponent(false, primitives.ComboboxFilterClient, nil, comboboxOptions(comboboxFruits, ""))
	body := `<section data-slot="combobox-specimen">` +
		`<p>Type to filter; arrow keys move the active option; Enter selects it and submits the value.</p>` +
		`<form method="get" action="/combobox/pick" data-slot="combobox-form">` + cb + `</form>` +
		`</section>`
	return page("Combobox", body)
}

func comboboxAsyncSpecimen() string {
	// The input drives the async option seam with the typed htmx helper on
	// Base.Attributes (the frozen §9 merge rule). Since GOTH-5.3 the debounced
	// "input changed delay:150ms" trigger the combobox needs is a first-class
	// htmx.Trigger — the exact gap GOTH-5.2 recorded, now closed. The server owns
	// the filtered fragment and empty state.
	inputAttrs, _ := htmx.Attrs{
		Method:  htmx.MethodGet,
		URL:     "/combobox/options",
		Target:  "#cb-listbox",
		Swap:    htmx.SwapInnerHTML,
		Trigger: htmx.Trigger{Event: "input", Changed: true, Delay: 150 * time.Millisecond},
	}.Build()
	cb := comboboxComponent(false, primitives.ComboboxFilterServer, inputAttrs, comboboxOptions(comboboxFruits, ""))
	body := `<section data-slot="combobox-async-specimen">` +
		`<p>Typing fetches a server-filtered option list (async replacement); with no JavaScript the same query submits and reloads the filtered options.</p>` +
		`<form method="get" action="/combobox/pick" data-slot="combobox-form">` + cb + `</form>` +
		`</section>`
	return page("Combobox async", body)
}

func comboboxNoJSSpecimen() string {
	cb := comboboxComponent(true, primitives.ComboboxFilterClient, nil, comboboxOptions(comboboxFruits, ""))
	body := `<section data-slot="combobox-nojs-specimen">` +
		`<p>No JavaScript: the listbox is server-open and each option is a submit button — picking one POSTs the value.</p>` +
		`<form method="post" action="/combobox/pick" data-slot="combobox-form">` + cb + `</form>` +
		`</section>`
	return page("Combobox (no-JS)", body)
}

func commandComponent() string {
	input := comp(primitives.CommandInput(primitives.CommandInputProps{
		Base:        primitives.Base{ID: "cmd-input"},
		Name:        "q",
		Placeholder: "Type a command…",
		Listbox:     "cmd-list",
	}))

	fileGroup := compKids(primitives.CommandGroup(primitives.CommandGroupProps{Heading: "Files", HeadingID: "cmd-files"}),
		compKids(primitives.CommandItem(primitives.CommandItemProps{Name: "cmd", Value: "new-file"}), "New file")+
			compKids(primitives.CommandItem(primitives.CommandItemProps{Name: "cmd", Value: "open-file"}), "Open file"))
	navGroup := compKids(primitives.CommandGroup(primitives.CommandGroupProps{Heading: "Navigation", HeadingID: "cmd-nav"}),
		compKids(primitives.CommandItem(primitives.CommandItemProps{URL: mustURL("/command/run?cmd=settings"), Value: "settings"}), "Go to settings")+
			compKids(primitives.CommandItem(primitives.CommandItemProps{URL: mustURL("/command/run?cmd=profile"), Value: "profile"}), "Go to profile"))
	sep := comp(primitives.CommandSeparator(primitives.CommandSeparatorProps{}))
	empty := compKids(primitives.CommandEmpty(primitives.CommandEmptyProps{}), "No commands found.")

	list := compKids(primitives.CommandList(primitives.CommandListProps{
		Base:  primitives.Base{ID: "cmd-list"},
		Label: "Commands",
	}), fileGroup+sep+navGroup+empty)

	return compKids(primitives.Command(primitives.CommandProps{
		Base:   primitives.Base{ID: "cmd"},
		Filter: primitives.ComboboxFilterClient,
	}), input+list)
}

func commandSpecimen() string {
	body := `<section data-slot="command-specimen">` +
		`<p>An always-visible palette: type to filter, arrow keys loop the active item, Enter runs it.</p>` +
		`<form method="get" action="/command/run" data-slot="command-form">` + commandComponent() + `</form>` +
		`</section>`
	return page("Command", body)
}

func commandNoJSSpecimen() string {
	body := `<section data-slot="command-nojs-specimen">` +
		`<p>No JavaScript: the grouped list is fully visible and its items are real links and submit buttons.</p>` +
		`<form method="get" action="/command/run" data-slot="command-form">` + commandComponent() + `</form>` +
		`</section>`
	return page("Command (no-JS)", body)
}

// registerPaletteFixtures wires the server-owned round-trip routes. The server owns
// the option data, the async filtering, and the selection echo — the primitives
// never filter or parse.
func (s *Server) registerPaletteFixtures() {
	// /combobox/options is the async option seam: it returns a freshly filtered
	// option fragment (server-owned filtering + empty state) for the HTMX swap and a
	// full document on a direct request (the no-JS reload equivalent).
	s.handler.Handle(http.MethodGet, "/combobox/options", func(w http.ResponseWriter, r *http.Request) {
		list := filterFruits(r.URL.Query().Get("q"))
		fragment := comboboxOptions(list, "")
		if htmx.FromRequest(r).IsHTMX() {
			writeFragment(w, http.StatusOK, fragment)
			return
		}
				bundle := s.bundles[goth.Full]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, true))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		body := `<main data-slot="combobox-options-page"><h1>Options</h1><div data-slot="listbox" role="listbox" aria-label="Fruits">` +
			fragment + `</div><p><a href="/specimen/primitive-combobox-async">Back</a></p></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — combobox options"}, rawComponent(body))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})

	// /combobox/pick echoes the selected fruit. It accepts GET (the client/no-JS GET
	// form) and POST (the no-JS form-POST specimen), proving the form value round-trips.
	comboboxPick := func(w http.ResponseWriter, r *http.Request) {
				bundle := s.bundles[goth.StylesOnly]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, false))
		w.Header().Set("X-Content-Type-Options", "nosniff")

		value := r.FormValue("fruit")
		var echo string
		if value != "" {
			echo = `<p data-slot="combobox-echo" role="status">Selected: ` + templ.EscapeString(value) + `</p>`
		}
		body := `<main data-slot="combobox-pick"><h1>Combobox</h1>` + echo +
			`<p><a href="/specimen/primitive-combobox">Back</a></p></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — combobox pick"}, rawComponent(body))
		web.Render(r.Context(), w, http.StatusOK, doc)
	}
	s.handler.Handle(http.MethodGet, "/combobox/pick", comboboxPick)
	s.handler.Handle(http.MethodPost, "/combobox/pick", comboboxPick)

	// /command/run echoes the run command (a link's ?cmd or a submit button's field).
	s.handler.Handle(http.MethodGet, "/command/run", func(w http.ResponseWriter, r *http.Request) {
				bundle := s.bundles[goth.StylesOnly]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, false))
		w.Header().Set("X-Content-Type-Options", "nosniff")

		value := r.URL.Query().Get("cmd")
		var echo string
		if value != "" {
			echo = `<p data-slot="command-echo" role="status">Ran: ` + templ.EscapeString(value) + `</p>`
		}
		body := `<main data-slot="command-run"><h1>Command</h1>` + echo +
			`<p><a href="/specimen/primitive-command">Back</a></p></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — command run"}, rawComponent(body))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})
}

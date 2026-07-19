package cms

import (
	"strconv"
	"strings"

	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// stubViews is a package-local marker implementation of the Views port for the
// handler tests. Its renderers emit just the data/markers each handler test
// asserts — the real bundled chrome is tested in features/cms/views/goth. It is
// the seam a host would fill; embedding it and overriding one method is the
// blessed partial-override path (see TestViews_HostOverridesHome).
type stubViews struct{}

var _ Views = stubViews{}

func (stubViews) Home(_ []menus.MenuItem, _ []ListItem) web.Renderer {
	return stringRenderer("STUB-HOME")
}

func (stubViews) Archive(heading string, _ []menus.MenuItem, _ []ListItem, _ Pager) web.Renderer {
	return stringRenderer("STUB-ARCHIVE:" + heading)
}

func (stubViews) Single(title, _ string, _ []menus.MenuItem, _ web.Renderer) web.Renderer {
	return stringRenderer("STUB-SINGLE:" + title)
}

func (stubViews) Error(status int, message string) web.Renderer {
	return stringRenderer("STUB-ERROR:" + strconv.Itoa(status) + ":" + message)
}

func (stubViews) ContactForm(m ContactModel) web.Renderer {
	return stringRenderer(`STUB-CONTACT name="message" ` + m.FormError)
}

func (stubViews) ContactThanks() web.Renderer { return stringRenderer("Thanks") }

func (stubViews) MenuNav(_ menus.Menu, items []menus.MenuItem) web.Renderer {
	var b strings.Builder
	b.WriteString("STUB-NAV")
	for _, it := range items {
		b.WriteString(" " + it.Label)
	}
	return stringRenderer(b.String())
}

func (stubViews) EntriesList(heading, _, _ string, _ []EntryListItem, _ Pager) web.Renderer {
	return stringRenderer("STUB-ENTRIES:" + heading)
}

func (stubViews) EntriesListContent(heading, _, _ string, _ []EntryListItem, _ Pager) web.Renderer {
	return stringRenderer("STUB-ENTRIES-CONTENT:" + heading)
}

func (stubViews) EntryForm(m EntryFormModel) web.Renderer {
	return stringRenderer("STUB-ENTRYFORM:" + m.Heading)
}

func (stubViews) TermsList(_, _ []taxonomy.Term) web.Renderer { return stringRenderer("STUB-TERMS") }

func (stubViews) TermForm(m TermFormModel) web.Renderer {
	return stringRenderer("STUB-TERMFORM:" + m.Heading)
}

func (stubViews) MenusList(ms []menus.Menu) web.Renderer {
	var b strings.Builder
	b.WriteString("STUB-MENUS")
	for _, m := range ms {
		b.WriteString(" " + m.Name)
	}
	return stringRenderer(b.String())
}

func (stubViews) MenuNew(formError string) web.Renderer {
	return stringRenderer("STUB-MENUNEW " + formError)
}

func (stubViews) MenuDetail(m menus.Menu, _ []menus.MenuItem) web.Renderer {
	return stringRenderer("STUB-MENUDETAIL:" + m.Name)
}

func (stubViews) MenuItemForm(it menus.MenuItem) web.Renderer {
	return stringRenderer("STUB-MENUITEM:" + it.Label)
}

func (stubViews) MediaLibrary(assets []media.Asset, formError string) web.Renderer {
	var b strings.Builder
	b.WriteString(`STUB-MEDIA enctype="multipart/form-data" `)
	for _, a := range assets {
		b.WriteString(a.Filename + " ")
	}
	b.WriteString(formError)
	return stringRenderer(b.String())
}

func (stubViews) InquiriesList(_ []messaging.Inquiry) web.Renderer {
	return stringRenderer("STUB-INQUIRIES")
}

func (stubViews) AdminError(status int, message string) web.Renderer {
	return stringRenderer("STUB-ADMINERROR:" + strconv.Itoa(status) + ":" + message)
}

func (stubViews) SeedTemplates() []content.TemplateBinding { return nil }

package cms

import internalhttp "github.com/gopernicus/gopernicus/features/cms/internal/inbound/http"

// Views is the CMS feature's HTML rendering port (FS3). Public type alias of the
// transport-package interface (A1 precedent), so hosts implement/override it
// without importing internal/. The bundled default lives in the sibling module
// features/cms/views/templ; the blessed customization path is embedding that
// concrete default and overriding individual methods. A nil Config.Views means
// the HTML surface is not registered (only the media byte endpoint mounts).
type Views = internalhttp.Views

// View models re-exported from the transport package so hosts can build listings
// and forms without importing internal/ (A1 precedent).
type (
	ListItem       = internalhttp.ListItem
	EntryListItem  = internalhttp.EntryListItem
	EntryFormModel = internalhttp.EntryFormModel
	FieldInput     = internalhttp.FieldInput
	SelectOption   = internalhttp.SelectOption
	TermChoice     = internalhttp.TermChoice
	TermFormModel  = internalhttp.TermFormModel
	ContactModel   = internalhttp.ContactModel
)

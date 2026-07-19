package cms

import inbound "github.com/gopernicus/gopernicus/features/cms/internal/inbound/cms"

// Views is the CMS feature's HTML rendering port (FS3). Public type alias of the
// transport-package interface (A1 precedent), so hosts implement/override it
// without importing internal/. The bundled default lives in the sibling module
// features/cms/views/goth (rendered through ui/goth); the blessed customization
// path is embedding that concrete default and overriding individual methods. A nil
// Config.Views means the HTML surface is not registered (only the media byte
// endpoint mounts).
type Views = inbound.Views

// View models re-exported from the transport package so hosts can build listings
// and forms without importing internal/ (A1 precedent).
type (
	ListItem       = inbound.ListItem
	EntryListItem  = inbound.EntryListItem
	Pager          = inbound.Pager
	EntryFormModel = inbound.EntryFormModel
	FieldInput     = inbound.FieldInput
	SelectOption   = inbound.SelectOption
	TermChoice     = inbound.TermChoice
	TermFormModel  = inbound.TermFormModel
	ContactModel   = inbound.ContactModel
)

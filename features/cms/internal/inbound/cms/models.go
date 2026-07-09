package cms

// ListItem is one entry in a public listing (home / archive). The handler
// pre-computes Href from the type's route scheme so the view stays type-blind.
type ListItem struct {
	Title   string
	Href    string
	Excerpt string
}

// ContactModel holds the contact form values + an optional error.
type ContactModel struct {
	Name      string
	Email     string
	Message   string
	FormError string
}

// TermChoice is a taxonomy term checkbox in the entry form.
type TermChoice struct {
	ID      string
	Label   string // e.g. "category: News" / "tag: go"
	Checked bool
}

// TermFormModel carries the values for the taxonomy term form.
type TermFormModel struct {
	Heading   string
	Action    string
	Kind      string
	Name      string
	ParentID  string
	FormError string
}

// SelectOption is one <option> in a form dropdown (parent picker, template
// picker, relation/image selectors).
type SelectOption struct {
	Value    string
	Label    string
	Selected bool
}

// FieldInput is one custom-field input in the generated entry editor. Kind
// governs which control renders (text→input, richtext→textarea, number→number,
// bool→checkbox, date→date, image/relation→select). Options is populated for
// image (media assets) and relation (entries of RelTo).
type FieldInput struct {
	Key      string
	Label    string
	Kind     string
	Help     string
	Required bool
	Value    string
	Checked  bool // bool kind
	Options  []SelectOption
}

// EntryListItem is one row in the admin entry list.
type EntryListItem struct {
	ID     string
	Title  string
	Slug   string
	Status string
}

// Pager carries a list view's bidirectional pagination controls: the forward
// cursor (NextCursor → "Older →") and the reverse-probe prev state (HasPrev +
// PreviousCursor → "← Newer"), plus the active order carried in the pagination
// links and the list's base URL. When HasPrev is true and PreviousCursor is
// empty, the previous page is the first page, so the "← Newer" link targets
// BaseHref (the bare list URL). Order is empty for the default sort. The zero
// value is a single, unpaged first page (no links).
type Pager struct {
	NextCursor     string
	HasPrev        bool
	PreviousCursor string
	Order          string // active order, carried in the pagination links ("" = default)
	BaseHref       string // the list's base URL — the first-page target
}

// EntryFormModel drives the generic create/edit entry editor: the spine inputs
// plus one FieldInput per the type's FieldDefs. It is rendered by EntryForm and
// is type-agnostic — the handler fills it from the registered ContentType.
type EntryFormModel struct {
	Heading      string
	Action       string
	Title        string
	Excerpt      string
	Body         string
	Author       string
	Status       string
	Hierarchical bool
	ParentID     string
	MenuOrder    int
	Parents      []SelectOption // hierarchical parent picker
	Templates    []SelectOption // template picker
	Fields       []FieldInput   // custom fields
	Terms        []TermChoice
	FormError    string
}

package showcase

import (
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// GOTH-6.1 Attachment (P55) specimens. Two pages, both StylesOnly (the primitive
// binds no controller and needs no JavaScript):
//
//   - primitive-attachment: a static gallery proving media (icon + image),
//     every upload state (idle/uploading/processing/error/done) with meaning
//     beyond color, the three sizes, the two orientations, a scrolling group, and
//     the full-card trigger with independently focusable actions.
//   - primitive-attachment-upload: a REAL multipart no-JS upload round-trip. The
//     host owns the file <input>, the /attachment/upload route, and the in-memory
//     store; the SERVER decides each attachment's state (done, or error with an
//     accessible description when a file exceeds the demo limit). The Attachment
//     primitive only DISPLAYS that server-owned state — it selects no file and owns
//     no route, storage, progress, retries, or authorization.

// uploadedAttachment is one host-owned, server-decided upload record. The primitive
// never holds this; the showcase host does.
type uploadedAttachment struct {
	Name  string
	Size  int64
	State primitives.AttachmentState
	Note  string
}

// attachmentStore is the showcase's in-memory upload store (host-owned). It is a
// demo stand-in for real storage; it caps its length so repeated e2e runs across
// three engines do not grow it without bound.
type attachmentStore struct {
	mu    sync.Mutex
	items []uploadedAttachment
}

const attachmentStoreCap = 6

// attachmentMaxBytes is the demo upload limit. A larger file is rejected into the
// error state with an accessible description — the server owns that decision.
const attachmentMaxBytes = 512 * 1024

func (s *attachmentStore) add(a uploadedAttachment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, a)
	if len(s.items) > attachmentStoreCap {
		s.items = s.items[len(s.items)-attachmentStoreCap:]
	}
}

func (s *attachmentStore) list() []uploadedAttachment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uploadedAttachment{}, s.items...)
}

func registerAttachmentSpecimens(r *Registry) {
	r.Register(Specimen{
		ID:        "primitive-attachment",
		Title:     "Attachment (P55)",
		Section:   SectionPrimitive,
		Primitive: "P55",
		Profile:   goth.StylesOnly,
		Body:      attachmentGallerySpecimen,
	})
	r.Register(Specimen{
		ID:        "primitive-attachment-upload",
		Title:     "Attachment upload — real multipart no-JS (P55)",
		Section:   SectionPrimitive,
		Primitive: "P55",
		Profile:   goth.StylesOnly,
		Body:      attachmentUploadSpecimen,
	})
}

// fileGlyph is a decorative file-type icon for the media slot.
const fileGlyph = `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v5h5"/></svg>`

const trashGlyph = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>`

// attachmentCard renders one Attachment composing media, content (title +
// description metadata), and an actions slot.
func attachmentCard(p primitives.AttachmentProps, mediaHTML, title, desc, actionsHTML string) string {
	media := compKids(primitives.AttachmentMedia(primitives.AttachmentMediaProps{}), mediaHTML)
	content := compKids(primitives.AttachmentContent(primitives.AttachmentContentProps{}),
		compKids(primitives.AttachmentTitle(primitives.AttachmentTitleProps{}), templ.EscapeString(title))+
			compKids(primitives.AttachmentDescription(primitives.AttachmentDescriptionProps{}), templ.EscapeString(desc)))
	inner := media + content
	if actionsHTML != "" {
		inner += compKids(primitives.AttachmentActions(primitives.AttachmentActionsProps{}), actionsHTML)
	}
	return compKids(primitives.Attachment(p), inner)
}

func attachmentGallerySpecimen() string {
	var b strings.Builder

	// States: each carries a glyph + text label (meaning beyond color). The error
	// card's label is its accessible description.
	b.WriteString(`<h2>Upload states</h2><section data-slot="attachment-states">`)
	b.WriteString(attachmentCard(primitives.AttachmentProps{State: primitives.AttachmentIdle},
		fileGlyph, "budget.xlsx", "24 KB · Spreadsheet", ""))
	b.WriteString(attachmentCard(primitives.AttachmentProps{State: primitives.AttachmentUploading, Progress: 60, StatusLabel: "Uploading 60%"},
		fileGlyph, "keynote.pdf", "3.1 MB · PDF", ""))
	b.WriteString(attachmentCard(primitives.AttachmentProps{State: primitives.AttachmentProcessing},
		fileGlyph, "recording.mp4", "58 MB · Video", ""))
	b.WriteString(attachmentCard(primitives.AttachmentProps{State: primitives.AttachmentError, StatusLabel: "Upload failed: file too large"},
		fileGlyph, "archive.zip", "1.2 GB · Archive", ""))
	b.WriteString(attachmentCard(primitives.AttachmentProps{State: primitives.AttachmentDone, StatusLabel: "Uploaded"},
		fileGlyph, "logo.svg", "8 KB · Image", ""))
	b.WriteString(`</section>`)

	// Media: an icon card and an image-preview card.
	image := `<img src="/sample-preview.png" alt="Preview of cover.png">`
	b.WriteString(`<h2>Media (icon and image)</h2><section data-slot="attachment-media-samples">`)
	b.WriteString(attachmentCard(primitives.AttachmentProps{State: primitives.AttachmentDone, StatusLabel: "Uploaded"},
		fileGlyph, "notes.txt", "2 KB · Text", ""))
	b.WriteString(attachmentCard(primitives.AttachmentProps{State: primitives.AttachmentDone, StatusLabel: "Uploaded"},
		image, "cover.png", "640 KB · Image", ""))
	b.WriteString(`</section>`)

	// Sizes.
	b.WriteString(`<h2>Sizes</h2><section data-slot="attachment-sizes">`)
	for _, sz := range []struct {
		size  primitives.AttachmentSize
		label string
	}{{primitives.AttachmentSmall, "small.txt"}, {primitives.AttachmentMedium, "medium.txt"}, {primitives.AttachmentLarge, "large.txt"}} {
		b.WriteString(attachmentCard(primitives.AttachmentProps{Size: sz.size, State: primitives.AttachmentDone, StatusLabel: "Uploaded"},
			fileGlyph, sz.label, "12 KB · Text", ""))
	}
	b.WriteString(`</section>`)

	// Orientations.
	b.WriteString(`<h2>Orientations</h2><section data-slot="attachment-orientations">`)
	b.WriteString(attachmentCard(primitives.AttachmentProps{Orientation: primitives.AttachmentHorizontal, State: primitives.AttachmentDone, StatusLabel: "Uploaded"},
		fileGlyph, "horizontal.pdf", "Media beside content", ""))
	b.WriteString(attachmentCard(primitives.AttachmentProps{Orientation: primitives.AttachmentVertical, State: primitives.AttachmentDone, StatusLabel: "Uploaded"},
		image, "vertical.png", "Media stacked above content", ""))
	b.WriteString(`</section>`)

	// Full-card trigger with independently focusable actions. The trigger opens the
	// file; the trailing action (remove) stays a separate tab stop above the trigger.
	remove := compKids(primitives.Button(primitives.ButtonProps{
		Variant: primitives.ButtonGhost, Size: primitives.ButtonIcon,
		Label: "Remove report.pdf", LeadingIcon: templ.Raw(trashGlyph),
	}), "")
	triggerCard := compKids(primitives.Attachment(primitives.AttachmentProps{State: primitives.AttachmentDone, StatusLabel: "Uploaded"}),
		comp(primitives.AttachmentTrigger(primitives.AttachmentTriggerProps{
			URL:   mustURL("/specimen/primitive-attachment"),
			Label: "Open report.pdf",
		}))+
			compKids(primitives.AttachmentMedia(primitives.AttachmentMediaProps{}), fileGlyph)+
			compKids(primitives.AttachmentContent(primitives.AttachmentContentProps{}),
				compKids(primitives.AttachmentTitle(primitives.AttachmentTitleProps{}), "report.pdf")+
					compKids(primitives.AttachmentDescription(primitives.AttachmentDescriptionProps{}), "820 KB · PDF"))+
			compKids(primitives.AttachmentActions(primitives.AttachmentActionsProps{}), remove))
	b.WriteString(`<h2>Full-card trigger + independent actions</h2>`)
	b.WriteString(`<section data-slot="attachment-trigger-sample">` + triggerCard + `</section>`)

	// Group: a horizontally scrolling shelf of cards.
	var groupCards strings.Builder
	for i := 1; i <= 6; i++ {
		groupCards.WriteString(attachmentCard(primitives.AttachmentProps{State: primitives.AttachmentDone, StatusLabel: "Uploaded"},
			fileGlyph, "file-"+strconv.Itoa(i)+".dat", strconv.Itoa(i*7)+" KB · Data", ""))
	}
	group := compKids(primitives.AttachmentGroup(primitives.AttachmentGroupProps{Label: "Recent attachments"}), groupCards.String())
	b.WriteString(`<h2>Group (horizontal scroll)</h2>` + group)

	return page("Attachment", `<section data-slot="attachment-specimen">`+b.String()+`</section>`)
}

// attachmentUploadSpecimen renders the real no-JS upload form plus the live group
// of server-decided attachments read from the host-owned in-memory store.
func attachmentUploadSpecimen() string {
	var b strings.Builder
	b.WriteString(`<p>Choose a file and press Upload. No JavaScript runs: the browser posts a real multipart form to <code>/attachment/upload</code>, the SERVER decides each attachment's state (a file over 512&nbsp;KB is rejected into the error state with a description), and this page re-renders the Attachment cards from that server-owned state.</p>`)

	// Host-owned native file input + submit — the primitive owns none of this.
	b.WriteString(`<form method="post" action="/attachment/upload" enctype="multipart/form-data" data-slot="attachment-upload-form">`)
	b.WriteString(`<label for="att-file" data-slot="label" class="goth-label">File</label>`)
	b.WriteString(`<input id="att-file" type="file" name="file" data-slot="attachment-file-input">`)
	b.WriteString(compKids(primitives.Button(primitives.ButtonProps{Type: primitives.ButtonTypeSubmit}), "Upload"))
	b.WriteString(`</form>`)

	items := defaultAttachmentStore.list()
	b.WriteString(`<h2>Uploaded</h2>`)
	if len(items) == 0 {
		b.WriteString(`<p data-slot="attachment-upload-empty">No files uploaded yet.</p>`)
	} else {
		var cards strings.Builder
		for _, it := range items {
			label := "Uploaded"
			if it.State == primitives.AttachmentError {
				label = it.Note
			}
			cards.WriteString(attachmentCard(primitives.AttachmentProps{State: it.State, StatusLabel: label},
				fileGlyph, it.Name, humanSize(it.Size), ""))
		}
		b.WriteString(compKids(primitives.AttachmentGroup(primitives.AttachmentGroupProps{
			Label: "Uploaded files",
			Base:  primitives.Base{Class: "goth-attachment-group-stack"},
		}), cards.String()))
	}

	return page("Attachment upload", `<section data-slot="attachment-upload-specimen">`+b.String()+`</section>`)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatInt(n/(1<<20), 10) + " MB"
	case n >= 1<<10:
		return strconv.FormatInt(n/(1<<10), 10) + " KB"
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}

// registerAttachmentFixtures wires the real multipart no-JS upload round-trip. The
// host owns storage and the server decides the state; POST stores the record and
// redirects (303) back to the specimen, which re-renders from the server-owned
// store — a full-document round-trip with no JavaScript.
func (s *Server) registerAttachmentFixtures() {
	s.handler.Handle(http.MethodPost, "/attachment/upload", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(attachmentMaxBytes + (1 << 20)); err != nil {
			http.Error(w, "invalid multipart form", http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err == nil {
			defer file.Close()
			rec := uploadedAttachment{Name: header.Filename, Size: header.Size, State: primitives.AttachmentDone}
			if header.Size > attachmentMaxBytes {
				rec.State = primitives.AttachmentError
				rec.Note = "Upload failed: file exceeds 512 KB"
			}
			defaultAttachmentStore.add(rec)
		}
		http.Redirect(w, r, "/specimen/primitive-attachment-upload", http.StatusSeeOther)
	})
}

// defaultAttachmentStore is the host-owned in-memory upload store the specimen and
// the /attachment/upload fixture share. The redirect target after a POST is the
// ordinary specimen GET handler (full-document render under the strict CSP).
var defaultAttachmentStore = &attachmentStore{}

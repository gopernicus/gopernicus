package content

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// stubRenderer is a trivial web.Renderer used to prove template resolution.
type stubRenderer string

func (s stubRenderer) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, string(s))
	return err
}

func productType() ContentType {
	return ContentType{
		Slug: "product", Singular: "Product", Plural: "Products", Routable: true,
		Templates: []string{"default", "fancy"},
		Fields: []FieldDef{
			{Key: "subtitle", Kind: KindText},
			{Key: "price", Kind: KindNumber, Required: true},
			{Key: "hero", Kind: KindImage},
			{Key: "related", Kind: KindRelation, RelTo: "product"},
		},
	}
}

func TestRegistry_RegisterAndType(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(productType()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := r.Type("product"); !ok {
		t.Fatal("Type(product) not found after Register")
	}
	if got := r.Types(); len(got) != 1 || got[0].Slug != "product" {
		t.Fatalf("Types() = %+v, want one product", got)
	}

	// Duplicate slug rejected.
	if err := r.Register(productType()); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Fatalf("duplicate Register err = %v, want ErrAlreadyExists", err)
	}
}

func TestRegistry_RegisterValidation(t *testing.T) {
	r := NewRegistry()
	tests := []struct {
		name string
		ct   ContentType
	}{
		{"no slug", ContentType{Singular: "X", Plural: "Xs"}},
		{"no names", ContentType{Slug: "x"}},
		{"bad kind", ContentType{Slug: "x", Singular: "X", Plural: "Xs", Fields: []FieldDef{{Key: "f", Kind: "bogus"}}}},
		{"relation no relto", ContentType{Slug: "x", Singular: "X", Plural: "Xs", Fields: []FieldDef{{Key: "f", Kind: KindRelation}}}},
		{"dup field", ContentType{Slug: "x", Singular: "X", Plural: "Xs", Fields: []FieldDef{{Key: "f", Kind: KindText}, {Key: "f", Kind: KindText}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := r.Register(tt.ct); !errors.Is(err, errs.ErrInvalidInput) {
				t.Fatalf("Register err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestRegistry_Templates(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(productType()); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterTemplate("product", "default", func(e Entry) web.Renderer {
		return stubRenderer("default:" + e.Title)
	}); err != nil {
		t.Fatalf("RegisterTemplate default: %v", err)
	}
	if err := r.RegisterTemplate("product", "fancy", func(e Entry) web.Renderer {
		return stubRenderer("fancy:" + e.Title)
	}); err != nil {
		t.Fatalf("RegisterTemplate fancy: %v", err)
	}

	// Unknown type.
	if err := r.RegisterTemplate("nope", "default", func(Entry) web.Renderer { return stubRenderer("") }); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("RegisterTemplate unknown type err = %v, want ErrNotFound", err)
	}
	// Undeclared template.
	if err := r.RegisterTemplate("product", "ghost", func(Entry) web.Renderer { return stubRenderer("") }); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("RegisterTemplate undeclared err = %v, want ErrInvalidInput", err)
	}
	// Nil func.
	if err := r.RegisterTemplate("product", "default", nil); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("RegisterTemplate nil err = %v, want ErrInvalidInput", err)
	}
}

func TestRegistry_Render(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(productType())
	_ = r.RegisterTemplate("product", "default", func(e Entry) web.Renderer { return stubRenderer("default:" + e.Title) })

	// Exact match.
	got, ok := r.Render(Entry{Type: "product", Template: "fancy", Title: "Widget"})
	if !ok {
		t.Fatal("Render fell through with no fallback bound")
	}
	if s := render(t, got); s != "default:Widget" {
		t.Fatalf("Render fallback = %q, want default:Widget", s)
	}

	// Unknown type → not ok.
	if _, ok := r.Render(Entry{Type: "ghost", Template: "default"}); ok {
		t.Fatal("Render(ghost) = ok, want false")
	}
}

func TestRegistry_ValidateFields(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(productType())

	t.Run("ok with coercion", func(t *testing.T) {
		out, err := r.ValidateFields("product", Fields{
			"subtitle": {Raw: "  hi  "},
			"price":    {Raw: "9.99"},
		})
		if err != nil {
			t.Fatalf("ValidateFields: %v", err)
		}
		if out["subtitle"].Raw != "hi" || out["subtitle"].Kind != KindText {
			t.Fatalf("subtitle = %+v", out["subtitle"])
		}
		if out["price"].Kind != KindNumber {
			t.Fatalf("price kind = %v", out["price"].Kind)
		}
	})

	t.Run("missing required", func(t *testing.T) {
		if _, err := r.ValidateFields("product", Fields{"subtitle": {Raw: "x"}}); !errors.Is(err, errs.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("unknown key", func(t *testing.T) {
		if _, err := r.ValidateFields("product", Fields{"price": {Raw: "1"}, "ghost": {Raw: "x"}}); !errors.Is(err, errs.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("bad number", func(t *testing.T) {
		if _, err := r.ValidateFields("product", Fields{"price": {Raw: "notanum"}}); !errors.Is(err, errs.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("unknown type", func(t *testing.T) {
		if _, err := r.ValidateFields("ghost", Fields{}); !errors.Is(err, errs.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestFields_Accessors(t *testing.T) {
	ts := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	f := Fields{
		"name":  {Kind: KindText, Raw: "Acme"},
		"count": {Kind: KindNumber, Raw: "42"},
		"price": {Kind: KindNumber, Raw: "9.5"},
		"on":    {Kind: KindBool, Raw: "true"},
		"when":  {Kind: KindDate, Raw: ts.Format(time.RFC3339)},
		"rel":   {Kind: KindRelation, Raw: "entry-1"},
		"img":   {Kind: KindImage, Raw: "asset-1"},
	}
	if f.String("name") != "Acme" {
		t.Errorf("String = %q", f.String("name"))
	}
	if f.Int("count") != 42 {
		t.Errorf("Int = %d", f.Int("count"))
	}
	if f.Float("price") != 9.5 {
		t.Errorf("Float = %v", f.Float("price"))
	}
	if !f.Bool("on") {
		t.Error("Bool = false")
	}
	if !f.Time("when").Equal(ts) {
		t.Errorf("Time = %v", f.Time("when"))
	}
	if f.Relation("rel") != "entry-1" {
		t.Errorf("Relation = %q", f.Relation("rel"))
	}
	if f.Image("img") != "asset-1" {
		t.Errorf("Image = %q", f.Image("img"))
	}
	// Missing keys → zero values.
	if f.String("nope") != "" || f.Int("nope") != 0 || f.Bool("nope") {
		t.Error("missing key did not return zero value")
	}
}

func TestNewEntry(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	e, err := NewEntry("article", "  Hello World  ", "ex", "body", "me", StatusPublished, "", now)
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	if e.Type != "article" || e.Slug != "hello-world" || e.Title != "Hello World" {
		t.Fatalf("entry = %+v", e)
	}
	if e.Template != "default" {
		t.Fatalf("template = %q, want default", e.Template)
	}
	if e.PublishedAt == nil || !e.PublishedAt.Equal(now) {
		t.Fatalf("PublishedAt = %v, want %v", e.PublishedAt, now)
	}
	if e.Fields == nil {
		t.Fatal("Fields not initialized")
	}

	// Empty title rejected.
	if _, err := NewEntry("article", "   ", "", "", "", StatusDraft, "", now); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("empty title err = %v, want ErrInvalidInput", err)
	}
}

// render runs a Renderer to a string for assertions.
func render(t *testing.T, r web.Renderer) string {
	t.Helper()
	var b struct{ s string }
	pw := writerFunc(func(p []byte) (int, error) { b.s += string(p); return len(p), nil })
	if err := r.Render(context.Background(), pw); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return b.s
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

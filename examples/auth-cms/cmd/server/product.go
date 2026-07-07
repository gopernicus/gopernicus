package main

import (
	"context"
	"html/template"
	"io"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// productType is a host-registered custom content type — the whole point of the
// Registry model. A "Product" carries a subtitle and a (required) price beyond
// the spine, declared as data here. Registering it needs NO database migration:
// products are content.Entry rows of type "product" with their custom fields in
// entry_fields (here, the in-memory store's field bag).
func productType() content.ContentType {
	return content.ContentType{
		Slug:      "product",
		Singular:  "Product",
		Plural:    "Products",
		Routable:  true,
		Templates: []string{"default"},
		Fields: []content.FieldDef{
			{Key: "subtitle", Label: "Subtitle", Kind: content.KindText},
			{Key: "price", Label: "Price (USD)", Kind: content.KindNumber, Required: true},
		},
	}
}

// productBinding binds a dev-authored template to the product type. The renderer
// reads custom fields by key (the typed edge) — e.Fields.String/Float.
func productBinding() cms.TemplateBinding {
	return cms.TemplateBinding{
		Type:     "product",
		Template: "default",
		Fn:       func(e content.Entry) web.Renderer { return productRenderer{e} },
	}
}

// productTmpl is the host's product page markup.
var productTmpl = template.Must(template.New("product").Parse(
	`<article><h1>{{.Title}}</h1>` +
		`{{if .Subtitle}}<p><em>{{.Subtitle}}</em></p>{{end}}` +
		`<p><strong>${{printf "%.2f" .Price}}</strong></p>` +
		`<div>{{.Body}}</div></article>`))

// productRenderer renders a product entry through productTmpl.
type productRenderer struct{ e content.Entry }

func (p productRenderer) Render(_ context.Context, w io.Writer) error {
	return productTmpl.Execute(w, struct {
		Title    string
		Subtitle string
		Price    float64
		Body     string
	}{
		Title:    p.e.Title,
		Subtitle: p.e.Fields.String("subtitle"),
		Price:    p.e.Fields.Float("price"),
		Body:     p.e.Body,
	})
}

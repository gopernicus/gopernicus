package cms

import (
	"github.com/gopernicus/gopernicus/features/cms/internal/inbound/http/views"
	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// registerSeedTypes registers the two built-in content types every CMS ships
// with — Article and Page — exactly as WordPress ships `post` and `page`. They
// are registrations, not Go structs (plan locked decision 2): Article is a flat,
// routable blog type; Page is hierarchical and flat-routed at the site root.
func registerSeedTypes(r *content.Registry) error {
	if err := r.Register(content.ContentType{
		Slug:      "article",
		Singular:  "Article",
		Plural:    "Articles",
		Templates: []string{"default"},
		Routable:  true,
	}); err != nil {
		return err
	}
	return r.Register(content.ContentType{
		Slug:         "page",
		Singular:     "Page",
		Plural:       "Pages",
		Templates:    []string{"default"},
		Hierarchical: true,
		Routable:     true,
	})
}

// registerSeedTemplates binds the seed types' default per-entry renderers, ported
// from the former PublicPost/PublicPage views. A host may override either via
// Config.Templates (the per-type seam).
func registerSeedTemplates(r *content.Registry) error {
	if err := r.RegisterTemplate("article", "default", func(e content.Entry) web.Renderer {
		return views.ArticleContent(e)
	}); err != nil {
		return err
	}
	return r.RegisterTemplate("page", "default", func(e content.Entry) web.Renderer {
		return views.PageContent(e)
	})
}

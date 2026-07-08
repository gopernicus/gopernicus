package cms

import (
	"github.com/gopernicus/gopernicus/features/cms/logic/content"
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

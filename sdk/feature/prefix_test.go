package feature

import (
	"net/http"
	"testing"
)

// PrefixRegistrar must keep satisfying RouteRegistrar structurally — it is
// itself a valid Mount.Router value.
var _ RouteRegistrar = PrefixRegistrar{}

func TestPrefixRegistrar_Handle(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		path   string
		want   string
	}{
		{"prefix without trailing slash", "/blog", "/{$}", "/blog/{$}"},
		{"prefix with trailing slash", "/blog/", "/{$}", "/blog/{$}"},
		{"root prefix is a no-op", "/", "/{$}", "/{$}"},
		{"empty prefix is a no-op", "", "/{$}", "/{$}"},
		{"prefix missing leading slash is normalized", "blog", "/{$}", "/blog/{$}"},
		{"bare root path (subtree pattern)", "/blog", "/", "/blog/"},
		{"wildcard segments pass through untouched", "/blog", "/terms/{id}/edit", "/blog/terms/{id}/edit"},
		{"nested prefix", "/x/y", "/widgets", "/x/y/widgets"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := &recordingRegistrar{}
			p := PrefixRegistrar{Prefix: tt.prefix, Next: next}

			p.Handle(http.MethodGet, tt.path, func(w http.ResponseWriter, r *http.Request) {})

			want := "GET " + tt.want
			if len(next.calls) != 1 || next.calls[0] != want {
				t.Errorf("Handle(%q under prefix %q) recorded %v, want [%q]", tt.path, tt.prefix, next.calls, want)
			}
		})
	}
}

func TestPrefixRegistrar_MethodPassesThroughUnchanged(t *testing.T) {
	next := &recordingRegistrar{}
	p := PrefixRegistrar{Prefix: "/admin", Next: next}

	p.Handle(http.MethodPost, "/widgets/{id}/publish", func(w http.ResponseWriter, r *http.Request) {})

	want := "POST /admin/widgets/{id}/publish"
	if len(next.calls) != 1 || next.calls[0] != want {
		t.Errorf("calls = %v, want [%q]", next.calls, want)
	}
}

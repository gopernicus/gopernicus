package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouteGroup_PrefixedRoutes(t *testing.T) {
	h := NewWebHandler()
	api := h.Group("/api/v1")

	api.GET("/users", func(w http.ResponseWriter, r *http.Request) {
		RespondText(w, http.StatusOK, "users")
	})
	api.GET("/items", func(w http.ResponseWriter, r *http.Request) {
		RespondText(w, http.StatusOK, "items")
	})

	tests := []struct {
		path string
		want string
	}{
		{"/api/v1/users", "users"},
		{"/api/v1/items", "items"},
	}

	for _, tt := range tests {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))

		if w.Body.String() != tt.want {
			t.Errorf("GET %s: body = %q, want %q", tt.path, w.Body.String(), tt.want)
		}
	}
}

func TestRouteGroup_Middleware(t *testing.T) {
	h := NewWebHandler()

	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Auth", "checked")
			next.ServeHTTP(w, r)
		})
	}

	api := h.Group("/api", authMW)
	api.GET("/protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Public route outside group should not have auth middleware.
	h.GET("/public", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Check group route has middleware.
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, httptest.NewRequest("GET", "/api/protected", nil))
	if got := w1.Header().Get("X-Auth"); got != "checked" {
		t.Errorf("/api/protected: X-Auth = %q, want %q", got, "checked")
	}

	// Check public route does not.
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest("GET", "/public", nil))
	if got := w2.Header().Get("X-Auth"); got != "" {
		t.Errorf("/public: X-Auth = %q, want empty", got)
	}
}

func TestRouteGroup_NestedGroups(t *testing.T) {
	h := NewWebHandler()

	var order []string

	mw1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "v1")
			next.ServeHTTP(w, r)
		})
	}
	mw2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "admin")
			next.ServeHTTP(w, r)
		})
	}

	v1 := h.Group("/api/v1", mw1)
	admin := v1.Group("/admin", mw2)

	admin.GET("/stats", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		RespondText(w, http.StatusOK, "stats")
	})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/stats", nil))

	if w.Body.String() != "stats" {
		t.Errorf("body = %q, want %q", w.Body.String(), "stats")
	}

	if len(order) != 3 || order[0] != "v1" || order[1] != "admin" || order[2] != "handler" {
		t.Errorf("middleware order = %v, want [v1 admin handler]", order)
	}
}

func TestRouteGroup_AllMethods(t *testing.T) {
	h := NewWebHandler()
	g := h.Group("/api")

	g.GET("/r", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, "GET") })
	g.POST("/r", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, "POST") })
	g.PUT("/r", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, "PUT") })
	g.DELETE("/r", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, "DELETE") })
	g.PATCH("/r", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, "PATCH") })

	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(m, "/api/r", nil))
		if w.Body.String() != m {
			t.Errorf("%s /api/r: body = %q, want %q", m, w.Body.String(), m)
		}
	}
}

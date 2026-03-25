package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebHandler_BasicRoute(t *testing.T) {
	h := NewWebHandler()
	h.GET("/ping", func(w http.ResponseWriter, r *http.Request) {
		RespondText(w, http.StatusOK, "pong")
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ping", nil)
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "pong" {
		t.Errorf("body = %q, want %q", w.Body.String(), "pong")
	}
}

func TestWebHandler_GlobalMiddleware(t *testing.T) {
	h := NewWebHandler()

	// Middleware that adds a header.
	h.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Global", "applied")
			next.ServeHTTP(w, r)
		})
	})

	h.GET("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	h.ServeHTTP(w, r)

	if got := w.Header().Get("X-Global"); got != "applied" {
		t.Errorf("X-Global = %q, want %q", got, "applied")
	}
}

func TestWebHandler_PerRouteMiddleware(t *testing.T) {
	h := NewWebHandler()

	routeMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Route", "yes")
			next.ServeHTTP(w, r)
		})
	}

	h.GET("/with-mw", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, routeMW)

	h.GET("/without-mw", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Route with middleware.
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, httptest.NewRequest("GET", "/with-mw", nil))
	if got := w1.Header().Get("X-Route"); got != "yes" {
		t.Errorf("/with-mw: X-Route = %q, want %q", got, "yes")
	}

	// Route without middleware.
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest("GET", "/without-mw", nil))
	if got := w2.Header().Get("X-Route"); got != "" {
		t.Errorf("/without-mw: X-Route = %q, want empty", got)
	}
}

func TestWebHandler_MiddlewareOrder(t *testing.T) {
	h := NewWebHandler()

	var order []string

	h.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "first")
			next.ServeHTTP(w, r)
		})
	})
	h.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "second")
			next.ServeHTTP(w, r)
		})
	})

	h.GET("/order", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/order", nil))

	if len(order) != 3 || order[0] != "first" || order[1] != "second" || order[2] != "handler" {
		t.Errorf("order = %v, want [first second handler]", order)
	}
}

func TestWebHandler_HandleRaw(t *testing.T) {
	h := NewWebHandler()

	// Global middleware should NOT apply to HandleRaw routes.
	h.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Global", "applied")
			next.ServeHTTP(w, r)
		})
	})

	h.HandleRaw("GET /raw", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RespondText(w, http.StatusOK, "raw")
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/raw", nil))

	if got := w.Header().Get("X-Global"); got != "" {
		t.Errorf("X-Global = %q, want empty (HandleRaw bypasses middleware)", got)
	}
	if w.Body.String() != "raw" {
		t.Errorf("body = %q, want %q", w.Body.String(), "raw")
	}
}

func TestWebHandler_HTTPMethods(t *testing.T) {
	h := NewWebHandler()

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	for _, m := range methods {
		method := m
		switch method {
		case "GET":
			h.GET("/m", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, method) })
		case "POST":
			h.POST("/m", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, method) })
		case "PUT":
			h.PUT("/m", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, method) })
		case "DELETE":
			h.DELETE("/m", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, method) })
		case "PATCH":
			h.PATCH("/m", func(w http.ResponseWriter, r *http.Request) { RespondText(w, 200, method) })
		}
	}

	for _, m := range methods {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(m, "/m", nil))
		if w.Body.String() != m {
			t.Errorf("%s /m: body = %q, want %q", m, w.Body.String(), m)
		}
	}
}

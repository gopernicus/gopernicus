package web

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mark returns middleware that appends its name to order and stamps a response
// header, so a test can assert both that the middleware ran and where.
func mark(order *[]string, name string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*order = append(*order, name)
			w.Header().Set("X-"+name, "1")
			next.ServeHTTP(w, r)
		})
	}
}

func TestWebHandler_GlobalMiddlewareSeesMuxNotFound(t *testing.T) {
	var order []string
	h := NewWebHandler()
	h.Use(mark(&order, "global"))
	h.GET("/known", func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unknown", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("X-global"); got != "1" {
		t.Error("global middleware did not observe the mux-generated 404")
	}
}

func TestWebHandler_GlobalMiddlewareSeesMethodMismatch405WithAllow(t *testing.T) {
	var order []string
	h := NewWebHandler()
	h.Use(mark(&order, "global"))
	h.POST("/auth/login", func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	// The mux still routes, so its automatic Allow header survives the wrap —
	// which an unqualified catch-all registration would have destroyed (turning
	// the 405 into a 404).
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Errorf("Allow = %q, want POST", got)
	}
	if got := rec.Header().Get("X-global"); got != "1" {
		t.Error("global middleware did not observe the mux-generated 405")
	}
}

func TestWebHandler_GlobalMiddlewareSeesMuxRedirect(t *testing.T) {
	var order []string
	h := NewWebHandler()
	h.Use(mark(&order, "global"))
	h.GET("/dir/", func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dir", nil))

	if rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want a mux-generated redirect", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/dir/" {
		t.Errorf("Location = %q, want /dir/", got)
	}
	if got := rec.Header().Get("X-global"); got != "1" {
		t.Error("global middleware did not observe the mux-generated redirect")
	}
}

func TestWebHandler_GlobalMiddlewareWrapsHandleRaw(t *testing.T) {
	var order []string
	h := NewWebHandler()
	h.Use(mark(&order, "global"))
	h.HandleRaw("GET /openapi.json", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RespondText(w, http.StatusOK, "{}")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	if rec.Body.String() != "{}" {
		t.Fatalf("body = %q, want {}", rec.Body.String())
	}
	if got := rec.Header().Get("X-global"); got != "1" {
		t.Error("global middleware did not observe a HandleRaw registration")
	}
}

// TestWebHandler_PanicsRecoversRawHandler pins the semantic change: HandleRaw
// registers a raw handler/pattern, not a bypass of host policy.
func TestWebHandler_PanicsRecoversRawHandler(t *testing.T) {
	h := NewWebHandler()
	h.Use(Panics(discardLogger()))
	h.HandleRaw("GET /openapi.json", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (raw handler panic recovered globally)", rec.Code)
	}
}

// TestWebHandler_CORSPreflightForMethodQualifiedRoute is the wiring that used to
// fail silently: the obvious router.Use(CORSMiddleware(...)) now answers a
// preflight for a route registered as POST-only.
func TestWebHandler_CORSPreflightForMethodQualifiedRoute(t *testing.T) {
	h := NewWebHandler()
	h.Use(CORSMiddleware([]string{"https://app.example.com"}))
	h.POST("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("ACAO = %q, want echoed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("credentials = %q, want true", got)
	}
}

// TestWebHandler_NonPreflightOptionsReachesRoute proves the other half: an
// OPTIONS route stays reachable behind a globally installed CORS middleware.
func TestWebHandler_NonPreflightOptionsReachesRoute(t *testing.T) {
	h := NewWebHandler()
	h.Use(CORSMiddleware([]string{"https://app.example.com"}))
	h.Handle(http.MethodOptions, "/api/v1/things", func(w http.ResponseWriter, r *http.Request) {
		RespondText(w, http.StatusOK, "options")
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/api/v1/things", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "options" {
		t.Fatalf("status = %d body = %q, want 200 %q", rec.Code, rec.Body.String(), "options")
	}
}

func TestWebHandler_GlobalThenRouteMiddlewareOrder(t *testing.T) {
	var order []string
	h := NewWebHandler()
	h.Use(mark(&order, "g1"), mark(&order, "g2"))
	h.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}, mark(&order, "r1"), mark(&order, "r2"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	want := "g1,g2,r1,r2,handler"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// TestWebHandler_UseAfterRegistration documents the semantic change: global
// middleware wraps the mux, so registration order no longer matters.
func TestWebHandler_UseAfterRegistration(t *testing.T) {
	var order []string
	h := NewWebHandler()
	h.GET("/x", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})
	h.Use(mark(&order, "late"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	want := "late,handler"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// TestWebHandler_FlushThroughGlobalMiddleware guards the risk the whole-mux wrap
// introduces: a streaming handler must still reach the real writer's Flusher
// through the global stack's wrappers.
func TestWebHandler_FlushThroughGlobalMiddleware(t *testing.T) {
	h := NewWebHandler()
	h.Use(RequestID(), Logger(discardLogger()), Panics(discardLogger()))

	var sendErr error
	h.GET("/stream", func(w http.ResponseWriter, r *http.Request) {
		sw := NewStreamWriter(w)
		sendErr = sw.SendData("hello")
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if sendErr != nil {
		t.Fatalf("stream send through global middleware: %v", sendErr)
	}
	if !rec.Flushed {
		t.Error("response was never flushed through the global stack")
	}
	if !strings.Contains(rec.Body.String(), "data: hello") {
		t.Errorf("body = %q, want the streamed event", rec.Body.String())
	}
}

// TestWebHandler_HijackThroughGlobalMiddleware proves connection hijacking still
// reaches the real writer through the global stack.
func TestWebHandler_HijackThroughGlobalMiddleware(t *testing.T) {
	h := NewWebHandler()
	h.Use(RequestID(), Logger(discardLogger()), Panics(discardLogger()))

	var hijackErr error
	h.GET("/hijack", func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			hijackErr = err
			return
		}
		defer conn.Close()
		fmt.Fprint(buf, "HTTP/1.1 200 OK\r\nContent-Length: 8\r\n\r\nhijacked")
		buf.Flush()
	})

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/hijack")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if hijackErr != nil {
		t.Fatalf("hijack through global middleware: %v", hijackErr)
	}
	if string(body) != "hijacked" {
		t.Errorf("body = %q, want %q", body, "hijacked")
	}
}

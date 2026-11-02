package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// cloneRequest stands in for the logging, request-id and recovery middleware
// a host stacks between the span and the mux: it hands the next layer a copy.
func cloneRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), struct{}{}, "clone")))
	})
}

// routeSpans serves each request through a WebHandler built by stack and
// returns the ended spans' names and http.route values, in request order.
func routeSpans(t *testing.T, stack func(tr *Tracer) []web.Middleware, reqs ...*http.Request) (names, routes []string) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.Background())
	tracer, err := Open(context.Background(), Config{Exporter: ExporterProvider, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	h := web.NewWebHandler()
	h.Use(stack(tracer)...)
	h.Handle("GET", "/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "missing" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	for _, r := range reqs {
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	for _, span := range recorder.Ended() {
		names = append(names, span.Name())
		route := ""
		for _, a := range span.Attributes() {
			if a.Key == attribute.Key("http.route") {
				route = a.Value.AsString()
			}
		}
		routes = append(routes, route)
	}
	return names, routes
}

// TestRouteNamesUnderGlobalUse: with RouteNames as the last Use, a global span
// is named by the matched route; a 404 or 405 is "<METHOD> unmatched"; no
// name or route ever carries the request path.
func TestRouteNamesUnderGlobalUse(t *testing.T) {
	names, routes := routeSpans(t, func(tr *Tracer) []web.Middleware {
		return []web.Middleware{Middleware(tr, HTTPConfig{}), cloneRequest, RouteNames()}
	},
		httptest.NewRequest("GET", "/users/42", nil),
		httptest.NewRequest("GET", "/users/missing", nil),
		httptest.NewRequest("GET", "/nope/secret-token", nil),
		httptest.NewRequest("POST", "/users/42", nil),
	)
	want := []string{"GET /users/{id}", "GET /users/{id}", "GET unmatched", "POST unmatched"}
	wantRoutes := []string{"/users/{id}", "/users/{id}", "", ""}
	for i := range want {
		if names[i] != want[i] || routes[i] != wantRoutes[i] {
			t.Errorf("request %d: name %q route %q, want %q / %q", i, names[i], routes[i], want[i], wantRoutes[i])
		}
	}
	for i, n := range names {
		for _, leak := range []string{"42", "missing", "secret-token", "nope"} {
			if strings.Contains(n, leak) || strings.Contains(routes[i], leak) {
				t.Errorf("span %d leaks %q: %q / %q", i, leak, n, routes[i])
			}
		}
	}
}

// TestRouteNamesAbsentKeepsMethodName pins today's behavior: a global span
// without RouteNames is named by its method alone.
func TestRouteNamesAbsentKeepsMethodName(t *testing.T) {
	names, routes := routeSpans(t, func(tr *Tracer) []web.Middleware {
		return []web.Middleware{Middleware(tr, HTTPConfig{}), cloneRequest}
	}, httptest.NewRequest("GET", "/users/42", nil), httptest.NewRequest("GET", "/nope", nil))
	if names[0] != "GET" || names[1] != "GET" || routes[0] != "" || routes[1] != "" {
		t.Fatalf("names %v routes %v, want method-only", names, routes)
	}
}

// TestRouteNamesMisplaced: RouteNames above a cloning layer sees no pattern. A
// matched 200 keeps its method-only name and is never called unmatched.
func TestRouteNamesMisplaced(t *testing.T) {
	names, _ := routeSpans(t, func(tr *Tracer) []web.Middleware {
		return []web.Middleware{Middleware(tr, HTTPConfig{}), RouteNames(), cloneRequest}
	}, httptest.NewRequest("GET", "/users/42", nil))
	if names[0] != "GET" {
		t.Fatalf("misplaced RouteNames named a matched route %q, want GET", names[0])
	}
}

// TestRouteNamesWithoutMiddleware: alone, RouteNames neither panics nor
// touches the response.
func TestRouteNamesWithoutMiddleware(t *testing.T) {
	h := web.NewWebHandler()
	h.Use(RouteNames())
	h.Handle("GET", "/users/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/users/42", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d", rec.Code)
	}
}

// TestRouteMountedMiddlewareStillNamesByRoute: mounted per route, Middleware
// sees r.Pattern at start and needs no RouteNames.
func TestRouteMountedMiddlewareStillNamesByRoute(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.Background())
	tracer, err := Open(context.Background(), Config{Exporter: ExporterProvider, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	h := web.NewWebHandler()
	h.Handle("GET", "/users/{id}", func(w http.ResponseWriter, r *http.Request) {}, Middleware(tracer, HTTPConfig{}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users/42", nil))
	if got := recorder.Ended()[0].Name(); got != "GET /users/{id}" {
		t.Fatalf("route-mounted name = %q", got)
	}
}

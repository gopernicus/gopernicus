package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/events/internal/logic/hub"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
	"github.com/gopernicus/gopernicus/sdk/identity"
	"github.com/gopernicus/gopernicus/sdk/web"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stashIdentity mimics authentication.RequireUser: it puts a fixed Principal on
// the request context.
func stashIdentity(p identity.Principal) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), p)))
		})
	}
}

type gatewayServer struct {
	server *httptest.Server
	bus    sdkevents.Bus
}

// newGatewayServer builds a hub over a Memory bus, mounts the routes with the
// given middleware/authorize/cap, and returns the running server.
func newGatewayServer(t *testing.T, mw []web.Middleware, authorize func(context.Context, identity.Principal, string, string) (bool, error), maxPerSubject int) gatewayServer {
	t.Helper()
	bus := sdkevents.NewMemory()
	t.Cleanup(func() { _ = bus.Close(context.Background()) })

	h, err := hub.New(bus, hub.Config{Logger: discardLogger(), MaxConnsPerSubject: maxPerSubject})
	if err != nil {
		t.Fatalf("hub.New: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	router := web.NewWebHandler()
	Mount(router, Config{
		Hub:        h,
		Authorize:  authorize,
		Middleware: mw,
		// The root events.NewService applies these defaults; this test mounts the
		// http layer directly, so it supplies non-zero values itself (a zero
		// MaxConnAge would expire every stream immediately).
		Heartbeat:  25 * time.Second,
		MaxConnAge: time.Minute,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return gatewayServer{server: srv, bus: bus}
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// openStream opens a streaming GET, waits for headers, and returns a cancel that
// closes the connection.
func openStream(t *testing.T, url string) (int, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	status := resp.StatusCode
	go func() {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
	}()
	return status, cancel
}

func TestSubjectStream_NoIdentityFailsClosed(t *testing.T) {
	gw := newGatewayServer(t, nil, nil, 0)
	if status := getStatus(t, gw.server.URL+"/events"); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
}

func TestResourceStream_NoIdentityFailsClosed(t *testing.T) {
	authorize := func(context.Context, identity.Principal, string, string) (bool, error) { return true, nil }
	gw := newGatewayServer(t, nil, authorize, 0)
	if status := getStatus(t, gw.server.URL+"/events/entry/e1"); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
}

func TestResourceStream_AuthorizeDenied(t *testing.T) {
	mw := []web.Middleware{stashIdentity(identity.Principal{Type: identity.User, ID: "u1"})}
	authorize := func(context.Context, identity.Principal, string, string) (bool, error) { return false, nil }
	gw := newGatewayServer(t, mw, authorize, 0)
	if status := getStatus(t, gw.server.URL+"/events/entry/e1"); status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
}

func TestResourceStream_AuthorizeError(t *testing.T) {
	mw := []web.Middleware{stashIdentity(identity.Principal{Type: identity.User, ID: "u1"})}
	authorize := func(context.Context, identity.Principal, string, string) (bool, error) {
		return false, errors.New("boom")
	}
	gw := newGatewayServer(t, mw, authorize, 0)
	if status := getStatus(t, gw.server.URL+"/events/entry/e1"); status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
}

func TestResourceStream_ReceivesPrincipal(t *testing.T) {
	seen := make(chan identity.Principal, 1)
	mw := []web.Middleware{stashIdentity(identity.Principal{Type: identity.User, ID: "u1"})}
	authorize := func(_ context.Context, p identity.Principal, rt, rid string) (bool, error) {
		if rt != "entry" || rid != "e1" {
			t.Errorf("authorize got (%q,%q), want (entry,e1)", rt, rid)
		}
		seen <- p
		return true, nil
	}
	gw := newGatewayServer(t, mw, authorize, 0)
	status, cancel := openStream(t, gw.server.URL+"/events/entry/e1")
	defer cancel()
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	select {
	case p := <-seen:
		if p.Type != identity.User || p.ID != "u1" {
			t.Fatalf("authorize principal = %+v, want user/u1", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authorize was not called")
	}
}

func TestSubjectStream_PerSubjectCap(t *testing.T) {
	mw := []web.Middleware{stashIdentity(identity.Principal{Type: identity.User, ID: "u1"})}
	gw := newGatewayServer(t, mw, nil, 1)

	status, cancel := openStream(t, gw.server.URL+"/events")
	defer cancel()
	if status != http.StatusOK {
		t.Fatalf("first stream status = %d, want 200", status)
	}

	// The subject's one slot is taken; the next stream is rejected.
	if status := getStatus(t, gw.server.URL+"/events"); status != http.StatusTooManyRequests {
		t.Fatalf("second stream status = %d, want 429", status)
	}
}

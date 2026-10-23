package authenticationhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// keyRecordingLimiter admits every request and records each key it is asked.
type keyRecordingLimiter struct {
	mu   sync.Mutex
	keys []string
}

func (l *keyRecordingLimiter) Allow(_ context.Context, key string, _ ratelimiter.Limit) (ratelimiter.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	return ratelimiter.Result{Allowed: true}, nil
}

func (*keyRecordingLimiter) Reset(context.Context, string) error { return nil }

func (l *keyRecordingLimiter) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	keys := l.keys
	l.keys = nil
	return keys
}

// forwardedRequest is a request from peer whose proxy appended client to
// X-Forwarded-For.
func forwardedRequest(peer, client string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/host", nil)
	r.RemoteAddr = peer + ":4000"
	r.Header.Set("X-Forwarded-For", client)
	r.Header.Set("User-Agent", "probe/1.0")
	return r
}

// TestClientInfoMiddlewareStampsTheResolvedClient installs the exported stamp
// the way a host does — router.Use after the TrustProxies Use, the route
// registered before either — and proves it admits the request and stamps
// web.ClientIP when TrustProxies ran, the peer address otherwise, never the
// raw X-Forwarded-For.
func TestClientInfoMiddlewareStampsTheResolvedClient(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	adapter, err := NewAuthenticator(service.Service, environment.ModeDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		trusted int // -1: TrustProxies not wired
		want    string
	}{
		{"TrustProxies ran", 1, "198.51.100.23"},
		{"no TrustProxies", -1, "10.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ip, ua string
			router := web.NewWebHandler()
			router.Handle(http.MethodPost, "/host", func(w http.ResponseWriter, r *http.Request) {
				ip, ua = authlogic.ClientInfoFromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			})
			if tc.trusted >= 0 {
				router.Use(web.TrustProxies(tc.trusted))
			}
			router.Use(adapter.ClientInfoMiddleware())
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, forwardedRequest("10.0.0.1", "198.51.100.23"))
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204: the stamp admits every request", rec.Code)
			}
			if ip != tc.want || ua != "probe/1.0" {
				t.Fatalf("carrier = %q/%q, want %q/probe/1.0", ip, ua, tc.want)
			}
		})
	}
}

// TestNewAuthenticatorCarriesTheProxyPosture proves the posture is the
// adapter's, not the bundled route table's: a NewAuthenticator-built adapter
// in production WARNs once that TrustProxies is not wired and still stamps; a
// development one never warns; and New's routes share the adapter's posture,
// so one process logs the WARN once.
func TestNewAuthenticatorCarriesTheProxyPosture(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	t.Run("a NewAuthenticator-built adapter in production WARNs once", func(t *testing.T) {
		var logs bytes.Buffer
		adapter, err := NewAuthenticator(service.Service, environment.ModeProduction,
			WithCookies(CookieConfig{Secure: true}), WithLimiter(sharedHTTPLimiter{}),
			WithAuthenticatorLogger(slog.New(slog.NewTextHandler(&logs, nil))))
		if err != nil {
			t.Fatal(err)
		}
		var ip string
		next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ip, _ = authlogic.ClientInfoFromContext(r.Context()) })
		for range 3 {
			adapter.ClientInfoMiddleware()(next).ServeHTTP(httptest.NewRecorder(), forwardedRequest("203.0.113.7", ""))
		}
		if got := strings.Count(logs.String(), "level=WARN"); got != 1 || !strings.Contains(logs.String(), "web.TrustProxies is not wired") {
			t.Fatalf("want one TrustProxies WARN, got %d:\n%s", got, logs.String())
		}
		if ip != "203.0.113.7" {
			t.Fatalf("carrier IP = %q, want the peer 203.0.113.7", ip)
		}
	})
	t.Run("development never warns", func(t *testing.T) {
		var logs bytes.Buffer
		adapter, err := NewAuthenticator(service.Service, environment.ModeDevelopment,
			WithAuthenticatorLogger(slog.New(slog.NewTextHandler(&logs, nil))))
		if err != nil {
			t.Fatal(err)
		}
		adapter.ClientInfoMiddleware()(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), forwardedRequest("203.0.113.7", "198.51.100.23"))
		if adapter.proxies != nil || logs.Len() != 0 {
			t.Fatalf("development adapter carries a posture or logged:\n%s", logs.String())
		}
	})
	t.Run("New's routes reuse the adapter's posture", func(t *testing.T) {
		adapter, err := New(service.Service, environment.ModeProduction,
			WithAuthenticatorPolicy(AuthenticatorPolicy{Cookie: CookieConfig{Secure: true}, Limiter: sharedHTTPLimiter{}}))
		if err != nil {
			t.Fatal(err)
		}
		if adapter.proxies == nil || adapter.routes.Proxies != adapter.proxies {
			t.Fatal("the bundled routes do not share the adapter's proxy posture")
		}
	})
}

// TestPerIPBudgetsKeyPerClient proves RateLimitByIP and the OAuth2 per-IP
// budget key on the carrier's IP behind ClientInfoMiddleware and, without the
// stamp, on the TrustProxies-resolved or peer address — never one shared
// bucket.
func TestPerIPBudgetsKeyPerClient(t *testing.T) {
	service := newServiceWithFakes(authenticationFixture{})
	limiter := &keyRecordingLimiter{}
	service.limiter = limiter
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	limited := service.RateLimitByIP("public", 10)(ok)
	for _, tc := range []struct {
		name    string
		handler http.Handler
		peers   [2]string
		want    []string
	}{
		{"the carrier", web.TrustProxies(1)(service.ClientInfoMiddleware()(limited)), [2]string{"10.0.0.1", "10.0.0.1"},
			[]string{"public:198.51.100.1", "public:198.51.100.2"}},
		{"no carrier, TrustProxies resolved", web.TrustProxies(1)(limited), [2]string{"10.0.0.1", "10.0.0.1"},
			[]string{"public:198.51.100.1", "public:198.51.100.2"}},
		{"no carrier, no TrustProxies", limited, [2]string{"10.0.0.1", "10.0.0.2"},
			[]string{"public:10.0.0.1", "public:10.0.0.2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, peer := range tc.peers {
				rec := httptest.NewRecorder()
				tc.handler.ServeHTTP(rec, forwardedRequest(peer, []string{"198.51.100.1", "198.51.100.2"}[i]))
				if rec.Code != http.StatusNoContent {
					t.Fatalf("status = %d, want 204", rec.Code)
				}
			}
			if got := limiter.take(); !slices.Equal(got, tc.want) {
				t.Fatalf("keys = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("the OAuth2 per-IP budget without a carrier", func(t *testing.T) {
		h := &handlers{oauth2: &OAuth2Config{Limiter: limiter}}
		var admitted bool
		web.TrustProxies(1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			admitted = h.oauth2Limit(w, r, "token", "", 10)
		})).ServeHTTP(httptest.NewRecorder(), forwardedRequest("10.0.0.1", "198.51.100.1"))
		digest := sha256.Sum256([]byte("198.51.100.1"))
		if got, want := limiter.take(), []string{"oauth2:token:ip:" + hex.EncodeToString(digest[:])}; !admitted || !slices.Equal(got, want) {
			t.Fatalf("admitted=%v keys = %q, want %q", admitted, got, want)
		}
	})
}

package cacher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func pageResponse(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPagesPartitions(t *testing.T) {
	calls := 0
	scope := func(r *http.Request) string { return r.Header.Get("X-Test-Tenant") }
	h := Pages(NewMemory(), PageConfig{Scope: scope})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "render %d", calls)
	}))
	for _, tc := range []struct{ url, tenant string }{
		{"http://one.test/page?q=1", "a"}, {"http://two.test/page?q=1", "a"},
		{"https://one.test/page?q=1", "a"}, {"http://one.test/page?q=2", "a"},
		{"http://one.test/page?q=1", "b"},
	} {
		for i := 0; i < 2; i++ {
			r := httptest.NewRequest("GET", tc.url, nil)
			r.Header.Set("X-Test-Tenant", tc.tenant)
			w := pageResponse(h, r)
			want := []string{"MISS", "HIT"}[i]
			if w.Header().Get("X-Cache") != want {
				t.Fatalf("%+v: %v", tc, w.Header())
			}
		}
	}
	if calls != 5 {
		t.Fatalf("renders = %d", calls)
	}
}

func TestPagesBypassesRequestPoliciesEvenOnHit(t *testing.T) {
	cases := map[string]func(*http.Request){
		"head": func(r *http.Request) { r.Method = "HEAD" },
		"post": func(r *http.Request) { r.Method = "POST" },
		"principal": func(r *http.Request) {
			*r = *r.WithContext(sdk.WithPrincipal(r.Context(), sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "u1"}))
		},
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "Cache-Control", "Pragma"} {
		cases[name] = func(r *http.Request) { r.Header.Set(name, "present") }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			calls := 0
			h := Pages(NewMemory(), PageConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprintf(w, "%d", calls)
			}))
			pageResponse(h, httptest.NewRequest("GET", "/", nil))
			r := httptest.NewRequest("GET", "/", nil)
			mutate(r)
			w := pageResponse(h, r)
			if calls != 2 || w.Header().Get("X-Cache") != "" {
				t.Fatalf("calls=%d headers=%v", calls, w.Header())
			}
		})
	}
}

func TestPagesResponseBypasses(t *testing.T) {
	cases := []struct{ name, value string }{
		{"Cache-Control", "private"}, {"Cache-Control", "no-store"}, {"Cache-Control", "no-cache"},
		{"Cache-Control", "max-age=0"}, {"Cache-Control", "max-age=bad"}, {"Cache-Control", "stale-while-revalidate=30"},
		{"Cache-Control", "public, max-age=10, max-age=20"}, {"Vary", "Accept-Language"},
		{"Set-Cookie", "session=secret"}, {"Content-Encoding", "gzip"}, {"Content-Length", "4"},
		{"Content-Type", "application/json"}, {"Content-Type", "text/html; broken"},
		{"Content-Security-Policy", "script-src 'nonce-unique'"}, {"Content-Security-Policy-Report-Only", "script-src 'nonce-unique'"},
		{"Expires", time.Now().Add(time.Hour).Format(http.TimeFormat)}, {"Trailer", "Digest"},
		{"X-Handler-Metadata", "must-not-disappear"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.value, func(t *testing.T) {
			calls := 0
			h := Pages(NewMemory(), PageConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/html")
				w.Header().Set(tc.name, tc.value)
				io.WriteString(w, "page")
			}))
			for i := 0; i < 2; i++ {
				pageResponse(h, httptest.NewRequest("GET", "/", nil))
			}
			if calls != 2 {
				t.Fatalf("ineligible response cached: calls=%d", calls)
			}
		})
	}
}

func TestPagesNoStoreBothOrders(t *testing.T) {
	for _, outer := range []bool{false, true} {
		calls := 0
		store := NewMemory()
		render := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "page")
		})
		cache := Pages(store, PageConfig{})
		var h http.Handler = cache(web.NoStore()(render))
		if outer {
			// Warm an existing entry, then enforce the host's new outer no-store policy.
			pageResponse(cache(render), httptest.NewRequest("GET", "/", nil))
			calls = 0
			h = web.NoStore()(cache(render))
		}
		for i := 0; i < 2; i++ {
			w := pageResponse(h, httptest.NewRequest("GET", "/", nil))
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Header())
			}
		}
		if calls != 2 {
			t.Fatalf("outer=%v calls=%d", outer, calls)
		}
	}
}

func TestPagesConflictingOuterMetadata(t *testing.T) {
	for _, tc := range []struct{ name, outer, inner string }{
		{"Cache-Control", "public, max-age=60", "public, max-age=5"},
		{"Etag", "outer", "inner"}, {"Etag", "outer", ""},
		{"Content-Language", "en", "fr"}, {"Content-Length", "5", "4"},
	} {
		t.Run(tc.name+tc.inner, func(t *testing.T) {
			calls := 0
			cache := Pages(NewMemory(), PageConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/html")
				if tc.inner == "" {
					w.Header().Del(tc.name)
				} else {
					w.Header().Set(tc.name, tc.inner)
				}
				io.WriteString(w, "page")
			}))
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set(tc.name, tc.outer); cache.ServeHTTP(w, r) })
			for i := 0; i < 2; i++ {
				w := pageResponse(h, httptest.NewRequest("GET", "/", nil))
				if w.Result().Header.Get(tc.name) != tc.inner {
					t.Fatal(w.Result().Header)
				}
			}
			if calls != 2 {
				t.Fatalf("conflicting metadata replayed; calls=%d", calls)
			}
		})
	}
}

func TestPagesWireMetadataAndFreshRequestID(t *testing.T) {
	calls := 0
	cache := Pages(NewMemory(), PageConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Header().Add("Content-Security-Policy", "default-src 'self'")
		w.Header().Add("Content-Security-Policy", "object-src 'none'")
		w.Header().Set("Content-Language", "fr")
		w.WriteHeader(200)
		// A post-commit mutation did not go to the client and must not enter a record.
		w.Header().Set("Content-Language", "en")
		io.WriteString(w, "<p>caf\xe9</p>")
	}))
	server := httptest.NewServer(web.RequestID()(cache))
	defer server.Close()
	previousID := ""
	for i := 0; i < 2; i++ {
		res, err := server.Client().Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "<p>caf\xe9</p>" || res.Header.Get("Content-Language") != "fr" || res.Header.Get("Content-Type") != "text/html; charset=iso-8859-1" || len(res.Header.Values("Content-Security-Policy")) != 2 {
			t.Fatalf("headers=%v body=%q", res.Header, body)
		}
		if res.Header.Get("Cache-Control") != "public, max-age=30" || res.Header.Get("X-Cache") != []string{"MISS", "HIT"}[i] {
			t.Fatal(res.Header)
		}
		id := res.Header.Get("X-Request-ID")
		if id == "" || id == previousID {
			t.Fatalf("request ID reused: %q", id)
		}
		previousID = id
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestPagesBoundedCaptureAndDisabled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cfg       PageConfig
		store     Storer
		wantCalls int
	}{
		{"at limit", PageConfig{MaxBodyBytes: 4}, NewMemory(), 1},
		{"over limit", PageConfig{MaxBodyBytes: 3}, NewMemory(), 2},
		{"disabled", PageConfig{TTL: -1}, NewMemory(), 2},
		{"nil", PageConfig{}, nil, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := Pages(tc.store, tc.cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/html")
				io.WriteString(w, "pa")
				io.WriteString(w, "ge")
			}))
			for i := 0; i < 2; i++ {
				if w := pageResponse(h, httptest.NewRequest("GET", "/", nil)); w.Body.String() != "page" {
					t.Fatal(w.Body.String())
				}
			}
			if calls != tc.wantCalls {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
	capture := &captureWriter{StatusRecorder: web.NewStatusRecorder(httptest.NewRecorder()), ttl: time.Minute, limit: 2}
	capture.Header().Set("Content-Type", "text/html")
	capture.Write([]byte("pa"))
	capture.Write([]byte("ge"))
	if capture.cacheable || capture.buf.Cap() != 0 {
		t.Fatal("oversized buffer retained")
	}
}

type pageErrorStore struct {
	Storer
	readError bool
}

func (s pageErrorStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	data, found, err := s.Storer.Get(ctx, key)
	if s.readError {
		return data, found, errors.New("cache down")
	}
	return data, found, err
}
func (s pageErrorStore) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("cache down")
}

func TestPagesInvalidRecordsAndOutages(t *testing.T) {
	for _, kind := range []string{"corrupt", "expired", "future", "version", "body limit", "current ttl", "read error", "write error"} {
		t.Run(kind, func(t *testing.T) {
			raw := NewMemory()
			now := time.Now()
			entry := pageRecord{Version: pageRecordVersion, Created: now.Add(-time.Second), Expires: now.Add(time.Minute), Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte("cached")}
			cfg := PageConfig{}
			switch kind {
			case "expired":
				entry.Expires = now.Add(-time.Second)
			case "future":
				entry.Created = now.Add(time.Minute)
			case "version":
				entry.Version++
			case "body limit":
				cfg.MaxBodyBytes = 4
			case "current ttl":
				cfg.TTL = time.Nanosecond
			}
			data, _ := json.Marshal(entry)
			if kind == "corrupt" {
				data = []byte("broken")
			}
			req := httptest.NewRequest("GET", "/", nil)
			if kind != "write error" {
				if err := raw.Set(req.Context(), pageKey(req, nil), data, 0); err != nil {
					t.Fatal(err)
				}
			}
			var store Storer = raw
			if strings.HasSuffix(kind, "error") {
				store = pageErrorStore{Storer: raw, readError: kind == "read error"}
			}
			h := Pages(store, cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				io.WriteString(w, "fresh")
			}))
			w := pageResponse(h, req)
			if w.Body.String() != "fresh" || w.Code != 200 {
				t.Fatalf("%d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestPageTTLBounds(t *testing.T) {
	for _, tc := range []struct {
		policy    string
		ttl, want time.Duration
	}{
		{"public, max-age=30, s-maxage=5", time.Minute, 5 * time.Second},
		{"public, max-age=999999999999999", time.Minute, time.Minute},
		{"max-age=1", time.Nanosecond, time.Nanosecond},
		{`max-age="5"`, time.Minute, 5 * time.Second},
	} {
		got, ok := pageTTL(http.Header{"Cache-Control": {tc.policy}}, tc.ttl)
		if !ok || got != tc.want {
			t.Fatalf("%s: %s, %v", tc.policy, got, ok)
		}
	}
}

func TestPageKeyPreservesRawBytes(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.URL.RawQuery = "x=\xff"
	a := pageKey(r, nil)
	r.URL.RawQuery = "x=\xfe"
	if a == pageKey(r, nil) {
		t.Fatal("raw query collision")
	}
	if pageKey(r, func(*http.Request) string { return "\xff" }) == pageKey(r, func(*http.Request) string { return "\xfe" }) {
		t.Fatal("scope collision")
	}
}

type pageRecordedFailure struct {
	failedWriter
	err error
}

func (w *pageRecordedFailure) RecordError(err error) { w.err = err }

func TestPagesHitRecordsShortWrite(t *testing.T) {
	h := Pages(NewMemory(), PageConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "page")
	}))
	pageResponse(h, httptest.NewRequest("GET", "/", nil))
	w := &pageRecordedFailure{failedWriter: failedWriter{ResponseRecorder: httptest.NewRecorder(), short: true}}
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Header().Get("X-Cache") != "HIT" || !errors.Is(w.err, io.ErrShortWrite) {
		t.Fatalf("headers=%v error=%v", w.Header(), w.err)
	}
}

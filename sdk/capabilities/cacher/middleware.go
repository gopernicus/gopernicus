package cacher

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	DefaultPageTTL          = time.Minute
	DefaultPageMaxBodyBytes = 1 << 20
	pageRecordVersion       = 1
)

// PageConfig configures public HTML caching. Zero values select a one-minute TTL
// and a 1 MiB body limit. Negative TTL disables caching. These TTL defaults are
// specific to Pages; Storer.Set's zero TTL still means no expiration.
type PageConfig struct {
	TTL          time.Duration
	MaxBodyBytes int
	// Scope adds a host-owned partition, such as a trusted tenant ID, to the
	// scheme/authority/URI key. It must be safe for concurrent calls. Pages never
	// infers tenant identity or proxy trust from request headers.
	Scope func(*http.Request) string
}

type pageRecord struct {
	Version int
	Created time.Time
	Expires time.Time
	Header  http.Header
	Body    []byte
}

// Pages caches eligible public GET HTML responses. Mount it only on public,
// cacheable routes. Credentials, cookies, a context principal, conditional/range
// requests and request cache directives bypass it. Responses with Vary, cookies,
// encoding, nonce policies or unsupported metadata also bypass caching.
//
// Hits restore supported response metadata while retaining fresh outer headers.
// Put compression and per-request headers outside Pages; nonce-dependent content
// must not use Pages. Errors and oversized bodies are streamed without caching.
// Cache outages become misses. A nil store or negative TTL disables the middleware.
// Entries use versioned page: keys; prefix invalidation is best effort and cannot
// prevent an in-flight render from repopulating old data.
func Pages(store Storer, cfg PageConfig) web.Middleware {
	if cfg.TTL == 0 {
		cfg.TTL = DefaultPageTTL
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = DefaultPageMaxBodyBytes
	}
	return func(next http.Handler) http.Handler {
		if store == nil || cfg.TTL < 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ttl, eligible := pageTTL(w.Header(), cfg.TTL)
			if !eligible || !pageRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			key := pageKey(r, cfg.Scope)
			if data, found, err := store.Get(r.Context(), key); err == nil && found {
				var entry pageRecord
				if json.Unmarshal(data, &entry) == nil && validPage(entry, time.Now(), ttl, cfg.MaxBodyBytes) && compatiblePageMetadata(w.Header(), entry.Header) {
					for name, values := range entry.Header {
						if _, present := w.Header()[name]; !present {
							w.Header()[name] = append([]string(nil), values...)
						}
					}
					w.Header().Set("Age", strconv.FormatInt(int64(time.Since(entry.Created)/time.Second), 10))
					w.Header().Set("X-Cache", "HIT")
					w.WriteHeader(http.StatusOK)
					n, err := w.Write(entry.Body)
					if n < len(entry.Body) && err == nil {
						err = io.ErrShortWrite
					}
					web.RecordError(w, err)
					return
				}
			}

			outer := w.Header().Clone()
			w.Header().Set("X-Cache", "MISS")
			capture := &captureWriter{StatusRecorder: web.NewStatusRecorder(w), outer: outer, ttl: ttl, limit: cfg.MaxBodyBytes}
			next.ServeHTTP(capture, r)
			if !capture.cacheable || capture.Err() != nil || r.Context().Err() != nil {
				return
			}
			remaining := time.Until(capture.entry.Expires)
			if remaining <= 0 {
				return
			}
			capture.entry.Body = capture.buf.Bytes()
			data, err := json.Marshal(capture.entry)
			if err == nil {
				_ = store.Set(r.Context(), key, data, remaining)
			}
		})
	}
}

func pageRequest(r *http.Request) bool {
	if r.Method != http.MethodGet || r.Context().Err() != nil {
		return false
	}
	if _, present := sdk.PrincipalFromContext(r.Context()); present {
		return false
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "Cache-Control", "Pragma"} {
		if len(r.Header.Values(name)) != 0 {
			return false
		}
	}
	return true
}

func pageKey(r *http.Request, scope func(*http.Request) string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	partition := ""
	if scope != nil {
		partition = scope(r)
	}
	// Length framing preserves arbitrary bytes in raw queries and host-owned
	// scopes. Hashing keeps those values out of visible storage keys.
	var data strings.Builder
	for _, part := range []string{scheme, strings.ToLower(r.Host), r.URL.RequestURI(), partition} {
		data.WriteString(strconv.Itoa(len(part)))
		data.WriteByte(':')
		data.WriteString(part)
	}
	sum := sha256.Sum256([]byte(data.String()))
	return "page:v2:" + hex.EncodeToString(sum[:])
}

func validPage(entry pageRecord, now time.Time, ttl time.Duration, limit int) bool {
	if entry.Version != pageRecordVersion || entry.Created.IsZero() || entry.Created.After(now) || !entry.Expires.After(now) || len(entry.Body) > limit {
		return false
	}
	effective, ok := pageTTL(entry.Header, ttl)
	if !ok || now.Sub(entry.Created) >= effective || !htmlContentType(entry.Header.Get("Content-Type")) {
		return false
	}
	for name := range entry.Header {
		if !pageHeaderAllowed(name) {
			return false
		}
	}
	return true
}

func compatiblePageMetadata(outer, stored http.Header) bool {
	for name, values := range stored {
		if fresh, present := outer[name]; present && !slices.Equal(fresh, values) {
			return false
		}
	}
	for name, values := range outer {
		if pageHeaderAllowed(name) && !slices.Equal(values, stored[name]) {
			return false
		}
	}
	return true
}

// captureWriter preserves streaming while keeping only an eligible bounded body.
// Metadata is captured at commitment, when net/http fixes its wire headers.
type captureWriter struct {
	*web.StatusRecorder
	outer     http.Header
	ttl       time.Duration
	limit     int
	committed bool
	cacheable bool
	entry     pageRecord
	buf       bytes.Buffer
}

func (c *captureWriter) commit(status int) {
	if c.committed {
		return
	}
	c.committed = true
	if status != http.StatusOK {
		return
	}
	ttl, ok := pageTTL(c.Header(), c.ttl)
	if !ok || !htmlContentType(c.Header().Get("Content-Type")) {
		return
	}
	header, ok := pageMetadata(c.Header(), c.outer)
	if !ok {
		return
	}
	now := time.Now()
	c.entry = pageRecord{Version: pageRecordVersion, Created: now, Expires: now.Add(ttl), Header: header}
	c.cacheable = true
}

func (c *captureWriter) WriteHeader(status int) {
	if status >= 200 || status == http.StatusSwitchingProtocols {
		c.commit(status)
	}
	c.StatusRecorder.WriteHeader(status)
}

func (c *captureWriter) Write(body []byte) (int, error) {
	c.commit(http.StatusOK)
	n, err := c.StatusRecorder.Write(body)
	if c.cacheable {
		if err != nil || n > c.limit-c.buf.Len() {
			c.discard()
		} else {
			c.buf.Write(body[:n])
		}
	}
	return n, err
}

func (c *captureWriter) FlushError() error {
	c.commit(http.StatusOK)
	err := c.StatusRecorder.FlushError()
	if err != nil {
		c.discard()
	}
	return err
}

func (c *captureWriter) Flush() { _ = c.FlushError() }

func (c *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c.committed = true
	c.discard()
	return c.StatusRecorder.Hijack()
}

func (c *captureWriter) discard() {
	c.cacheable = false
	c.buf = bytes.Buffer{}
}

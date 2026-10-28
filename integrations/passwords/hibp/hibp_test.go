package hibp_test

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/passwords/hibp"
	"github.com/gopernicus/gopernicus/sdk"
)

const candidate = "correct horse battery staple"

var hexRun = regexp.MustCompile(`[0-9A-Fa-f]{5}`)

// compromisedPasswordChecker mirrors the CompromisedPasswordChecker port in
// pockets/authentication/logic/authentication; the port lives with its
// consumer, so this module cannot import it.
type compromisedPasswordChecker interface {
	IsCompromised(ctx context.Context, password string) (bool, error)
}

var _ compromisedPasswordChecker = (*hibp.Checker)(nil)

func digest(password string) (prefix, suffix string) {
	sum := sha1.Sum([]byte(password))
	d := strings.ToUpper(hex.EncodeToString(sum[:]))
	return d[:5], d[5:]
}

func paddingRows(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%035X:0\r\n", i+1)
	}
	return b.String()
}

func newChecker(t *testing.T, url string, opts ...hibp.Option) *hibp.Checker {
	t.Helper()
	c, err := hibp.New(append([]hibp.Option{hibp.WithBaseURL(url)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// assertNoLeak fails when an error string carries anything the k-anonymity
// exchange must keep in the request: a five-hex-character run (the prefix or
// any longer hash fragment), the request path, or the server address.
func assertNoLeak(t *testing.T, err error, serverURL string) {
	t.Helper()
	msg := err.Error()
	if hexRun.MatchString(msg) {
		t.Errorf("error %q carries a hex run", msg)
	}
	if strings.Contains(msg, "/range") || strings.Contains(msg, strings.TrimPrefix(serverURL, "http://")) {
		t.Errorf("error %q carries the request URL", msg)
	}
	if strings.Contains(msg, candidate) {
		t.Errorf("error %q carries the password", msg)
	}
}

func TestHitSendsOnlyPrefixWithHeaders(t *testing.T) {
	prefix, suffix := digest(candidate)
	var gotPath, gotQuery, gotUA, gotPadding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotUA, gotPadding = r.Header.Get("User-Agent"), r.Header.Get("Add-Padding")
		fmt.Fprint(w, paddingRows(3)+suffix+":42\r\n"+paddingRows(2))
	}))
	defer srv.Close()

	ok, err := newChecker(t, srv.URL).IsCompromised(t.Context(), candidate)
	if err != nil || !ok {
		t.Fatalf("IsCompromised = %v, %v; want true, nil", ok, err)
	}
	if gotPath != "/range/"+prefix || gotQuery != "" {
		t.Errorf("request path = %q query %q; want /range/%s and no query", gotPath, gotQuery, prefix)
	}
	if gotUA != hibp.DefaultUserAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, hibp.DefaultUserAgent)
	}
	if gotPadding != "true" {
		t.Errorf("Add-Padding = %q, want true", gotPadding)
	}
}

func TestCustomUserAgentSent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, paddingRows(1))
	}))
	defer srv.Close()
	if _, err := newChecker(t, srv.URL, hibp.WithUserAgent("example-host/1.0")).IsCompromised(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if gotUA != "example-host/1.0" {
		t.Fatalf("User-Agent = %q", gotUA)
	}
}

func TestLowercaseSuffixMatches(t *testing.T) {
	_, suffix := digest(candidate)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.ToLower(suffix)+":2\n")
	}))
	defer srv.Close()
	ok, err := newChecker(t, srv.URL).IsCompromised(t.Context(), candidate)
	if err != nil || !ok {
		t.Fatalf("IsCompromised = %v, %v; want true, nil", ok, err)
	}
}

func TestMiss(t *testing.T) {
	_, suffix := digest("some other password")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, suffix+":9\r\n"+paddingRows(4))
	}))
	defer srv.Close()
	ok, err := newChecker(t, srv.URL).IsCompromised(t.Context(), candidate)
	if err != nil || ok {
		t.Fatalf("IsCompromised = %v, %v; want false, nil", ok, err)
	}
}

func TestPaddingOnlyAndZeroCountMatch(t *testing.T) {
	_, suffix := digest(candidate)
	for name, body := range map[string]string{
		"padding only":     paddingRows(5),
		"zero-count match": paddingRows(2) + suffix + ":0\r\n",
		"empty body":       "",
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, body)
			}))
			defer srv.Close()
			ok, err := newChecker(t, srv.URL).IsCompromised(t.Context(), candidate)
			if err != nil || ok {
				t.Fatalf("IsCompromised = %v, %v; want false, nil", ok, err)
			}
		})
	}
}

func TestMinimumCount(t *testing.T) {
	_, suffix := digest(candidate)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, suffix+":3\r\n")
	}))
	defer srv.Close()
	if ok, err := newChecker(t, srv.URL, hibp.WithMinimumCount(4)).IsCompromised(t.Context(), candidate); err != nil || ok {
		t.Fatalf("minimum 4: IsCompromised = %v, %v; want false, nil", ok, err)
	}
	if ok, err := newChecker(t, srv.URL, hibp.WithMinimumCount(3)).IsCompromised(t.Context(), candidate); err != nil || !ok {
		t.Fatalf("minimum 3: IsCompromised = %v, %v; want true, nil", ok, err)
	}
}

func TestFailuresAreUnavailableAndLeakNothing(t *testing.T) {
	_, suffix := digest(candidate)
	var redirectTargetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectTargetHits.Add(1)
		fmt.Fprint(w, suffix+":1\r\n")
	}))
	defer target.Close()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		class   string
	}{
		{"429", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }, "upstream status 429"},
		{"500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, "upstream status 500"},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
		}, "upstream status 302"},
		{"malformed line", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, paddingRows(1)+"not-a-range-line\r\n")
		}, "malformed response"},
		{"short suffix", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, suffix[:30]+":1\r\n")
		}, "malformed response"},
		{"bad count", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, suffix+":many\r\n")
		}, "malformed response"},
		{"negative count", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, suffix+":-1\r\n")
		}, "malformed response"},
		{"oversized body", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, paddingRows((1<<20)/38+10))
		}, "response too large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			ok, err := newChecker(t, srv.URL).IsCompromised(t.Context(), candidate)
			if err == nil || ok {
				t.Fatalf("IsCompromised = %v, %v; want false and an error", ok, err)
			}
			if !errors.Is(err, sdk.ErrUnavailable) {
				t.Errorf("error %v is not sdk.ErrUnavailable", err)
			}
			if !strings.Contains(err.Error(), "hibp: "+tc.class) {
				t.Errorf("error %q lacks class %q", err, tc.class)
			}
			assertNoLeak(t, err, srv.URL)
		})
	}
	if n := redirectTargetHits.Load(); n != 0 {
		t.Fatalf("redirect target hit %d times; redirects must not be followed", n)
	}
}

func TestTransportFailureLeaksNothing(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	ok, err := newChecker(t, url).IsCompromised(t.Context(), candidate)
	if err == nil || ok {
		t.Fatalf("IsCompromised = %v, %v; want false and an error", ok, err)
	}
	if !errors.Is(err, sdk.ErrUnavailable) || !strings.Contains(err.Error(), "hibp: transport failure") {
		t.Errorf("error = %q, want sdk.ErrUnavailable transport failure", err)
	}
	assertNoLeak(t, err, url)
}

func TestTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	start := time.Now()
	ok, err := newChecker(t, srv.URL, hibp.WithTimeout(50*time.Millisecond)).IsCompromised(t.Context(), candidate)
	if err == nil || ok {
		t.Fatalf("IsCompromised = %v, %v; want false and an error", ok, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout took %v", elapsed)
	}
	if !errors.Is(err, sdk.ErrUnavailable) || !strings.Contains(err.Error(), "hibp: timeout") {
		t.Errorf("error = %q, want sdk.ErrUnavailable timeout", err)
	}
	assertNoLeak(t, err, srv.URL)
}

func TestCallerCancellationIsPreserved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := newChecker(t, srv.URL).IsCompromised(ctx, candidate)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("error = %v, want context.Canceled and sdk.ErrUnavailable", err)
	}
	assertNoLeak(t, err, srv.URL)
}

func TestHostClientCopiedAndRedirectsRefused(t *testing.T) {
	_, suffix := digest(candidate)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, suffix+":1\r\n")
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	host := &http.Client{}
	if _, err := newChecker(t, srv.URL, hibp.WithHTTPClient(host)).IsCompromised(t.Context(), candidate); err == nil {
		t.Fatal("a redirect must not be followed through a host client")
	}
	if host.CheckRedirect != nil {
		t.Fatal("the host client must not be mutated")
	}
}

func TestNewValidates(t *testing.T) {
	cases := map[string][]hibp.Option{
		"nil option":         {nil},
		"empty user agent":   {hibp.WithUserAgent("  ")},
		"zero timeout":       {hibp.WithTimeout(0)},
		"negative timeout":   {hibp.WithTimeout(-time.Second)},
		"zero minimum":       {hibp.WithMinimumCount(0)},
		"relative base":      {hibp.WithBaseURL("/range")},
		"ftp base":           {hibp.WithBaseURL("ftp://example.test")},
		"base with query":    {hibp.WithBaseURL("https://example.test?x=1")},
		"base with fragment": {hibp.WithBaseURL("https://example.test#x")},
		"base with userinfo": {hibp.WithBaseURL("https://user:pass@example.test")},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := hibp.New(opts...)
			if c != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("New = %v, %v; want nil and sdk.ErrInvalidInput", c, err)
			}
			if strings.Contains(err.Error(), "pass") {
				t.Fatalf("error %q echoes the base URL", err)
			}
		})
	}
	if _, err := hibp.New(); err != nil {
		t.Fatalf("New() with defaults: %v", err)
	}
}

func TestTrailingSlashBaseURL(t *testing.T) {
	prefix, _ := digest(candidate)
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer srv.Close()
	if _, err := newChecker(t, srv.URL+"/").IsCompromised(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/range/"+prefix {
		t.Fatalf("path = %q", gotPath)
	}
}

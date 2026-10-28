// Package hibp checks candidate passwords against the Have I Been Pwned
// Pwned Passwords range API using k-anonymity: only the first five hex
// characters of the password's SHA-1 leave the process, and the suffix is
// matched locally against every returned line.
//
// Its Checker structurally satisfies the CompromisedPasswordChecker port in
// pockets/authentication/logic/authentication without importing that pocket.
// The dependency is the vendor's API contract; the module uses only the standard
// library and the SDK.
//
// Every failure (transport, timeout, unexpected status, oversized or malformed
// body) is a fresh error classified as sdk.ErrUnavailable. Transport errors are
// never wrapped: *url.Error prints the request URL, and the URL carries the hash
// prefix. The consumer's fail-closed or fail-open policy decides the outcome.
package hibp

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// DefaultBaseURL is the Pwned Passwords API origin.
	DefaultBaseURL = "https://api.pwnedpasswords.com"
	// DefaultUserAgent is sent when WithUserAgent is not given; the API refuses
	// requests without a User-Agent.
	DefaultUserAgent = "gopernicus-hibp"
	// DefaultTimeout bounds one range request, body included.
	DefaultTimeout = 3 * time.Second
	// DefaultMinimumCount is the breach count at or above which a password is
	// reported compromised.
	DefaultMinimumCount = 1

	prefixLength    = 5
	suffixLength    = 35
	maxResponseBody = 1 << 20
)

// Checker reports whether a password appears in the Pwned Passwords corpus. It
// is safe for concurrent use.
type Checker struct {
	client       *http.Client
	baseURL      string
	userAgent    string
	timeout      time.Duration
	minimumCount int
}

// Option configures construction of a Checker. Options apply in order.
type Option func(*config)

type config struct {
	client       *http.Client
	baseURL      string
	userAgent    string
	timeout      time.Duration
	minimumCount int
}

// WithHTTPClient sets the HTTP client. The client is copied and its redirect
// policy replaced so a redirect is never followed; a nil client keeps the
// default.
func WithHTTPClient(client *http.Client) Option {
	return func(cfg *config) { cfg.client = client }
}

// WithBaseURL overrides the API origin (tests, or a host-run mirror of the
// range API). It must be an absolute http or https URL without query or
// fragment.
func WithBaseURL(baseURL string) Option {
	return func(cfg *config) { cfg.baseURL = baseURL }
}

// WithUserAgent sets the User-Agent header. It must not be empty.
func WithUserAgent(userAgent string) Option {
	return func(cfg *config) { cfg.userAgent = userAgent }
}

// WithTimeout bounds one range request, body included. It must be positive.
func WithTimeout(timeout time.Duration) Option {
	return func(cfg *config) { cfg.timeout = timeout }
}

// WithMinimumCount sets the breach count at or above which a password is
// reported compromised. It must be at least 1.
func WithMinimumCount(count int) Option {
	return func(cfg *config) { cfg.minimumCount = count }
}

// New builds a Checker. It performs no network I/O. Invalid options return an
// error classified as sdk.ErrInvalidInput.
func New(opts ...Option) (*Checker, error) {
	cfg := config{
		baseURL:      DefaultBaseURL,
		userAgent:    DefaultUserAgent,
		timeout:      DefaultTimeout,
		minimumCount: DefaultMinimumCount,
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("%w: hibp: nil Option", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	baseURL, err := validBaseURL(cfg.baseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.userAgent) == "" {
		return nil, fmt.Errorf("%w: hibp: user agent is required", sdk.ErrInvalidInput)
	}
	if cfg.timeout <= 0 {
		return nil, fmt.Errorf("%w: hibp: timeout must be positive", sdk.ErrInvalidInput)
	}
	if cfg.minimumCount < 1 {
		return nil, fmt.Errorf("%w: hibp: minimum count must be at least 1", sdk.ErrInvalidInput)
	}
	client := http.Client{}
	if cfg.client != nil {
		client = *cfg.client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Checker{
		client:       &client,
		baseURL:      baseURL,
		userAgent:    cfg.userAgent,
		timeout:      cfg.timeout,
		minimumCount: cfg.minimumCount,
	}, nil
}

// IsCompromised reports whether password appears in the corpus at least the
// configured minimum count. A non-nil error means the check could not complete;
// it never carries the password, its hash, the prefix or the request URL.
func (c *Checker) IsCompromised(ctx context.Context, password string) (bool, error) {
	// The Pwned Passwords range API requires SHA-1 for its k-anonymity prefix
	// lookup. The digest lives only in memory: just its 5-character prefix is
	// sent and the rest is compared locally against the response. It is never
	// stored, logged or sent whole; password storage uses bcrypt.
	sum := sha1.Sum([]byte(password))
	digest := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := digest[:prefixLength], digest[prefixLength:]

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.baseURL+"/range/"+prefix, nil)
	if err != nil {
		return false, unavailable("request construction failure")
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Add-Padding", "true")

	resp, err := c.client.Do(req)
	if err != nil {
		return false, transportError(ctx, reqCtx)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, unavailable("upstream status " + strconv.Itoa(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return false, transportError(ctx, reqCtx)
	}
	if len(body) > maxResponseBody {
		return false, unavailable("response too large")
	}
	count, ok := matchCount(body, suffix)
	if !ok {
		return false, unavailable("malformed response")
	}
	return count >= c.minimumCount, nil
}

// matchCount parses SUFFIX:COUNT lines and returns the count recorded for
// suffix (0 when absent; padding rows carry 0). Every line is parsed and
// compared, matched or not.
func matchCount(body []byte, suffix string) (int, bool) {
	want := []byte(suffix)
	found := 0
	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		got, rawCount, ok := strings.Cut(line, ":")
		if !ok || len(got) != suffixLength || !isHex(got) {
			return 0, false
		}
		count, err := strconv.Atoi(rawCount)
		if err != nil || count < 0 {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(strings.ToUpper(got)), want) == 1 && count > found {
			found = count
		}
	}
	return found, true
}

func isHex(s string) bool {
	for i := range len(s) {
		b := s[i]
		if (b < '0' || b > '9') && (b < 'A' || b > 'F') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

func validBaseURL(raw string) (string, error) {
	invalid := fmt.Errorf("%w: hibp: base URL must be an absolute http or https URL without query or fragment", sdk.ErrInvalidInput)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil {
		return "", invalid
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// transportError classifies a failed exchange without the transport error,
// whose text includes the request URL. A caller cancellation keeps its
// context error for errors.Is; it carries no URL.
func transportError(ctx, reqCtx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: hibp: %w", sdk.ErrUnavailable, err)
	}
	if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
		return unavailable("timeout")
	}
	return unavailable("transport failure")
}

func unavailable(class string) error {
	return fmt.Errorf("%w: hibp: %s", sdk.ErrUnavailable, class)
}

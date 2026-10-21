package gooidc

import (
	"container/list"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"sync"
	"time"
)

const (
	// negativeKidEntries bounds the negative kid cache; the refetch floor is
	// the real defense, this only stops one repeated bogus kid from spending a
	// fetch every floor window.
	negativeKidEntries = 256
	negativeKidTTL     = 5 * time.Minute

	// unknownKidLogWindow aggregates unknown-kid rejections into one log line.
	unknownKidLogWindow = time.Minute

	minRSABits = 2048
)

// keySet is one issuer's JWKS: fetched lazily, refetched at most once per
// floor, single-flight, and never emptied by a failed fetch.
type keySet struct {
	issuer  string
	url     string
	client  *http.Client
	logger  *slog.Logger
	now     func() time.Time
	floor   time.Duration
	timeout time.Duration
	maxAge  time.Duration

	mu        sync.Mutex
	keys      map[string]publicKey
	fetchedAt time.Time // last successful fetch
	attempted time.Time // last attempt, successful or not
	lastErr   error     // outcome of the last attempt
	inflight  *fetchCall
	negative  *kidCache
	unknown   unknownKidLog
}

type publicKey struct {
	key crypto.PublicKey
	alg string // the JWK's alg, "" when the issuer did not pin one
}

type fetchCall struct {
	done chan struct{}
	err  error
}

type unknownKidLog struct {
	windowStart time.Time
	suppressed  int
}

func newKeySet(issuer, url string, client *http.Client, logger *slog.Logger) *keySet {
	return &keySet{
		issuer:   issuer,
		url:      url,
		client:   client,
		logger:   logger,
		now:      time.Now,
		floor:    RefetchFloor,
		timeout:  FetchTimeout,
		maxAge:   KeySetMaxAge,
		negative: newKidCache(negativeKidEntries),
	}
}

// key returns the public key for kid. A known kid never waits on the network;
// an unknown kid waits for at most one fetch, and only when the floor allows
// one. Unknown after a successful fetch is ErrUnknownKey; unknown while the
// issuer is unreachable is ErrKeysUnavailable.
func (s *keySet) key(ctx context.Context, kid, alg string) (crypto.PublicKey, error) {
	now := s.now()
	s.mu.Lock()
	if k, ok := s.lookupLocked(kid); ok {
		if now.Sub(s.fetchedAt) > s.maxAge && s.inflight == nil && s.canFetchLocked(now) {
			s.startFetchLocked(now)
		}
		s.mu.Unlock()
		return k.match(alg)
	}
	if s.negative.contains(kid, now) {
		return nil, s.rejectUnknownLocked(kid, now)
	}

	call := s.inflight
	started := false
	switch {
	case call != nil:
	case s.canFetchLocked(now):
		call = s.startFetchLocked(now)
		started = true
	case s.lastErr != nil:
		err := s.lastErr
		s.mu.Unlock()
		return nil, err
	default:
		return nil, s.rejectUnknownLocked(kid, now)
	}
	s.mu.Unlock()

	select {
	case <-call.done:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: waiting for keys: %w", ErrKeysUnavailable, ctx.Err())
	}

	s.mu.Lock()
	if k, ok := s.lookupLocked(kid); ok {
		s.mu.Unlock()
		return k.match(alg)
	}
	if call.err != nil {
		s.mu.Unlock()
		return nil, call.err
	}
	if started {
		s.negative.add(kid, s.now())
	}
	return nil, s.rejectUnknownLocked(kid, s.now())
}

// lookupLocked resolves kid; a token without a kid matches only a key set of
// exactly one key.
func (s *keySet) lookupLocked(kid string) (publicKey, bool) {
	if kid != "" {
		k, ok := s.keys[kid]
		return k, ok
	}
	if len(s.keys) == 1 {
		for _, k := range s.keys {
			return k, true
		}
	}
	return publicKey{}, false
}

func (s *keySet) canFetchLocked(now time.Time) bool {
	return s.attempted.IsZero() || now.Sub(s.attempted) >= s.floor
}

func (s *keySet) startFetchLocked(now time.Time) *fetchCall {
	call := &fetchCall{done: make(chan struct{})}
	s.inflight = call
	s.attempted = now
	go s.runFetch(call)
	return call
}

func (s *keySet) runFetch(call *fetchCall) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	keys, err := s.download(ctx)

	s.mu.Lock()
	retained := len(s.keys)
	if err == nil {
		s.keys = keys
		s.fetchedAt = s.now()
		s.lastErr = nil
	} else {
		s.lastErr = err
	}
	s.inflight = nil
	call.err = err
	s.mu.Unlock()
	close(call.done)

	if err != nil {
		s.logger.Warn("gooidc: JWKS fetch failed; keeping the last good key set",
			"issuer", s.issuer, "retained_keys", retained, "error", err)
	}
}

// rejectUnknownLocked records an unknown-kid rejection, unlocks, and logs at
// most once per window per issuer.
func (s *keySet) rejectUnknownLocked(kid string, now time.Time) error {
	logNow := false
	suppressed := 0
	if s.unknown.windowStart.IsZero() || now.Sub(s.unknown.windowStart) >= unknownKidLogWindow {
		logNow = true
		suppressed = s.unknown.suppressed
		s.unknown = unknownKidLog{windowStart: now}
	} else {
		s.unknown.suppressed++
	}
	s.mu.Unlock()

	if logNow {
		s.logger.Warn("gooidc: rejected tokens signed with an unknown key id",
			"issuer", s.issuer, "kid", truncate(kid),
			"suppressed_in_previous_window", suppressed, "window", unknownKidLogWindow)
	}
	return fmt.Errorf("%w: kid %q", ErrUnknownKey, truncate(kid))
}

func (k publicKey) match(alg string) (crypto.PublicKey, error) {
	if k.alg != "" && k.alg != alg {
		return nil, fmt.Errorf("%w: key is published for %s", ErrInvalidToken, k.alg)
	}
	return k.key, nil
}

func (s *keySet) download(ctx context.Context) (map[string]publicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeysUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeysUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxJWKSBytes))
		return nil, fmt.Errorf("%w: JWKS answered %s", ErrKeysUnavailable, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxJWKSBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading JWKS: %w", ErrKeysUnavailable, err)
	}
	if len(body) > MaxJWKSBytes {
		return nil, fmt.Errorf("%w: JWKS exceeds %d bytes", ErrKeysUnavailable, MaxJWKSBytes)
	}

	var doc struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: malformed JWKS", ErrKeysUnavailable)
	}
	keys := make(map[string]publicKey, len(doc.Keys))
	for _, raw := range doc.Keys {
		kid, key, ok := parseJWK(raw)
		if !ok {
			continue
		}
		if _, dup := keys[kid]; !dup {
			keys[kid] = key
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: JWKS has no usable signing keys", ErrKeysUnavailable)
	}
	return keys, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// parseJWK accepts public RSA (≥ 2048 bits), EC P-256/384/521 and Ed25519
// signing keys; anything else is skipped.
func parseJWK(raw json.RawMessage) (string, publicKey, bool) {
	var k jwk
	if err := json.Unmarshal(raw, &k); err != nil {
		return "", publicKey{}, false
	}
	if k.Use != "" && k.Use != "sig" {
		return "", publicKey{}, false
	}
	var key crypto.PublicKey
	var err error
	switch k.Kty {
	case "RSA":
		key, err = rsaKey(k)
	case "EC":
		key, err = ecKey(k)
	case "OKP":
		key, err = okpKey(k)
	default:
		err = errors.New("unsupported kty")
	}
	if err != nil {
		return "", publicKey{}, false
	}
	return k.Kid, publicKey{key: key, alg: k.Alg}, true
}

func rsaKey(k jwk) (*rsa.PublicKey, error) {
	n, err := b64(k.N)
	if err != nil {
		return nil, err
	}
	e, err := b64(k.E)
	if err != nil {
		return nil, err
	}
	if len(e) == 0 || len(e) > 4 {
		return nil, errors.New("bad exponent")
	}
	modulus := new(big.Int).SetBytes(n)
	exponent := int(new(big.Int).SetBytes(e).Int64())
	if modulus.BitLen() < minRSABits || exponent < 3 || exponent%2 == 0 {
		return nil, errors.New("weak RSA key")
	}
	return &rsa.PublicKey{N: modulus, E: exponent}, nil
}

func ecKey(k jwk) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch k.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, errors.New("unsupported curve")
	}
	size := (curve.Params().BitSize + 7) / 8
	x, err := b64(k.X)
	if err != nil {
		return nil, err
	}
	y, err := b64(k.Y)
	if err != nil {
		return nil, err
	}
	if len(x) != size || len(y) != size {
		return nil, errors.New("bad coordinate length")
	}
	point := make([]byte, 0, 1+2*size)
	point = append(point, 4)
	point = append(point, x...)
	point = append(point, y...)
	return ecdsa.ParseUncompressedPublicKey(curve, point)
}

func okpKey(k jwk) (ed25519.PublicKey, error) {
	if k.Crv != "Ed25519" {
		return nil, errors.New("unsupported curve")
	}
	x, err := b64(k.X)
	if err != nil {
		return nil, err
	}
	if len(x) != ed25519.PublicKeySize {
		return nil, errors.New("bad key length")
	}
	return ed25519.PublicKey(x), nil
}

func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// kidCache is a bounded LRU of kids confirmed absent from a fetch this
// process started after the kid was presented.
type kidCache struct {
	max     int
	order   *list.List
	entries map[string]*list.Element
}

type kidEntry struct {
	kid     string
	expires time.Time
}

func newKidCache(max int) *kidCache {
	return &kidCache{max: max, order: list.New(), entries: make(map[string]*list.Element)}
}

func (c *kidCache) contains(kid string, now time.Time) bool {
	el, ok := c.entries[kid]
	if !ok {
		return false
	}
	if now.After(el.Value.(*kidEntry).expires) {
		c.order.Remove(el)
		delete(c.entries, kid)
		return false
	}
	c.order.MoveToFront(el)
	return true
}

func (c *kidCache) add(kid string, now time.Time) {
	if el, ok := c.entries[kid]; ok {
		el.Value.(*kidEntry).expires = now.Add(negativeKidTTL)
		c.order.MoveToFront(el)
		return
	}
	c.entries[kid] = c.order.PushFront(&kidEntry{kid: kid, expires: now.Add(negativeKidTTL)})
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*kidEntry).kid)
	}
}

func (c *kidCache) len() int { return c.order.Len() }

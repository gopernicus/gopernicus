// Package goredis implements the authorization TupleCache backend using Redis.
package goredis

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/redis/go-redis/v9"
)

const (
	temporaryTTL            = 5 * time.Minute
	fieldChunk              = 256
	defaultMaxReadBytes     = 1 << 20
	defaultMaxMutationBytes = 4 << 20
	defaultExpiry           = 7 * 24 * time.Hour
)

var _ tuplecache.Backend = (*TupleCache)(nil)

// TupleCache stores raw forward and reverse canonical tuple sets in one Redis
// hash per binding. The client and its lifecycle belong to the host. Arbitrary
// writes to these hashes are unsupported; WithIntegrityKeys makes the adapter
// detect forged facts and headers instead of trusting Redis.
type TupleCache struct {
	client            *redis.Client
	namespace         string
	limits            Limits
	expiry            time.Duration
	ring              *keyRing
	integrityFailures atomic.Uint64
}

// Stats counts this backend's integrity failures: header MAC or fact tag
// verifications that failed, each answered by the durable fallback.
type Stats struct {
	IntegrityFailures uint64
}

// Limits bound encoded tuple work before Redis fetches or transforms raw sets.
// Zero values select defaults: 1 MiB per Read and 4 MiB per delta publication.
// MaxMutationBytes also bounds each full-rebuild field and upload chunk; it
// does not bound the total mirror size. Negative values are invalid.
type Limits struct {
	MaxReadBytes     int
	MaxMutationBytes int
}

type config struct {
	limits       Limits
	namespace    string
	namespaceSet bool
	expiry       time.Duration
	keys         [][]byte
	keysSet      bool
}

// Option configures a TupleCache at construction.
type Option func(*config)

// WithLimits replaces the complete limits record; zero fields select defaults.
func WithLimits(limits Limits) Option { return func(c *config) { c.limits = limits } }

// WithExpiry sets how long a mirror outlives its last publication. Every
// publication renews it, so only a mirror nothing publishes to (a superseded
// binding) expires. The default is 7 days; 0 never expires. Negative values and
// values below one millisecond are invalid.
func WithExpiry(expiry time.Duration) Option { return func(c *config) { c.expiry = expiry } }

// WithNamespace adds a readable prefix to every mirror key:
// tuplecache:v3:{<namespace>/<binding>}. It must be nonempty and contain only
// ASCII letters, digits, colons, periods, underscores, hyphens or slashes.
func WithNamespace(namespace string) Option {
	return func(c *config) { c.namespace, c.namespaceSet = namespace, true }
}

// WithIntegrityKeys signs the mirror: every fact carries an HMAC-SHA256 tag
// bound to the binding and the mirror's epoch, and every publication writes a
// MAC over the mirror key, binding, receipt, epoch, its time and the
// eligibility deadline.
// Read verifies both and treats a failure as an unavailable mirror, so a Redis
// writer without the key cannot add a fact or extend a mirror's eligibility.
// current signs new full publications; previous keys only verify, so rotate by
// prepending a new key and drop an old one once every mirror has been rebuilt
// under the new key. Each key must be at least MinIntegrityKeyBytes long.
// Keyed mirrors live under tuplecache:v3:ik:{...}, apart from unkeyed ones.
func WithIntegrityKeys(current []byte, previous ...[]byte) Option {
	keys := make([][]byte, 0, 1+len(previous))
	for _, key := range append([][]byte{current}, previous...) {
		keys = append(keys, append([]byte(nil), key...))
	}
	return func(c *config) { c.keys, c.keysSet = keys, true }
}

// NewTupleCache constructs a backend without I/O or background goroutines.
// Each mirror is stored under tuplecache:v3:{<binding>}, or
// tuplecache:v3:{<namespace>/<binding>} with WithNamespace, so processes share
// a mirror exactly when their runtime bindings match.
// The borrowed client must enable redis.Options.ContextTimeoutEnabled so the
// runtime's read deadline also bounds socket I/O. The client is never modified.
func NewTupleCache(client *redis.Client, opts ...Option) (*TupleCache, error) {
	if client == nil {
		return nil, fmt.Errorf("tuple cache requires a Redis client: %w", sdk.ErrInvalidInput)
	}
	if !client.Options().ContextTimeoutEnabled {
		return nil, fmt.Errorf("tuple cache Redis client requires ContextTimeoutEnabled: %w", sdk.ErrInvalidInput)
	}
	cfg := config{expiry: defaultExpiry}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("nil tuple cache option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if cfg.limits.MaxReadBytes < 0 || cfg.limits.MaxMutationBytes < 0 {
		return nil, fmt.Errorf("negative tuple cache limits: %w", sdk.ErrInvalidInput)
	}
	if cfg.expiry < 0 || (cfg.expiry > 0 && cfg.expiry < time.Millisecond) {
		return nil, fmt.Errorf("tuple cache expiry must be zero or at least one millisecond: %w", sdk.ErrInvalidInput)
	}
	if cfg.limits.MaxReadBytes == 0 {
		cfg.limits.MaxReadBytes = defaultMaxReadBytes
	}
	if cfg.limits.MaxMutationBytes == 0 {
		cfg.limits.MaxMutationBytes = defaultMaxMutationBytes
	}
	if cfg.namespaceSet {
		if err := validKeyPart("namespace", cfg.namespace); err != nil {
			return nil, err
		}
	}
	c := &TupleCache{client: client, namespace: cfg.namespace, limits: cfg.limits, expiry: cfg.expiry}
	if cfg.keysSet {
		for _, key := range cfg.keys {
			if len(key) < MinIntegrityKeyBytes {
				return nil, fmt.Errorf("tuple cache integrity keys must be at least %d bytes: %w", MinIntegrityKeyBytes, sdk.ErrInvalidInput)
			}
		}
		c.ring = &keyRing{keys: cfg.keys}
	}
	return c, nil
}

// Stats returns this backend's counters.
func (c *TupleCache) Stats() Stats {
	return Stats{IntegrityFailures: c.integrityFailures.Load()}
}

// Key returns the Redis key of the mirror for binding, so hosts can log which
// key a runtime uses (pass the runtime's Binding()). Bindings come from
// Source.Binding, which a custom source may fill with anything, so every call
// validates it and an invalid binding returns sdk.ErrInvalidInput.
func (c *TupleCache) Key(binding string) (string, error) {
	if err := validKeyPart("binding", binding); err != nil {
		return "", err
	}
	// The ik: marker sits outside the braces: one hash slot and one mirror per
	// binding, never shared between keyed and unkeyed runtimes.
	prefix := "tuplecache:v3:{"
	if c.ring != nil {
		prefix = "tuplecache:v3:ik:{"
	}
	if c.namespace == "" {
		return prefix + binding + "}", nil
	}
	return prefix + c.namespace + "/" + binding + "}", nil
}

// validKeyPart keeps key parts readable. Rejecting braces prevents a part from
// escaping the hash tag shared by the mirror and its temporary rebuild hashes.
func validKeyPart(name, part string) error {
	if part == "" {
		return fmt.Errorf("tuple cache %s is empty: %w", name, sdk.ErrInvalidInput)
	}
	for _, ch := range part {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == ':', ch == '.', ch == '_', ch == '-', ch == '/':
		default:
			return fmt.Errorf("tuple cache %s must contain only ASCII letters, digits or :._-/: %w", name, sdk.ErrInvalidInput)
		}
	}
	return nil
}

// State reports FullAt from the mirror's last full publication, on Redis TIME.
// A keyed mirror whose header fails verification reports the zero State, so
// the next poll rebuilds it in full.
func (c *TupleCache) State(ctx context.Context, binding string) (tuplecache.State, error) {
	key, err := c.Key(binding)
	if err != nil {
		return tuplecache.State{}, err
	}
	h, _, err := c.header(ctx, key)
	if err != nil || h.binding == "" {
		return tuplecache.State{}, err
	}
	if c.ring != nil {
		if _, ok := c.ring.verify(h); !ok || h.binding != binding {
			c.integrityFailures.Add(1)
			return tuplecache.State{}, nil
		}
	}
	return tuplecache.State{Binding: h.binding, Receipt: h.receipt, FullAt: fullAt(h.epochAt)}, nil
}

// header returns the stored header and the Redis server time in milliseconds.
func (c *TupleCache) header(ctx context.Context, key string) (header, int64, error) {
	values, err := headerScript.Run(ctx, c.client, []string{key}).Slice()
	if err != nil {
		return header{}, 0, unavailable(err)
	}
	if len(values) != 7 {
		return header{}, 0, tuplecache.ErrUnavailable
	}
	var parts [6]string
	for i := range parts {
		s, ok := values[i].(string)
		if !ok {
			return header{}, 0, tuplecache.ErrUnavailable
		}
		parts[i] = s
	}
	now, ok := values[6].(int64)
	if !ok || now <= 0 {
		return header{}, 0, tuplecache.ErrUnavailable
	}
	return header{location: key, binding: parts[0], receipt: parts[1], until: parts[2], epoch: parts[3], epochAt: parts[4], mac: parts[5]}, now, nil
}

// fullAt parses !epoch_at; a mirror written before it existed reports zero.
func fullAt(epochAt string) time.Time {
	ms, err := strconv.ParseInt(epochAt, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func (c *TupleCache) Read(ctx context.Context, expected tuplecache.State, keys []tuplecache.SetKey) ([][]tuples.Tuple, error) {
	if expected.Binding == "" || expected.Receipt == "" {
		return nil, tuplecache.ErrUnavailable
	}
	key, err := c.Key(expected.Binding)
	if err != nil {
		return nil, err
	}
	// Even absent sets return two JSON bytes. Bound request fan-out before
	// allocating arguments or asking Redis to inspect each field.
	if len(keys) > c.limits.MaxReadBytes/2 {
		return nil, tuplecache.ErrCapacity
	}
	args := make([]any, 3, 3+len(keys))
	args[0], args[1], args[2] = expected.Binding, expected.Receipt, c.limits.MaxReadBytes
	for _, key := range keys {
		if key.Validate() != nil {
			return nil, fmt.Errorf("invalid tuple set key: %w", sdk.ErrInvalidInput)
		}
		args = append(args, setField(key))
	}
	started := time.Now()
	values, err := readScript.Run(ctx, c.client, []string{key}, args...).Slice()
	if err != nil {
		return nil, unavailable(err)
	}
	if len(values) == 1 && values[0] == int64(-2) {
		return nil, tuplecache.ErrCapacity
	}
	const headerValues = 5
	if len(values) != len(keys)+headerValues {
		return nil, tuplecache.ErrUnavailable
	}
	remaining, ok := values[0].(int64)
	if !ok || remaining <= 0 || time.Since(started).Milliseconds() >= remaining {
		return nil, tuplecache.ErrUnavailable
	}
	var stored [headerValues - 1]string
	for i := range stored {
		if stored[i], ok = values[1+i].(string); !ok {
			return nil, tuplecache.ErrUnavailable
		}
	}
	var signer *factSigner
	if c.ring != nil {
		h := header{location: key, binding: expected.Binding, receipt: expected.Receipt, until: stored[0], epoch: stored[1], epochAt: stored[2], mac: stored[3]}
		key, ok := c.ring.verify(h)
		if !ok {
			c.integrityFailures.Add(1)
			return nil, ErrIntegrity
		}
		signer = newFactSigner(key, expected.Binding, h.epoch)
	}
	result := make([][]tuples.Tuple, len(keys))
	for i, value := range values[headerValues:] {
		s, ok := value.(string)
		if !ok {
			return nil, tuplecache.ErrUnavailable
		}
		refs, err := decodeSet(s, keys[i], signer)
		if errors.Is(err, ErrIntegrity) {
			c.integrityFailures.Add(1)
			return nil, err
		}
		if err != nil {
			return nil, unavailable(err)
		}
		result[i] = refs
	}
	if time.Since(started).Milliseconds() >= remaining {
		return nil, tuplecache.ErrUnavailable
	}
	return result, nil
}

func (c *TupleCache) Publish(ctx context.Context, expected, next tuplecache.State, snapshot tuplecache.Snapshot, validFor time.Duration) error {
	started := time.Now()
	if next.Binding == "" || next.Receipt == "" || (expected.Binding == "") != (expected.Receipt == "") {
		return fmt.Errorf("invalid tuple cache publication state: %w", sdk.ErrInvalidInput)
	}
	if expected.Binding != "" && expected.Binding != next.Binding {
		return tuplecache.ErrBinding
	}
	if expected.Binding == next.Binding && expected.Receipt == next.Receipt && (snapshot.Full || len(snapshot.Changes) != 0) {
		return fmt.Errorf("tuple changes require a new delivery receipt: %w", sdk.ErrInvalidInput)
	}
	if !snapshot.Full && expected.Binding == "" {
		return tuplecache.ErrUnavailable
	}
	key, err := c.Key(next.Binding)
	if err != nil {
		return err
	}
	if validFor < time.Millisecond {
		return tuplecache.ErrUnavailable
	}
	stored, serverTime, err := c.header(ctx, key)
	if err != nil {
		return err
	}
	// Subtract the entire first round trip conservatively. The resulting server
	// deadline cannot gain freshness from network delays or full-mirror building.
	remaining := validFor - time.Since(started)
	if remaining < time.Millisecond {
		return tuplecache.ErrUnavailable
	}
	deadline := serverTime + remaining.Milliseconds()
	if snapshot.Full {
		return c.publishFull(ctx, key, expected, next, snapshot.Tuples, deadline, serverTime, stored)
	}
	// Deltas extend the stored epoch, so they need its header; the script
	// compares the receipt and epoch again atomically.
	if stored.binding != expected.Binding || stored.receipt != expected.Receipt {
		return tuplecache.ErrConflict
	}
	var signer *factSigner
	mac := ""
	if c.ring != nil {
		key, ok := c.ring.verify(stored)
		if !ok {
			c.integrityFailures.Add(1)
			return ErrIntegrity
		}
		signer = newFactSigner(key, next.Binding, stored.epoch)
		mac = header{location: stored.location, binding: next.Binding, receipt: next.Receipt, until: strconv.FormatInt(deadline, 10), epoch: stored.epoch, epochAt: stored.epochAt}.sign(key)
	}
	payload, fields, err := changesWire(snapshot.Changes, c.limits.MaxMutationBytes, signer)
	if err != nil {
		return err
	}
	args := []any{expected.Binding, expected.Receipt, next.Binding, next.Receipt, deadline, string(payload), c.limits.MaxMutationBytes, c.expiry.Milliseconds(), stored.epoch, mac}
	for _, field := range fields {
		args = append(args, field)
	}
	status, err := deltaScript.Run(ctx, c.client, []string{key}, args...).Int()
	return publicationError(status, err)
}

// publishFull mints a new epoch, so every fact is re-tagged under the current
// key and replayed tags from earlier epochs stop verifying.
func (c *TupleCache) publishFull(ctx context.Context, key string, expected, next tuplecache.State, tuples []tuples.Tuple, deadline, serverTime int64, stored header) error {
	var epochBytes [16]byte
	if _, err := rand.Read(epochBytes[:]); err != nil {
		return err
	}
	epoch, epochAt := hex.EncodeToString(epochBytes[:]), strconv.FormatInt(serverTime, 10)
	var signer *factSigner
	mac, replaceUnverified := "", ""
	if c.ring != nil {
		current := c.ring.keys[0]
		signer = newFactSigner(current, next.Binding, epoch)
		mac = header{location: key, binding: next.Binding, receipt: next.Receipt, until: strconv.FormatInt(deadline, 10), epoch: epoch, epochAt: epochAt}.sign(current)
		// State reported this mirror absent because its header failed
		// verification or names another binding; replace it only while it is
		// unchanged.
		if _, ok := c.ring.verify(stored); expected.Binding == "" && stored.binding != "" && (!ok || stored.binding != next.Binding) {
			replaceUnverified = "1"
		}
	}
	sets, err := fullSets(tuples, c.limits.MaxMutationBytes, signer)
	if err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := key + ":build:" + hex.EncodeToString(nonce[:])
	// The first command creates the temporary hash and its TTL together. Every
	// subsequent chunk only extends its contents, never its bounded lifetime.
	if err := prepareScript.Run(ctx, c.client, []string{temporary}, temporaryTTL.Milliseconds()).Err(); err != nil {
		return unavailable(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = c.client.Del(cleanup, temporary).Err()
	}()
	args := make([]any, 0, 2*fieldChunk)
	chunkBytes := 0
	for field, refs := range sets {
		value, err := json.Marshal(refs)
		if err != nil {
			return err
		}
		if len(args) > 0 && (len(args) == 2*fieldChunk || len(field)+len(value) > c.limits.MaxMutationBytes-chunkBytes) {
			if err := buildScript.Run(ctx, c.client, []string{temporary}, args...).Err(); err != nil {
				return unavailable(err)
			}
			args = args[:0]
			chunkBytes = 0
		}
		args = append(args, field, string(value))
		chunkBytes += len(field) + len(value)
	}
	if len(args) != 0 {
		if err := buildScript.Run(ctx, c.client, []string{temporary}, args...).Err(); err != nil {
			return unavailable(err)
		}
	}
	// A temporary key may have expired or been evicted during construction. The
	// prepared marker must survive; setting the ready marker alone is insufficient.
	status, err := replaceScript.Run(ctx, c.client, []string{key, temporary}, expected.Binding, expected.Receipt,
		next.Binding, next.Receipt, deadline, c.expiry.Milliseconds(), epoch, epochAt, mac,
		replaceUnverified, stored.receipt, stored.mac).Int()
	return publicationError(status, err)
}

func unavailable(err error) error {
	return fmt.Errorf("tuple cache Redis: %w: %w", tuplecache.ErrUnavailable, err)
}

func publicationError(status int, err error) error {
	if err != nil {
		return unavailable(err)
	}
	switch status {
	case 1:
		return nil
	case 0:
		return tuplecache.ErrConflict
	case -2:
		return tuplecache.ErrCapacity
	default:
		return tuplecache.ErrUnavailable
	}
}

// Base64 keeps Lua JSON independent of reference syntax and Unicode normalization.
// Scope kind is explicit; the remaining six entries preserve validated UTF-8 bytes.
type wireTuple [7]string

// wireFact is a stored fact: the seven wire fields, then the tag on a keyed mirror.
type wireFact []string

type wireChange struct {
	Remove  bool     `json:"remove"`
	Forward string   `json:"forward"`
	Reverse string   `json:"reverse"`
	Tuple   wireFact `json:"tuple"`
}

// seal appends the fact tag; a nil signer leaves an unkeyed seven-field fact.
func (s *factSigner) seal(w wireTuple) wireFact {
	if s == nil {
		return w[:]
	}
	return append(w[:], s.tag(w))
}

func toWire(t tuples.Tuple) wireTuple {
	kind := "1"
	if t.Scope.Kind == tuples.ResourceScope {
		kind = "2"
	}
	w := wireTuple{kind}
	for i, v := range []string{t.Scope.Type, t.Scope.ID, t.Relation, t.Subject.Type, t.Subject.ID, t.Subject.Relation} {
		w[i+1] = base64.RawURLEncoding.EncodeToString([]byte(v))
	}
	return w
}
func setField(key tuplecache.SetKey) string {
	var value any
	prefix := "f:"
	if key.Reverse {
		prefix = "r:"
		value = []string{b64(key.Subject.Type), b64(key.Subject.ID), b64(key.Subject.Relation)}
	} else {
		kind := "1"
		if key.Scope.Kind == tuples.ResourceScope {
			kind = "2"
		}
		value = []string{kind, b64(key.Scope.Type), b64(key.Scope.ID), b64(key.Relation)}
	}
	encoded, _ := json.Marshal(value)
	return prefix + string(encoded)
}
func b64(v string) string { return base64.RawURLEncoding.EncodeToString([]byte(v)) }
func tupleFields(t tuples.Tuple) (string, string) {
	return setField(tuplecache.SetKey{Scope: t.Scope, Relation: t.Relation}), setField(tuplecache.SetKey{Reverse: true, Subject: t.Subject})
}
func changesWire(changes []tuplecache.Change, maxBytes int, signer *factSigner) ([]byte, []string, error) {
	if maxBytes < 2 {
		return nil, nil, tuplecache.ErrCapacity
	}
	payload := []byte{'['}
	var fields []string
	seen := make(map[string]bool)
	for i, change := range changes {
		if change.Before == nil && change.After == nil {
			return nil, nil, fmt.Errorf("empty tuple change %d: %w", i, sdk.ErrInvalidInput)
		}
		for _, part := range []struct {
			tuple  *tuples.Tuple
			remove bool
		}{{change.Before, true}, {change.After, false}} {
			if part.tuple == nil {
				continue
			}
			if err := part.tuple.Validate(); err != nil {
				return nil, nil, err
			}
			forward, reverse := tupleFields(*part.tuple)
			encoded, err := json.Marshal(wireChange{Remove: part.remove, Forward: forward, Reverse: reverse, Tuple: signer.seal(toWire(*part.tuple))})
			if err != nil {
				return nil, nil, err
			}
			comma := 0
			if len(payload) > 1 {
				comma = 1
			}
			if len(encoded)+comma > maxBytes-len(payload)-1 {
				return nil, nil, tuplecache.ErrCapacity
			}
			if comma != 0 {
				payload = append(payload, ',')
			}
			payload = append(payload, encoded...)
			for _, field := range []string{forward, reverse} {
				if !seen[field] {
					fields = append(fields, field)
					seen[field] = true
				}
			}
		}
	}
	return append(payload, ']'), fields, nil
}
func fullSets(facts []tuples.Tuple, maxBytes int, signer *factSigner) (map[string][]wireFact, error) {
	sets := make(map[string][]wireFact)
	seen := make(map[string]map[wireTuple]bool)
	sizes := make(map[string]int)
	add := func(field string, identity wireTuple, fact wireFact) error {
		if seen[field] == nil {
			seen[field] = make(map[wireTuple]bool)
			sizes[field] = len(field) + 2
		}
		if !seen[field][identity] {
			// Brackets, quotes and commas: 22 bytes for seven fields, 25 for eight.
			size := 3*len(fact) + 1
			for _, part := range fact {
				size += len(part)
			}
			if len(sets[field]) > 0 {
				size++
			}
			if size > maxBytes-sizes[field] {
				return tuplecache.ErrCapacity
			}
			sizes[field] += size
			sets[field] = append(sets[field], fact)
			seen[field][identity] = true
		}
		return nil
	}
	for _, fact := range facts {
		if err := fact.Validate(); err != nil {
			return nil, err
		}
		forward, reverse := tupleFields(fact)
		wire := toWire(fact)
		stored := signer.seal(wire)
		if err := add(forward, wire, stored); err != nil {
			return nil, err
		}
		if err := add(reverse, wire, stored); err != nil {
			return nil, err
		}
	}
	return sets, nil
}

// decodeSet checks shape and placement, and on a keyed mirror every fact's
// width and tag; a missing or wrong tag returns ErrIntegrity.
func decodeSet(value string, key tuplecache.SetKey, signer *factSigner) ([]tuples.Tuple, error) {
	var encoded [][]string
	if err := json.Unmarshal([]byte(value), &encoded); err != nil {
		return nil, err
	}
	if encoded == nil {
		return nil, fmt.Errorf("null tuple set")
	}
	result := make([]tuples.Tuple, 0, len(encoded))
	seen := make(map[tuples.Tuple]bool, len(encoded))
	for _, parts := range encoded {
		if signer != nil {
			if len(parts) != 8 || !signer.valid(wireTuple(parts[:7]), parts[7]) {
				return nil, ErrIntegrity
			}
			parts = parts[:7]
		}
		if len(parts) != 7 || (parts[0] != "1" && parts[0] != "2") {
			return nil, fmt.Errorf("invalid canonical tuple shape")
		}
		var decoded [6]string
		for i, part := range parts[1:] {
			bytes, err := base64.RawURLEncoding.Strict().DecodeString(part)
			if err != nil {
				return nil, err
			}
			decoded[i] = string(bytes)
		}
		kind := tuples.GlobalScope
		if parts[0] == "2" {
			kind = tuples.ResourceScope
		}
		fact := tuples.Tuple{Scope: tuples.Scope{Kind: kind, Type: decoded[0], ID: decoded[1]}, Relation: decoded[2], Subject: tuples.SubjectRef{Type: decoded[3], ID: decoded[4], Relation: decoded[5]}}
		if err := fact.Validate(); err != nil {
			return nil, err
		}
		if seen[fact] || (key.Reverse && fact.Subject != key.Subject) || (!key.Reverse && (fact.Scope != key.Scope || fact.Relation != key.Relation)) {
			return nil, fmt.Errorf("duplicate or misplaced canonical tuple")
		}
		seen[fact] = true
		result = append(result, fact)
	}
	return result, nil
}

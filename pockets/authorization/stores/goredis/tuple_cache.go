// Package goredis implements the authorization TupleCache backend using Redis.
package goredis

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
)

var _ tuplecache.Backend = (*TupleCache)(nil)

// TupleCache stores raw forward and reverse canonical tuple sets in one Redis hash.
// The client and its lifecycle belong to the host. The namespace must be dedicated
// to one authoritative tuple store; arbitrary writes to this hash are unsupported.
type TupleCache struct {
	client *redis.Client
	key    string
	limits Limits
}

// Limits bound encoded tuple work before Redis fetches or transforms raw sets.
// Zero values select defaults: 1 MiB per Read and 4 MiB per delta publication.
// MaxMutationBytes also bounds each full-rebuild field and upload chunk; it
// does not bound the total mirror size. Negative values are invalid.
type Limits struct {
	MaxReadBytes     int
	MaxMutationBytes int
}

type config struct{ limits Limits }

// Option configures a TupleCache at construction.
type Option func(*config)

// WithLimits replaces the complete limits record; zero fields select defaults.
func WithLimits(limits Limits) Option { return func(c *config) { c.limits = limits } }

// NewTupleCache constructs a backend without I/O or background goroutines.
// The namespace is used verbatim in tuplecache:v2:{<namespace>} and must contain
// only ASCII letters, digits, colons, periods, underscores, hyphens or slashes.
// The borrowed client must enable redis.Options.ContextTimeoutEnabled so the
// runtime's read deadline also bounds socket I/O. The client is never modified.
func NewTupleCache(client *redis.Client, namespace string, opts ...Option) (*TupleCache, error) {
	if client == nil || namespace == "" {
		return nil, fmt.Errorf("tuple cache requires a Redis client and namespace: %w", sdk.ErrInvalidInput)
	}
	if !client.Options().ContextTimeoutEnabled {
		return nil, fmt.Errorf("tuple cache Redis client requires ContextTimeoutEnabled: %w", sdk.ErrInvalidInput)
	}
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("nil tuple cache option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if cfg.limits.MaxReadBytes < 0 || cfg.limits.MaxMutationBytes < 0 {
		return nil, fmt.Errorf("negative tuple cache limits: %w", sdk.ErrInvalidInput)
	}
	if cfg.limits.MaxReadBytes == 0 {
		cfg.limits.MaxReadBytes = defaultMaxReadBytes
	}
	if cfg.limits.MaxMutationBytes == 0 {
		cfg.limits.MaxMutationBytes = defaultMaxMutationBytes
	}
	for _, ch := range namespace {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == ':', ch == '.', ch == '_', ch == '-', ch == '/':
		default:
			return nil, fmt.Errorf("tuple cache namespace must contain only ASCII letters, digits or :._-/: %w", sdk.ErrInvalidInput)
		}
	}
	// Keep the namespace readable. Rejecting braces prevents it from escaping
	// the hash tag shared by the mirror and its temporary rebuild hashes.
	key := "tuplecache:v2:{" + namespace + "}"
	return &TupleCache{client: client, key: key, limits: cfg.limits}, nil
}

func (c *TupleCache) State(ctx context.Context) (tuplecache.State, error) {
	values, err := stateScript.Run(ctx, c.client, []string{c.key}).StringSlice()
	if err != nil {
		return tuplecache.State{}, unavailable(err)
	}
	if len(values) != 2 {
		return tuplecache.State{}, tuplecache.ErrUnavailable
	}
	return tuplecache.State{Binding: values[0], Receipt: values[1]}, nil
}

func (c *TupleCache) Read(ctx context.Context, expected tuplecache.State, keys []tuplecache.SetKey) ([][]tuples.Tuple, error) {
	if expected.Binding == "" || expected.Receipt == "" {
		return nil, tuplecache.ErrUnavailable
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
	values, err := readScript.Run(ctx, c.client, []string{c.key}, args...).Slice()
	if err != nil {
		return nil, unavailable(err)
	}
	if len(values) == 1 && values[0] == int64(-2) {
		return nil, tuplecache.ErrCapacity
	}
	if len(values) != len(keys)+1 {
		return nil, tuplecache.ErrUnavailable
	}
	remaining, ok := values[0].(int64)
	if !ok || remaining <= 0 || time.Since(started).Milliseconds() >= remaining {
		return nil, tuplecache.ErrUnavailable
	}
	result := make([][]tuples.Tuple, len(keys))
	for i, value := range values[1:] {
		s, ok := value.(string)
		if !ok {
			return nil, tuplecache.ErrUnavailable
		}
		refs, err := decodeSet(s, keys[i])
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
	if expected == next && (snapshot.Full || len(snapshot.Changes) != 0) {
		return fmt.Errorf("tuple changes require a new delivery receipt: %w", sdk.ErrInvalidInput)
	}
	if !snapshot.Full && expected.Binding == "" {
		return tuplecache.ErrUnavailable
	}
	if validFor < time.Millisecond {
		return tuplecache.ErrUnavailable
	}
	serverTime, err := c.client.Time(ctx).Result()
	if err != nil {
		return unavailable(err)
	}
	// Subtract the entire first round trip conservatively. The resulting server
	// deadline cannot gain freshness from network delays or full-mirror building.
	remaining := validFor - time.Since(started)
	if remaining < time.Millisecond {
		return tuplecache.ErrUnavailable
	}
	deadline := serverTime.UnixMilli() + remaining.Milliseconds()
	if snapshot.Full {
		return c.publishFull(ctx, expected, next, snapshot.Tuples, deadline)
	}
	payload, fields, err := changesWire(snapshot.Changes, c.limits.MaxMutationBytes)
	if err != nil {
		return err
	}
	args := []any{expected.Binding, expected.Receipt, next.Binding, next.Receipt, deadline, string(payload), c.limits.MaxMutationBytes}
	for _, field := range fields {
		args = append(args, field)
	}
	status, err := deltaScript.Run(ctx, c.client, []string{c.key}, args...).Int()
	return publicationError(status, err)
}

func (c *TupleCache) publishFull(ctx context.Context, expected, next tuplecache.State, tuples []tuples.Tuple, deadline int64) error {
	sets, err := fullSets(tuples, c.limits.MaxMutationBytes)
	if err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := c.key + ":build:" + hex.EncodeToString(nonce[:])
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
	status, err := replaceScript.Run(ctx, c.client, []string{c.key, temporary}, expected.Binding, expected.Receipt,
		next.Binding, next.Receipt, deadline).Int()
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

type wireChange struct {
	Remove  bool      `json:"remove"`
	Forward string    `json:"forward"`
	Reverse string    `json:"reverse"`
	Tuple   wireTuple `json:"tuple"`
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
func changesWire(changes []tuplecache.Change, maxBytes int) ([]byte, []string, error) {
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
			encoded, err := json.Marshal(wireChange{Remove: part.remove, Forward: forward, Reverse: reverse, Tuple: toWire(*part.tuple)})
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
func fullSets(facts []tuples.Tuple, maxBytes int) (map[string][]wireTuple, error) {
	sets := make(map[string][]wireTuple)
	seen := make(map[string]map[wireTuple]bool)
	sizes := make(map[string]int)
	add := func(field string, fact wireTuple) error {
		if seen[field] == nil {
			seen[field] = make(map[wireTuple]bool)
			sizes[field] = len(field) + 2
		}
		if !seen[field][fact] {
			size := 22
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
			seen[field][fact] = true
		}
		return nil
	}
	for _, fact := range facts {
		if err := fact.Validate(); err != nil {
			return nil, err
		}
		forward, reverse := tupleFields(fact)
		wire := toWire(fact)
		if err := add(forward, wire); err != nil {
			return nil, err
		}
		if err := add(reverse, wire); err != nil {
			return nil, err
		}
	}
	return sets, nil
}
func decodeSet(value string, key tuplecache.SetKey) ([]tuples.Tuple, error) {
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

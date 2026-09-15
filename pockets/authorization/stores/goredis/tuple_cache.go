// Package goredis implements the authorization TupleCache backend using Redis.
package goredis

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/redis/go-redis/v9"
)

const (
	temporaryTTL = 5 * time.Minute
	fieldChunk   = 256
)

var _ tuplecache.Backend = (*TupleCache)(nil)

// TupleCache stores raw forward and reverse relationship sets in one Redis hash.
// The client and its lifecycle belong to the host. The namespace must be dedicated
// to one authoritative tuple store; arbitrary writes to this hash are unsupported.
type TupleCache struct {
	client *redis.Client
	key    string
}

// NewTupleCache constructs a backend without I/O or background goroutines.
func NewTupleCache(client *redis.Client, namespace string) (*TupleCache, error) {
	if client == nil || namespace == "" {
		return nil, fmt.Errorf("tuple cache requires a Redis client and namespace: %w", sdk.ErrInvalidInput)
	}
	// Encode the host namespace so braces and separators cannot alias another
	// namespace. Temporary rebuild hashes use this same Redis cluster hash tag.
	key := "gopernicus:tuplecache:{" + base64.RawURLEncoding.EncodeToString([]byte(namespace)) + "}:mirror"
	return &TupleCache{client: client, key: key}, nil
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

func (c *TupleCache) Read(ctx context.Context, expected tuplecache.State, keys []tuplecache.SetKey) ([][]relationships.SubjectRef, error) {
	if expected.Binding == "" || expected.Receipt == "" {
		return nil, tuplecache.ErrUnavailable
	}
	args := make([]any, 2, 2+len(keys))
	args[0], args[1] = expected.Binding, expected.Receipt
	for _, key := range keys {
		if !validRef(key.Ref, !key.Reverse) {
			return nil, fmt.Errorf("invalid tuple set key: %w", sdk.ErrInvalidInput)
		}
		args = append(args, setField(key.Reverse, toWire(key.Ref)))
	}
	started := time.Now()
	values, err := readScript.Run(ctx, c.client, []string{c.key}, args...).Slice()
	if err != nil {
		return nil, unavailable(err)
	}
	if len(values) != len(keys)+1 {
		return nil, tuplecache.ErrUnavailable
	}
	remaining, ok := values[0].(int64)
	if !ok || remaining <= 0 || time.Since(started).Milliseconds() >= remaining {
		return nil, tuplecache.ErrUnavailable
	}
	result := make([][]relationships.SubjectRef, len(keys))
	for i, value := range values[1:] {
		s, ok := value.(string)
		if !ok {
			return nil, tuplecache.ErrUnavailable
		}
		refs, err := decodeSet(s, keys[i].Reverse)
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
	ops, err := changesWire(snapshot.Changes)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	status, err := deltaScript.Run(ctx, c.client, []string{c.key}, expected.Binding, expected.Receipt,
		next.Binding, next.Receipt, deadline, string(payload)).Int()
	return publicationError(status, err)
}

func (c *TupleCache) publishFull(ctx context.Context, expected, next tuplecache.State, tuples []relationships.CreateRelationship, deadline int64) error {
	sets, err := fullSets(tuples)
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
	for field, refs := range sets {
		value, err := json.Marshal(refs)
		if err != nil {
			return err
		}
		args = append(args, field, string(value))
		if len(args) == 2*fieldChunk {
			if err := buildScript.Run(ctx, c.client, []string{temporary}, args...).Err(); err != nil {
				return unavailable(err)
			}
			args = args[:0]
		}
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
	default:
		return tuplecache.ErrUnavailable
	}
}

// Base64 preserves exact Go string bytes, including invalid UTF-8, while keeping
// Redis Lua's JSON codec independent from tuple syntax and Unicode normalization.
type wireRef [3]string

type wireChange struct {
	Remove   bool    `json:"remove"`
	Forward  string  `json:"forward"`
	Subject  wireRef `json:"subject"`
	Reverse  string  `json:"reverse"`
	Resource wireRef `json:"resource"`
}

func toWire(ref relationships.SubjectRef) wireRef {
	return wireRef{base64.RawURLEncoding.EncodeToString([]byte(ref.Type)), base64.RawURLEncoding.EncodeToString([]byte(ref.ID)), base64.RawURLEncoding.EncodeToString([]byte(ref.Relation))}
}

func setField(reverse bool, ref wireRef) string {
	data, _ := json.Marshal(ref)
	if reverse {
		return "r:" + string(data)
	}
	return "f:" + string(data)
}

func tupleRefs(tuple relationships.CreateRelationship) (relationships.SubjectRef, relationships.SubjectRef, error) {
	resource := relationships.SubjectRef{Type: tuple.ResourceType, ID: tuple.ResourceID, Relation: tuple.Relation}
	subject := relationships.SubjectRef{Type: tuple.SubjectType, ID: tuple.SubjectID, Relation: tuple.SubjectRelation}
	if !validRef(resource, true) || !validRef(subject, false) {
		return resource, subject, fmt.Errorf("invalid raw relationship: %w", sdk.ErrInvalidInput)
	}
	return resource, subject, nil
}

func validRef(ref relationships.SubjectRef, relationRequired bool) bool {
	return ref.Type != "" && ref.ID != "" && (!relationRequired || ref.Relation != "")
}

func changesWire(changes []tuplecache.Change) ([]wireChange, error) {
	ops := make([]wireChange, 0, 2*len(changes))
	for i, change := range changes {
		if change.Before == nil && change.After == nil {
			return nil, fmt.Errorf("empty tuple change %s: %w", strconv.Itoa(i), sdk.ErrInvalidInput)
		}
		for _, part := range []struct {
			tuple  *relationships.CreateRelationship
			remove bool
		}{{change.Before, true}, {change.After, false}} {
			if part.tuple == nil {
				continue
			}
			resource, subject, err := tupleRefs(*part.tuple)
			if err != nil {
				return nil, err
			}
			r, s := toWire(resource), toWire(subject)
			ops = append(ops, wireChange{Remove: part.remove, Forward: setField(false, r), Subject: s, Reverse: setField(true, s), Resource: r})
		}
	}
	return ops, nil
}

func fullSets(tuples []relationships.CreateRelationship) (map[string][]wireRef, error) {
	sets := make(map[string][]wireRef)
	seen := make(map[string]map[wireRef]bool)
	add := func(field string, ref wireRef) {
		if seen[field] == nil {
			seen[field] = make(map[wireRef]bool)
		}
		if !seen[field][ref] {
			sets[field] = append(sets[field], ref)
			seen[field][ref] = true
		}
	}
	for _, tuple := range tuples {
		resource, subject, err := tupleRefs(tuple)
		if err != nil {
			return nil, err
		}
		r, s := toWire(resource), toWire(subject)
		add(setField(false, r), s)
		add(setField(true, s), r)
	}
	return sets, nil
}

func decodeSet(value string, resourceRefs bool) ([]relationships.SubjectRef, error) {
	var refs [][]string
	if err := json.Unmarshal([]byte(value), &refs); err != nil {
		return nil, err
	}
	if refs == nil {
		return nil, fmt.Errorf("null tuple set")
	}
	result := make([]relationships.SubjectRef, 0, len(refs))
	seen := make(map[relationships.SubjectRef]bool, len(refs))
	for _, parts := range refs {
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid tuple reference length")
		}
		decoded := [3]string{}
		for i, part := range parts {
			b, err := base64.RawURLEncoding.Strict().DecodeString(part)
			if err != nil {
				return nil, err
			}
			decoded[i] = string(b)
		}
		ref := relationships.SubjectRef{Type: decoded[0], ID: decoded[1], Relation: decoded[2]}
		if !validRef(ref, resourceRefs) || seen[ref] {
			return nil, fmt.Errorf("invalid or duplicate tuple reference")
		}
		seen[ref] = true
		result = append(result, ref)
	}
	return result, nil
}

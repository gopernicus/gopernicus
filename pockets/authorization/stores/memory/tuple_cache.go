package memory

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

// TupleCache is the in-memory reference backend for a complete raw mirror.
// It is separate from Store, which owns the authoritative in-memory facts.
type TupleCache struct {
	mu    sync.Mutex
	state tuplecache.State
	until time.Time
	sets  map[tuplecache.SetKey][]relationships.SubjectRef
}

func NewTupleCache() *TupleCache { return &TupleCache{} }

func (c *TupleCache) State(ctx context.Context) (tuplecache.State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tuplecache.State{}, err
	}
	return c.state, nil
}

func (c *TupleCache) Read(ctx context.Context, expected tuplecache.State, keys []tuplecache.SetKey) ([][]relationships.SubjectRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expected != c.state || c.state.Receipt == "" || !time.Now().Before(c.until) {
		return nil, tuplecache.ErrUnavailable
	}
	out := make([][]relationships.SubjectRef, len(keys))
	for i, key := range keys {
		out[i] = slices.Clone(c.sets[key])
	}
	return out, nil
}

func (c *TupleCache) Publish(ctx context.Context, expected, next tuplecache.State, snapshot tuplecache.Snapshot, validFor time.Duration) error {
	started := time.Now()
	if validFor <= 0 || next.Binding == "" || next.Receipt == "" || ((snapshot.Full || len(snapshot.Changes) > 0) && next == expected) {
		return fmt.Errorf("tuple cache: invalid publication: %w", sdk.ErrInvalidInput)
	}
	if !snapshot.Full && (expected.Binding != next.Binding || expected.Receipt == "") {
		return tuplecache.ErrConflict
	}
	if expected.Binding != "" && expected.Binding != next.Binding {
		return tuplecache.ErrBinding
	}
	for _, t := range snapshot.Tuples {
		if err := t.Validate(); err != nil {
			return err
		}
	}
	if !snapshot.Full {
		for _, change := range snapshot.Changes {
			if change.Before == nil && change.After == nil {
				return fmt.Errorf("tuple cache: empty change: %w", sdk.ErrInvalidInput)
			}
			for _, t := range []*relationships.CreateRelationship{change.Before, change.After} {
				if t != nil {
					if err := t.Validate(); err != nil {
						return err
					}
				}
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.state != expected {
		return tuplecache.ErrConflict
	}
	sets := maps.Clone(c.sets)
	if sets == nil || snapshot.Full {
		sets = make(map[tuplecache.SetKey][]relationships.SubjectRef)
	}
	mutate := func(t relationships.CreateRelationship, add bool) {
		resource := relationships.SubjectRef{Type: t.ResourceType, ID: t.ResourceID, Relation: t.Relation}
		subject := t.Subject()
		for key, ref := range map[tuplecache.SetKey]relationships.SubjectRef{{Ref: resource}: subject, {Reverse: true, Ref: subject}: resource} {
			refs := slices.Clone(sets[key])
			if add {
				if !slices.Contains(refs, ref) {
					refs = append(refs, ref)
				}
			} else {
				refs = slices.DeleteFunc(refs, func(r relationships.SubjectRef) bool { return r == ref })
			}
			if len(refs) == 0 {
				delete(sets, key)
			} else {
				sets[key] = refs
			}
		}
	}
	if snapshot.Full {
		for _, t := range snapshot.Tuples {
			mutate(t, true)
		}
	} else {
		for _, change := range snapshot.Changes {
			if change.Before != nil {
				mutate(*change.Before, false)
			}
			if change.After != nil {
				mutate(*change.After, true)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	until := started.Add(validFor)
	if !time.Now().Before(until) {
		return tuplecache.ErrUnavailable
	}
	c.sets, c.state, c.until = sets, next, until
	return nil
}

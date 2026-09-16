// Package audit records actual authorization fact changes. Attribution is
// metadata supplied by a trusted host path, never permission to perform a write.
package audit

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

const MaxReasonLen = 1024

// Source attributes a change to exactly one complete actor pair or an explicit
// system source. Reason is optional UTF-8 context of at most MaxReasonLen bytes;
// NUL is forbidden so all bundled stores can persist it directly.
type Source struct {
	ActorType string `json:"actor_type,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	System    string `json:"system,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func (s Source) Validate() error {
	actor := s.ActorType != "" || s.ActorID != ""
	if actor == (s.System != "") {
		return fmt.Errorf("audit source requires exactly one actor or system: %w", sdk.ErrInvalidInput)
	}
	if actor {
		if err := tuples.ValidateRefField("audit actor type", s.ActorType); err != nil {
			return err
		}
		if err := tuples.ValidateRefField("audit actor id", s.ActorID); err != nil {
			return err
		}
	} else if err := tuples.ValidateRefField("audit system", s.System); err != nil {
		return err
	}
	if len(s.Reason) > MaxReasonLen || !utf8.ValidString(s.Reason) || strings.ContainsRune(s.Reason, 0) {
		return fmt.Errorf("audit reason must be valid UTF-8 without NUL and at most %d bytes: %w", MaxReasonLen, sdk.ErrInvalidInput)
	}
	return nil
}

type sourceKey struct{}

// WithSource attaches audit metadata. Recording stores validate it before work;
// the value cannot confer authority or select an unguarded mutation path.
func WithSource(ctx context.Context, source Source) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

func SourceFromContext(ctx context.Context) (Source, error) {
	source, _ := ctx.Value(sourceKey{}).(Source)
	if err := source.Validate(); err != nil {
		return Source{}, err
	}
	return source, nil
}

type Action string

const (
	ActionAdded   Action = "added"
	ActionRemoved Action = "removed"
)

// Change identifies one canonical fact delta. A swap is one removal and one addition.
type Change struct {
	Action Action       `json:"action"`
	Tuple  tuples.Tuple `json:"tuple"`
}

func (c Change) Validate() error {
	if c.Action != ActionAdded && c.Action != ActionRemoved {
		return fmt.Errorf("invalid audit action %q: %w", c.Action, sdk.ErrInvalidInput)
	}
	return c.Tuple.Validate()
}

const EncodingTuple = "tuple/v2"

// Record stores one actual fact change. EventID groups the records of one
// changed operation; it is generated internally and carries no replay semantics.
// ID appends a colon and a one-based, 20-digit ordinal to that event ID.
type Record struct {
	Encoding   string    `json:"encoding"`
	ID         string    `json:"id"`
	EventID    string    `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
	Source     Source    `json:"source"`
	Change     Change    `json:"change"`
}

// Filter uses exact optional resource, subject and actor pairs. Both fields in
// each pair must be supplied or both empty. An empty pair applies no filter.
type Filter struct {
	ResourceType string
	ResourceID   string
	SubjectType  string
	SubjectID    string
	ActorType    string
	ActorID      string
}

func (f Filter) Validate() error {
	for _, pair := range []struct{ name, a, b string }{
		{"resource", f.ResourceType, f.ResourceID}, {"subject", f.SubjectType, f.SubjectID}, {"actor", f.ActorType, f.ActorID},
	} {
		if pair.a == "" && pair.b == "" {
			continue
		}
		if err := tuples.ValidateRefField("audit filter "+pair.name+" type", pair.a); err != nil {
			return err
		}
		if err := tuples.ValidateRefField("audit filter "+pair.name+" id", pair.b); err != nil {
			return err
		}
	}
	return nil
}

var OrderFields = map[string]list.OrderField{"occurred_at": {Column: "occurred_at"}}
var DefaultOrder = list.NewOrder("occurred_at", list.DESC)

// Reader lists retained history even when recording new changes is disabled.
// Records order by occurred_at then ID in the requested direction (default DESC).
// Both cursor and offset strategies support optional totals. Records contain
// owned values; mutating a result cannot change retained history.
// Hosts own access control, retention and presentation; no audit HTTP route is
// installed by this capability.
type Reader interface {
	List(ctx context.Context, filter Filter, req list.Request) (list.Page[Record], error)
}

// NewRecords validates and owns actual deltas, removes duplicate changes and
// cancels opposing changes to the same fact, then sorts by fact identity. One
// generated EventID groups all remaining changes. No net change yields no records.
// OccurredAt is UTC microsecond precision for consistent datastore round trips.
func NewRecords(ctx context.Context, changes []Change, now time.Time) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, err := SourceFromContext(ctx)
	if err != nil {
		return nil, err
	}
	type delta struct {
		change         Change
		added, removed bool
	}
	byKey := make(map[tuples.Tuple]delta, len(changes))
	for _, c := range changes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := c.Validate(); err != nil {
			return nil, err
		}
		key := c.Tuple
		d := byKey[key]
		d.change = c
		if c.Action == ActionAdded {
			d.added = true
		} else {
			d.removed = true
		}
		byKey[key] = d
	}
	keys := make([]tuples.Tuple, 0, len(byKey))
	for key, d := range byKey {
		if d.added != d.removed {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, tuples.Compare)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return []Record{}, nil
	}
	eventID, err := (sdk.IDGenerator{}).Generate()
	if err != nil {
		return nil, err
	}
	occurredAt := now.UTC().Truncate(time.Microsecond)
	out := make([]Record, 0, len(keys))
	for i, key := range keys {
		d := byKey[key]
		if d.added {
			d.change.Action = ActionAdded
		} else {
			d.change.Action = ActionRemoved
		}
		out = append(out, Record{Encoding: EncodingTuple, ID: fmt.Sprintf("%s:%020d", eventID, i+1), EventID: eventID, OccurredAt: occurredAt, Source: source, Change: d.change})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

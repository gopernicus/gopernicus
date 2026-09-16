package tuples

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var (
	ErrSnapshotClosed    = fmt.Errorf("authorization tuple snapshot closed: %w", sdk.ErrUnavailable)
	ErrSnapshotIsolation = fmt.Errorf("authorization requires repeatable-read or serializable ambient isolation: %w", sdk.ErrInvalidInput)
	ErrReadLimit         = fmt.Errorf("authorization tuple read limit exceeded: %w", sdk.ErrUnavailable)
)

// SetKey selects either one scope/relation or all facts naming an exact subject.
// Unused fields must be zero. Reverse reads include global facts but do not turn
// them into resource references for graph traversal.
type SetKey struct {
	Reverse  bool
	Scope    Scope
	Relation string
	Subject  SubjectRef
}

func (k SetKey) Validate() error {
	if k.Reverse {
		if k.Scope != (Scope{}) || k.Relation != "" {
			return fmt.Errorf("reverse tuple set has resource coordinates: %w", sdk.ErrInvalidInput)
		}
		return k.Subject.Validate()
	}
	if k.Subject != (SubjectRef{}) {
		return fmt.Errorf("forward tuple set has a subject: %w", sdk.ErrInvalidInput)
	}
	if err := k.Scope.Validate(); err != nil {
		return err
	}
	return ValidateRefField("relation", k.Relation)
}

// Query filters canonical facts. Empty string filters are unconstrained; Scope
// explicitly selects global or resource scope. SubjectType and SubjectID are
// independent filters across concrete subjects and usersets; Subject is exact.
// After is the last tuple from a prior result; continuation uses Compare's
// full identity order. Limit zero
// requests an unbounded raw read; decision services always supply positive bounds.
type Query struct {
	Scope        *Scope
	ResourceOnly bool
	ResourceType string
	Relation     string
	Subject      *SubjectRef
	SubjectType  string
	SubjectID    string
	ConcreteOnly bool
	After        *Tuple
	Limit        int
}

func (q Query) Validate() error {
	if q.Limit < 0 {
		return fmt.Errorf("negative tuple limit: %w", sdk.ErrInvalidInput)
	}
	if q.Scope != nil {
		if err := q.Scope.Validate(); err != nil {
			return err
		}
		if q.ResourceOnly && q.Scope.Kind != ResourceScope {
			return fmt.Errorf("resource-only tuple query names global scope: %w", sdk.ErrInvalidInput)
		}
	}
	if q.ResourceType != "" {
		if err := ValidateRefField("resource type", q.ResourceType); err != nil {
			return err
		}
	}
	if q.Scope != nil && q.ResourceType != "" && (q.Scope.Kind != ResourceScope || q.Scope.Type != q.ResourceType) {
		return fmt.Errorf("tuple scope and resource type disagree: %w", sdk.ErrInvalidInput)
	}
	if q.Relation != "" {
		if err := ValidateRefField("relation", q.Relation); err != nil {
			return err
		}
	}
	if q.Subject != nil {
		if err := q.Subject.Validate(); err != nil {
			return err
		}
		if (q.SubjectType != "" && q.SubjectType != q.Subject.Type) || (q.SubjectID != "" && q.SubjectID != q.Subject.ID) {
			return fmt.Errorf("tuple subject filters disagree: %w", sdk.ErrInvalidInput)
		}
	}
	for _, field := range []struct{ name, value string }{{"subject type", q.SubjectType}, {"subject id", q.SubjectID}} {
		if field.value != "" {
			if err := ValidateRefField(field.name, field.value); err != nil {
				return err
			}
		}
	}
	if q.ConcreteOnly && q.Subject != nil && q.Subject.IsUserset() {
		return fmt.Errorf("concrete tuple query names a userset: %w", sdk.ErrInvalidInput)
	}
	if q.After != nil {
		return q.After.Validate()
	}
	return nil
}

func (q Query) Matches(t Tuple) bool {
	return (q.Scope == nil || *q.Scope == t.Scope) &&
		(!q.ResourceOnly || t.Scope.Kind == ResourceScope) &&
		(q.ResourceType == "" || (t.Scope.Kind == ResourceScope && q.ResourceType == t.Scope.Type)) &&
		(q.Relation == "" || q.Relation == t.Relation) &&
		(!q.ConcreteOnly || !t.Subject.IsUserset()) &&
		(q.SubjectType == "" || q.SubjectType == t.Subject.Type) &&
		(q.SubjectID == "" || q.SubjectID == t.Subject.ID) &&
		(q.Subject == nil || *q.Subject == t.Subject)
}

// Reader inspects exact raw facts, without policy, fallback or userset expansion.
// ContainsMany and ReadSets preserve input order and return complete results or
// an error. Lookup orders the seven identity components in byte order.
type Reader interface {
	Contains(context.Context, Tuple) (bool, error)
	ContainsMany(context.Context, []Tuple) ([]bool, error)
	// A positive maxResults bounds all returned facts, including repeated keys.
	// Overflow returns ErrReadLimit with no partial result; zero is unbounded.
	ReadSets(ctx context.Context, keys []SetKey, maxResults int) ([][]Tuple, error)
	Lookup(context.Context, Query) ([]Tuple, error)
}

// Snapshotter supplies one coherent operation view, closes it on every exit and
// discards provisional results if completion fails. A borrowed reader is valid
// only during its callback. Suitable ambient transactions retain pending writes;
// an unsuitable ambient transaction returns ErrSnapshotIsolation before reads.
type Snapshotter interface {
	ReadTupleSnapshot(context.Context, func(context.Context, Reader) error) error
}

// Changes is an atomic exact-fact update. Duplicates within a set are idempotent;
// a fact appearing in both sets is invalid. Stores must not split the commit.
type Changes struct{ Add, Remove []Tuple }

func (c Changes) Validate() error {
	added := make(map[Tuple]struct{}, len(c.Add))
	for _, t := range c.Add {
		if err := t.Validate(); err != nil {
			return err
		}
		added[t] = struct{}{}
	}
	for _, t := range c.Remove {
		if err := t.Validate(); err != nil {
			return err
		}
		if _, ok := added[t]; ok {
			return fmt.Errorf("tuple appears in both add and remove sets: %w", sdk.ErrInvalidInput)
		}
	}
	return nil
}

// Storer is the canonical raw authority. Actor-facing writes use guarded
// mutations; these trusted operations apply structural validation only.
type Storer interface {
	Reader
	Snapshotter
	ListTuples(context.Context, Query, list.Request) (list.Page[Tuple], error)
	ApplyTuples(context.Context, Changes) error
	ReconcileTuples(context.Context, Scope, string, []SubjectRef) error
	DeleteScope(context.Context, Scope) error
}

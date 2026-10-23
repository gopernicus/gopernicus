package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestSourceValidation(t *testing.T) {
	for _, source := range []Source{{}, {ActorType: "user"}, {ActorID: "u"}, {ActorType: "user", ActorID: "u", System: "job"}, {System: "bad\x01name"}, {System: "job", Reason: strings.Repeat("a", MaxReasonLen+1)}, {System: "job", Reason: "x\x00y"}, {System: "job", Reason: "\xff"}} {
		if _, err := SourceFromContext(WithSource(context.Background(), source)); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("accepted invalid source %+v: %v", source, err)
		}
	}
	for _, source := range []Source{{ActorType: "user", ActorID: "u", Reason: "reason\nmore"}, {System: "sync", Reason: strings.Repeat("a", MaxReasonLen)}} {
		got, err := SourceFromContext(WithSource(context.Background(), source))
		if err != nil || got != source {
			t.Fatalf("source changed: %+v %v", got, err)
		}
	}
}
func TestSourceCorrelationIDs(t *testing.T) {
	whole := strings.Repeat("~", MaxCorrelationIDLen)
	for _, source := range []Source{
		{System: "job", RequestID: whole + "a"},
		{System: "job", TraceID: whole + "a"},
		{System: "job", RequestID: "req\x01"},
		{System: "job", TraceID: "trace\x7f"},
		{System: "job", RequestID: "r\u00e9q"},
		{ActorType: "user", ActorID: "u", RequestID: "req\n"},
	} {
		if err := source.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("accepted invalid correlation %q/%q: %v", source.RequestID, source.TraceID, err)
		}
	}
	for _, source := range []Source{{System: "job"}, {System: "job", RequestID: whole, TraceID: whole}, {ActorType: "user", ActorID: "u", RequestID: " req-1 ", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"}} {
		if err := source.Validate(); err != nil {
			t.Fatalf("refused valid correlation %+v: %v", source, err)
		}
	}
	source := Source{ActorType: "user", ActorID: "u", RequestID: "req-1", TraceID: "trace-1"}
	change := Change{Action: ActionAdded, Tuple: tuples.Tuple{Scope: tuples.Global(), Relation: "editor", Subject: tuples.SubjectRef{Type: "user", ID: "u"}}}
	records, err := NewRecords(WithSource(context.Background(), source), []Change{change}, time.Now())
	if err != nil || len(records) != 1 || records[0].Source != source {
		t.Fatalf("records lost correlation: %+v %v", records, err)
	}
	if _, err := NewRecords(WithSource(context.Background(), Source{System: "job", RequestID: whole + "a"}), []Change{change}, time.Now()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("recorded an over-long request id: %v", err)
	}
}
func TestRecordsOwnCanonicalNetChanges(t *testing.T) {
	ctx := WithSource(context.Background(), Source{System: "sync"})
	a := (relationships.CreateRelationship{ResourceType: "doc", ResourceID: "d", Relation: "viewer", SubjectType: "group", SubjectID: "g", SubjectRelation: "member"}).Tuple()
	b := a
	b.Subject.Relation = "admin"
	roleGrant := tuples.Tuple{Scope: tuples.Global(), Relation: "editor", Subject: tuples.SubjectRef{Type: "user", ID: "u"}}
	now := time.Date(2026, 9, 11, 1, 2, 3, 123456789, time.FixedZone("test", 3600))
	records, err := NewRecords(ctx, []Change{{Action: ActionAdded, Tuple: a}, {Action: ActionAdded, Tuple: b}, {Action: ActionRemoved, Tuple: a}, {Action: ActionAdded, Tuple: roleGrant}, {Action: ActionAdded, Tuple: b}}, now)
	if err != nil || len(records) != 2 {
		t.Fatalf("net changes: %+v %v", records, err)
	}
	if records[0].Change.Tuple != roleGrant || records[1].Change.Tuple != b {
		t.Fatalf("canonical changes: %+v", records)
	}
	for i, r := range records {
		if r.Encoding != EncodingTuple || r.EventID != records[0].EventID || r.ID == "" || !r.OccurredAt.Equal(now.UTC().Truncate(time.Microsecond)) || r.OccurredAt.Location() != time.UTC {
			t.Fatalf("record %d malformed: %+v", i, r)
		}
	}
	if !strings.HasSuffix(records[0].ID, ":00000000000000000001") || !strings.HasSuffix(records[1].ID, ":00000000000000000002") {
		t.Fatalf("ordinal format: %+v", records)
	}
	b.Subject.ID = "changed"
	roleGrant.Relation = "changed"
	if records[1].Change.Tuple.Subject.ID != "g" || records[0].Change.Tuple.Relation != "editor" {
		t.Fatal("input aliases retained history")
	}
	second, err := NewRecords(ctx, []Change{{Action: ActionAdded, Tuple: a}}, now)
	if err != nil || second[0].EventID == records[0].EventID {
		t.Fatalf("operation reused event id: %v", err)
	}
	empty, err := NewRecords(ctx, []Change{{Action: ActionAdded, Tuple: a}, {Action: ActionRemoved, Tuple: a}}, now)
	if err != nil || len(empty) != 0 {
		t.Fatalf("opposing changes not canceled: %+v %v", empty, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := NewRecords(canceled, nil, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
func TestChangeAndFilterValidation(t *testing.T) {
	ctx := WithSource(context.Background(), Source{System: "sync"})
	a := (relationships.CreateRelationship{ResourceType: "doc", ResourceID: "d", Relation: "viewer", SubjectType: "user", SubjectID: "u"}).Tuple()
	for _, c := range []Change{{Action: ActionAdded}, {Action: ActionAdded, Tuple: tuples.Tuple{Scope: tuples.Global(), Relation: "owner"}}, {Action: "changed", Tuple: a}, {Action: ActionRemoved, Tuple: tuples.Tuple{}}} {
		if _, err := NewRecords(ctx, []Change{c}, time.Now()); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid change accepted: %+v %v", c, err)
		}
	}
	for _, f := range []Filter{{ResourceType: "doc"}, {SubjectID: "u"}, {ActorType: "user", ActorID: "bad\x01value"}} {
		if err := f.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid filter accepted: %+v %v", f, err)
		}
	}
}

// Cancel while attribution is read, after NewRecords' initial cancellation check.
// Even an empty change list must observe cancellation before returning success.
type cancelOnSourceContext struct {
	context.Context
	cancel context.CancelFunc
}

func (c cancelOnSourceContext) Value(key any) any {
	value := c.Context.Value(key)
	c.cancel()
	return value
}
func TestEmptyRecordsObserveCancellationAfterSource(t *testing.T) {
	base, cancel := context.WithCancel(WithSource(context.Background(), Source{System: "test"}))
	defer cancel()
	records, err := NewRecords(cancelOnSourceContext{Context: base, cancel: cancel}, nil, time.Now())
	if !errors.Is(err, context.Canceled) || records != nil {
		t.Fatalf("empty canceled records: %+v %v", records, err)
	}
}

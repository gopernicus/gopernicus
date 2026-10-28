package audit

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

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
func counting(prefix string) (sdk.IDGenerator, *int) {
	n := 0
	return sdk.NewIDGenerator(func() (string, error) {
		n++
		return prefix + strconv.Itoa(n), nil
	}), &n
}
func TestNewRecordsWithMintsFromGenerator(t *testing.T) {
	ctx := WithSource(context.Background(), Source{System: "sync"})
	a := (relationships.CreateRelationship{ResourceType: "doc", ResourceID: "d", Relation: "viewer", SubjectType: "user", SubjectID: "u"}).Tuple()
	b := a
	b.Relation = "editor"
	ids, minted := counting("fixed-")
	first, err := NewRecordsWith(ctx, []Change{{Action: ActionAdded, Tuple: a}, {Action: ActionAdded, Tuple: b}}, time.Now(), ids)
	if err != nil || len(first) != 2 {
		t.Fatalf("records: %+v %v", first, err)
	}
	for i, r := range first {
		if want := "fixed-1:" + strings.Repeat("0", 19) + strconv.Itoa(i+1); r.EventID != "fixed-1" || r.ID != want {
			t.Fatalf("record %d: id=%q event=%q want %q", i, r.ID, r.EventID, want)
		}
	}
	second, err := NewRecordsWith(ctx, []Change{{Action: ActionRemoved, Tuple: a}}, time.Now(), ids)
	if err != nil || len(second) != 1 || second[0].EventID != "fixed-2" || second[0].ID != "fixed-2:00000000000000000001" {
		t.Fatalf("second write: %+v %v", second, err)
	}
	if none, err := NewRecordsWith(ctx, []Change{{Action: ActionAdded, Tuple: a}, {Action: ActionRemoved, Tuple: a}}, time.Now(), ids); err != nil || len(none) != 0 || *minted != 2 {
		t.Fatalf("no net change minted or recorded: %+v %v mints=%d", none, err, *minted)
	}
}
func TestNewRecordsDefaultsToUUIDv7(t *testing.T) {
	ctx := WithSource(context.Background(), Source{System: "sync"})
	a := (relationships.CreateRelationship{ResourceType: "doc", ResourceID: "d", Relation: "viewer", SubjectType: "user", SubjectID: "u"}).Tuple()
	for name, build := range map[string]func() ([]Record, error){
		"NewRecords": func() ([]Record, error) {
			return NewRecords(ctx, []Change{{Action: ActionAdded, Tuple: a}}, time.Now())
		},
		"NewRecordsWithZero": func() ([]Record, error) {
			return NewRecordsWith(ctx, []Change{{Action: ActionAdded, Tuple: a}}, time.Now(), sdk.IDGenerator{})
		},
	} {
		records, err := build()
		if err != nil || len(records) != 1 || !uuidV7.MatchString(records[0].EventID) || records[0].ID != records[0].EventID+":00000000000000000001" {
			t.Fatalf("%s: %+v %v", name, records, err)
		}
	}
}
func TestNewRecordsWithRefusesEmptyAndFailedMints(t *testing.T) {
	ctx := WithSource(context.Background(), Source{System: "sync"})
	a := (relationships.CreateRelationship{ResourceType: "doc", ResourceID: "d", Relation: "viewer", SubjectType: "user", SubjectID: "u"}).Tuple()
	changes := []Change{{Action: ActionAdded, Tuple: a}}
	if records, err := NewRecordsWith(ctx, changes, time.Now(), sdk.NewIDGenerator(sdk.DatabaseID)); records != nil || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty mint: %+v %v", records, err)
	}
	failure := errors.New("generator down")
	if records, err := NewRecordsWith(ctx, changes, time.Now(), sdk.NewIDGenerator(func() (string, error) { return "", failure })); records != nil || !errors.Is(err, failure) {
		t.Fatalf("failed mint: %+v %v", records, err)
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

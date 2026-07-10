package events

import (
	"encoding/json"
	"time"
)

// EventEncoder is an optional interface an event may implement to control its
// serialization (protobuf, msgpack, a stable envelope). Events that do not
// implement it are encoded with json.Marshal by EncodeEvent.
type EventEncoder interface {
	EncodeEvent() ([]byte, error)
}

// EncodeEvent serializes an event for transport (the outbox row, a remote
// broker). It uses the event's EventEncoder when present and falls back to
// json.Marshal otherwise. This is the one place serialization happens — a
// domain service never hand-rolls json.Marshal for an event.
func EncodeEvent(event Event) ([]byte, error) {
	if enc, ok := event.(EventEncoder); ok {
		return enc.EncodeEvent()
	}
	return json.Marshal(event)
}

// Record is the durable/wire envelope: the outbox row's shape and the only
// event form that crosses a datastore or process boundary. EventID (sdk/id) is
// the at-least-once de-duplication key — it is the outbox primary key and the
// SSE `id:` field, so consumers de-dupe on it.
type Record struct {
	EventID       string
	Type          string
	OccurredAt    time.Time
	CorrelationID string
	Payload       []byte // EncodeEvent output
	AggregateType *string
	AggregateID   *string
	TenantID      *string
}

// NewRecord builds a Record from a typed Event: it assigns a fresh EventID from
// sdk/id, copies the envelope fields, extracts Metadata when the event carries
// it, and encodes the payload via EncodeEvent. Serialization is owned here, not
// by callers.
func NewRecord(e Event) (Record, error) {
	payload, err := EncodeEvent(e)
	if err != nil {
		return Record{}, err
	}
	rec := Record{
		EventID:       ids.MustGenerate(),
		Type:          e.Type(),
		OccurredAt:    e.OccurredAt(),
		CorrelationID: e.CorrelationID(),
		Payload:       payload,
	}
	if m, ok := e.(Metadata); ok {
		rec.AggregateType = m.AggregateType()
		rec.AggregateID = m.AggregateID()
		rec.TenantID = m.TenantID()
	}
	return rec, nil
}

// RemoteEvent is an event reconstructed from a transport envelope on another
// process (or replayed from the outbox). The broadcast/durable path cannot
// recover the original typed struct, so RemoteEvent carries the envelope fields
// plus the encoded payload. It satisfies Event and Metadata directly and
// Unmarshaler for TypedHandler's slow path (decoding the payload into the
// handler's concrete type), so one handler serves both in-process typed events
// and rehydrated ones.
type RemoteEvent struct {
	EventType   string
	Occurred    time.Time
	Correlation string
	Payload     []byte // the original EncodeEvent bytes

	Tenant  *string
	AggType *string
	AggID   *string
}

var (
	_ Event        = RemoteEvent{}
	_ Metadata     = RemoteEvent{}
	_ Unmarshaler  = RemoteEvent{}
	_ EventEncoder = RemoteEvent{}
)

// Type implements Event.
func (e RemoteEvent) Type() string { return e.EventType }

// OccurredAt implements Event.
func (e RemoteEvent) OccurredAt() time.Time { return e.Occurred }

// CorrelationID implements Event.
func (e RemoteEvent) CorrelationID() string { return e.Correlation }

// AggregateType implements Metadata.
func (e RemoteEvent) AggregateType() *string { return e.AggType }

// AggregateID implements Metadata.
func (e RemoteEvent) AggregateID() *string { return e.AggID }

// TenantID implements Metadata.
func (e RemoteEvent) TenantID() *string { return e.Tenant }

// EncodeEvent returns the original payload bytes unchanged, so re-encoding a
// rehydrated event preserves its wire form rather than JSON-marshaling the
// RemoteEvent wrapper.
func (e RemoteEvent) EncodeEvent() ([]byte, error) { return e.Payload, nil }

// Unmarshal decodes the payload into target — TypedHandler's slow path uses it
// to rebuild the handler's concrete event type from the wire bytes.
func (e RemoteEvent) Unmarshal(target any) error {
	return json.Unmarshal(e.Payload, target)
}

// DecodeRemoteMetadata best-effort extracts tenant/aggregate metadata from an
// encoded event payload (BaseEvent's json tags). Absent or unparseable fields
// stay nil. A bus adapter uses it to populate a RemoteEvent's Metadata from the
// payload it received.
func DecodeRemoteMetadata(payload []byte) (tenant, aggType, aggID *string) {
	var probe struct {
		Tenant  *string `json:"tenant_id"`
		AggType *string `json:"aggregate_type"`
		AggID   *string `json:"aggregate_id"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return nil, nil, nil
	}
	return probe.Tenant, probe.AggType, probe.AggID
}

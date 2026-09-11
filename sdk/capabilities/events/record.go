package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// EventEncoder supplies opaque transport bytes. Without it EncodeEvent uses
// JSON. Custom binary consumers decode these bytes themselves; RemoteEvent's
// Unmarshal convenience supports JSON only.
type EventEncoder interface{ EncodeEvent() ([]byte, error) }

func EncodeEvent(event Event) ([]byte, error) {
	if err := ValidateEvent(event); err != nil {
		return nil, err
	}
	if encoder, ok := event.(EventEncoder); ok {
		return encoder.EncodeEvent()
	}
	return json.Marshal(event)
}

// Record is the complete durable/transport envelope. Metadata is independent of
// the payload's format. JSON encodes opaque Payload bytes as base64. EventID is
// preserved on replay; CorrelationID can be shared by many distinct events.
type Record struct {
	EventID       string    `json:"event_id"`
	Type          string    `json:"type"`
	OccurredAt    time.Time `json:"occurred_at"`
	CorrelationID string    `json:"correlation_id"`
	Payload       []byte    `json:"payload"`
	AggregateType *string   `json:"aggregate_type,omitempty"`
	AggregateID   *string   `json:"aggregate_id,omitempty"`
	TenantID      *string   `json:"tenant_id,omitempty"`
}

// NewRecord snapshots an event. It preserves a nonempty Identified ID and
// otherwise generates one. Encoder-owned bytes and metadata pointers are copied.
func NewRecord(event Event) (Record, error) {
	payload, err := EncodeEvent(event)
	if err != nil {
		return Record{}, err
	}
	record := Record{
		Type:          event.Type(),
		OccurredAt:    event.OccurredAt(),
		CorrelationID: event.CorrelationID(),
		Payload:       bytes.Clone(payload),
	}
	if identified, ok := event.(Identified); ok {
		record.EventID = identified.EventID()
	}
	if record.EventID == "" {
		record.EventID = ids.MustGenerate()
	}
	if metadata, ok := event.(Metadata); ok {
		record.AggregateType = copyString(metadata.AggregateType())
		record.AggregateID = copyString(metadata.AggregateID())
		record.TenantID = copyString(metadata.TenantID())
	}
	return record, nil
}

// Validate checks the envelope's required identity and type. Occurrence time and
// correlation are caller vocabulary, not a framework clock or tracing policy.
func (r Record) Validate() error {
	if r.EventID == "" {
		return fmt.Errorf("events: record needs an event ID: %w", sdk.ErrInvalidInput)
	}
	return ValidateEvent(RemoteEvent{Record: r})
}

// Event returns an independent payload/metadata snapshot ready for dispatch.
func (r Record) Event() RemoteEvent {
	r.Payload = bytes.Clone(r.Payload)
	r.AggregateType = copyString(r.AggregateType)
	r.AggregateID = copyString(r.AggregateID)
	r.TenantID = copyString(r.TenantID)
	return RemoteEvent{Record: r}
}

// RemoteEvent exposes a Record through the same methods as a typed local event.
// The embedded Record retains identity and routing without inspecting payloads.
type RemoteEvent struct{ Record }

var (
	_ Event        = RemoteEvent{}
	_ Metadata     = RemoteEvent{}
	_ Identified   = RemoteEvent{}
	_ Unmarshaler  = RemoteEvent{}
	_ EventEncoder = RemoteEvent{}
)

func (e RemoteEvent) Type() string                 { return e.Record.Type }
func (e RemoteEvent) OccurredAt() time.Time        { return e.Record.OccurredAt }
func (e RemoteEvent) CorrelationID() string        { return e.Record.CorrelationID }
func (e RemoteEvent) EventID() string              { return e.Record.EventID }
func (e RemoteEvent) AggregateType() *string       { return e.Record.AggregateType }
func (e RemoteEvent) AggregateID() *string         { return e.Record.AggregateID }
func (e RemoteEvent) TenantID() *string            { return e.Record.TenantID }
func (e RemoteEvent) EncodeEvent() ([]byte, error) { return e.Payload, nil }
func (e RemoteEvent) Unmarshal(target any) error   { return json.Unmarshal(e.Payload, target) }

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

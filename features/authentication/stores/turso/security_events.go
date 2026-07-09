package turso

import (
	"context"
	"encoding/json"

	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// SecurityEventStore implements securityevent.SecurityEventRepository over a libSQL
// database. The table is append-only (no Update/Delete). Details round-trips
// uniformly: a nil or empty map stores '{}' and reads back as a NON-NIL empty map.
// List composes only the set filter fields into a PARAMETERIZED WHERE (never
// string concatenation) and pages in the pinned created_at DESC, id DESC order.
type SecurityEventStore struct {
	db *tursodb.DB
}

var _ securityevent.SecurityEventRepository = (*SecurityEventStore)(nil)

// NewSecurityEventStore returns a SecurityEventStore backed by db.
func NewSecurityEventStore(db *tursodb.DB) *SecurityEventStore {
	return &SecurityEventStore{db: db}
}

const securityEventColumns = "id, user_id, actor_type, actor_id, event_type, event_status, details, ip_address, user_agent, created_at"

// Create appends an audit row and returns the stored record.
func (s *SecurityEventStore) Create(ctx context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	details, err := marshalDetails(evt.Details)
	if err != nil {
		return securityevent.SecurityEvent{}, err
	}
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if evt.ID == "" {
		const q = `INSERT INTO security_events (user_id, actor_type, actor_id, event_type, event_status, details, ip_address, user_agent, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q,
			evt.UserID, evt.Actor.Type, evt.Actor.ID, evt.EventType, evt.EventStatus,
			details, evt.IPAddress, evt.UserAgent, tursodb.FormatTime(evt.CreatedAt),
		).Scan(&evt.ID); err != nil {
			return securityevent.SecurityEvent{}, tursodb.MapError(err)
		}
	} else {
		const q = `INSERT INTO security_events (` + securityEventColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		if _, err := s.db.Exec(ctx, q,
			evt.ID, evt.UserID, evt.Actor.Type, evt.Actor.ID, evt.EventType, evt.EventStatus,
			details, evt.IPAddress, evt.UserAgent, tursodb.FormatTime(evt.CreatedAt),
		); err != nil {
			return securityevent.SecurityEvent{}, err
		}
	}
	// Return the stored shape: Details normalized to a non-nil map, matching the
	// read-back contract.
	stored, err := unmarshalDetails(details)
	if err != nil {
		return securityevent.SecurityEvent{}, err
	}
	evt.Details = stored
	return evt, nil
}

// List returns a cursor-paginated page of events matching filter, ordered
// created_at DESC, id DESC. The dynamic WHERE is parameterized.
func (s *SecurityEventStore) List(ctx context.Context, filter securityevent.ListFilter, req crud.ListRequest) (crud.Page[securityevent.SecurityEvent], error) {
	where := "WHERE 1 = 1"
	var args []any
	if filter.UserID != "" {
		where += " AND user_id = ?"
		args = append(args, filter.UserID)
	}
	if filter.EventType != "" {
		where += " AND event_type = ?"
		args = append(args, filter.EventType)
	}
	if filter.EventStatus != "" {
		where += " AND event_status = ?"
		args = append(args, filter.EventStatus)
	}
	if !filter.Since.IsZero() {
		where += " AND created_at >= ?"
		args = append(args, tursodb.FormatTime(filter.Since))
	}
	if !filter.Until.IsZero() {
		where += " AND created_at < ?"
		args = append(args, tursodb.FormatTime(filter.Until))
	}
	q := tursodb.ListQuery[securityevent.SecurityEvent]{
		BaseSQL:      `SELECT ` + securityEventColumns + ` FROM security_events ` + where,
		Args:         args,
		OrderFields:  securityevent.OrderFields,
		DefaultOrder: securityevent.DefaultOrder,
		PK:           "id",
		Scan:         scanSecurityEvent,
		OrderValueOf: func(evt securityevent.SecurityEvent, _ string) any { return evt.CreatedAt },
		PKOf:         func(evt securityevent.SecurityEvent) string { return evt.ID },
	}
	return tursodb.List(ctx, s.db, q, req)
}

// scanSecurityEvent scans one security_events row, mapping sql.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanSecurityEvent(sc scanner) (securityevent.SecurityEvent, error) {
	var (
		evt       securityevent.SecurityEvent
		details   string
		createdAt string
	)
	if err := sc.Scan(
		&evt.ID, &evt.UserID, &evt.Actor.Type, &evt.Actor.ID, &evt.EventType, &evt.EventStatus,
		&details, &evt.IPAddress, &evt.UserAgent, &createdAt,
	); err != nil {
		return securityevent.SecurityEvent{}, tursodb.MapError(err)
	}
	var err error
	if evt.Details, err = unmarshalDetails(details); err != nil {
		return securityevent.SecurityEvent{}, err
	}
	if evt.CreatedAt, err = tursodb.ParseTime(createdAt); err != nil {
		return securityevent.SecurityEvent{}, err
	}
	return evt, nil
}

// marshalDetails renders an open details bag as TEXT JSON. A nil or empty map
// stores '{}' so it reads back as a non-nil empty map (the uniform round-trip
// contract).
func marshalDetails(d map[string]any) (string, error) {
	if len(d) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unmarshalDetails parses stored TEXT JSON into a NON-NIL map: '{}' , NULL, and an
// empty column all yield a non-nil empty map.
func unmarshalDetails(s string) (map[string]any, error) {
	m := map[string]any{}
	if s == "" || s == "null" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

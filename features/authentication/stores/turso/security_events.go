package turso

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
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

// detailsJSON is a sql.Scanner that reads the stored TEXT JSON details bag into a
// non-nil map through unmarshalDetails (the read twin of marshalDetails), so a
// db-tagged row-struct field performs the decode inside rows.Scan — surfacing a
// malformed-JSON error rather than swallowing it. A NULL or empty column reads
// back as a non-nil empty map (the uniform round-trip contract).
type detailsJSON map[string]any

func (d *detailsJSON) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case nil:
		s = ""
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("authentication turso: cannot scan %T into details JSON", src)
	}
	m, err := unmarshalDetails(s)
	if err != nil {
		return err
	}
	*d = m
	return nil
}

// securityEventRow is the store-local, db-tagged projection of a security_events
// row ScanStruct scans into; Details decodes via the detailsJSON Scanner and
// toDomain maps the flat actor columns back to the Principal.
type securityEventRow struct {
	ID          string       `db:"id"`
	UserID      string       `db:"user_id"`
	ActorType   string       `db:"actor_type"`
	ActorID     string       `db:"actor_id"`
	EventType   string       `db:"event_type"`
	EventStatus string       `db:"event_status"`
	Details     detailsJSON  `db:"details"`
	IPAddress   string       `db:"ip_address"`
	UserAgent   string       `db:"user_agent"`
	CreatedAt   tursodb.Time `db:"created_at"`
}

func (r securityEventRow) toDomain() securityevent.SecurityEvent {
	return securityevent.SecurityEvent{
		ID:          r.ID,
		UserID:      r.UserID,
		Actor:       securityevent.Principal{Type: r.ActorType, ID: r.ActorID},
		EventType:   r.EventType,
		EventStatus: r.EventStatus,
		Details:     map[string]any(r.Details),
		IPAddress:   r.IPAddress,
		UserAgent:   r.UserAgent,
		CreatedAt:   r.CreatedAt.Time,
	}
}

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
	q := tursodb.ListQuery[securityEventRow]{
		BaseSQL:      `SELECT ` + securityEventColumns + ` FROM security_events ` + where,
		Args:         args,
		OrderFields:  securityevent.OrderFields,
		DefaultOrder: securityevent.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r securityEventRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r securityEventRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[securityevent.SecurityEvent]{}, err
	}
	return crud.MapPage(page, securityEventRow.toDomain), nil
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

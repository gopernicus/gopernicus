package pgx

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/securityevent"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// SecurityEventStore implements securityevent.SecurityEventRepository over a
// PostgreSQL database. The table is append-only (no Update/Delete). Details
// round-trips uniformly: a nil or empty map stores '{}' JSONB and reads back as a
// NON-NIL empty map. List composes only the set filter fields into a
// PARAMETERIZED WHERE (never string concatenation) and pages in the pinned
// created_at DESC, id DESC order.
type SecurityEventStore struct {
	db *pgxdb.DB
}

var _ securityevent.SecurityEventRepository = (*SecurityEventStore)(nil)

// NewSecurityEventStore returns a SecurityEventStore backed by db.
func NewSecurityEventStore(db *pgxdb.DB) *SecurityEventStore {
	return &SecurityEventStore{db: db}
}

const securityEventColumns = "id, user_id, actor_type, actor_id, event_type, event_status, details, ip_address, user_agent, created_at"

// Create appends an audit row and returns the stored record.
func (s *SecurityEventStore) Create(ctx context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	details, err := marshalDetails(evt.Details)
	if err != nil {
		return securityevent.SecurityEvent{}, err
	}
	const q = `INSERT INTO security_events (` + securityEventColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	if _, err := s.db.Exec(ctx, q,
		evt.ID, evt.UserID, evt.Actor.Type, evt.Actor.ID, evt.EventType, evt.EventStatus,
		details, evt.IPAddress, evt.UserAgent, evt.CreatedAt.UTC(),
	); err != nil {
		return securityevent.SecurityEvent{}, err
	}
	// Return the stored shape: Details normalized to a non-nil map, matching the
	// read-back contract.
	stored, err := unmarshalDetails([]byte(details))
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
		args = append(args, filter.UserID)
		where += fmt.Sprintf(" AND user_id = $%d", len(args))
	}
	if filter.EventType != "" {
		args = append(args, filter.EventType)
		where += fmt.Sprintf(" AND event_type = $%d", len(args))
	}
	if filter.EventStatus != "" {
		args = append(args, filter.EventStatus)
		where += fmt.Sprintf(" AND event_status = $%d", len(args))
	}
	if !filter.Since.IsZero() {
		args = append(args, filter.Since.UTC())
		where += fmt.Sprintf(" AND created_at >= $%d", len(args))
	}
	if !filter.Until.IsZero() {
		args = append(args, filter.Until.UTC())
		where += fmt.Sprintf(" AND created_at < $%d", len(args))
	}
	return listPage(ctx, s.db, securityEventColumns, "security_events", where, args, "id", req,
		scanSecurityEvent,
		func(evt securityevent.SecurityEvent) (time.Time, string) { return evt.CreatedAt, evt.ID },
	)
}

// scanSecurityEvent scans one security_events row, mapping pgx.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanSecurityEvent(sc scanner) (securityevent.SecurityEvent, error) {
	var (
		evt       securityevent.SecurityEvent
		details   []byte
		createdAt time.Time
	)
	if err := sc.Scan(
		&evt.ID, &evt.UserID, &evt.Actor.Type, &evt.Actor.ID, &evt.EventType, &evt.EventStatus,
		&details, &evt.IPAddress, &evt.UserAgent, &createdAt,
	); err != nil {
		return securityevent.SecurityEvent{}, pgxdb.MapError(err)
	}
	var err error
	if evt.Details, err = unmarshalDetails(details); err != nil {
		return securityevent.SecurityEvent{}, err
	}
	evt.CreatedAt = createdAt.UTC()
	return evt, nil
}

// marshalDetails renders an open details bag as JSON text for the JSONB column. A
// nil or empty map stores '{}' so it reads back as a non-nil empty map (the
// uniform round-trip contract).
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

// unmarshalDetails parses stored JSONB into a NON-NIL map: '{}', NULL, and an empty
// value all yield a non-nil empty map.
func unmarshalDetails(b []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(b) == 0 || string(b) == "null" {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

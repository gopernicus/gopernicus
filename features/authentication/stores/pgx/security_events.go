package pgx

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
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

// securityEventRow is the store-local, db-tagged projection of a security_events
// row. Details scans directly from JSONB into a map; toDomain normalizes a NULL
// (nil map) to a non-nil empty map, honoring the uniform read-back contract.
type securityEventRow struct {
	ID          string         `db:"id"`
	UserID      string         `db:"user_id"`
	ActorType   string         `db:"actor_type"`
	ActorID     string         `db:"actor_id"`
	EventType   string         `db:"event_type"`
	EventStatus string         `db:"event_status"`
	Details     map[string]any `db:"details"`
	IPAddress   string         `db:"ip_address"`
	UserAgent   string         `db:"user_agent"`
	CreatedAt   time.Time      `db:"created_at"`
}

func (r securityEventRow) toDomain() securityevent.SecurityEvent {
	details := r.Details
	if details == nil {
		details = map[string]any{}
	}
	return securityevent.SecurityEvent{
		ID:          r.ID,
		UserID:      r.UserID,
		Actor:       securityevent.Principal{Type: r.ActorType, ID: r.ActorID},
		EventType:   r.EventType,
		EventStatus: r.EventStatus,
		Details:     details,
		IPAddress:   r.IPAddress,
		UserAgent:   r.UserAgent,
		CreatedAt:   r.CreatedAt.UTC(),
	}
}

// Create appends an audit row and returns the stored record.
func (s *SecurityEventStore) Create(ctx context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	details, err := marshalDetails(evt.Details)
	if err != nil {
		return securityevent.SecurityEvent{}, err
	}
	const q = `INSERT INTO security_events (` + securityEventColumns + `)
		VALUES (@id, @user_id, @actor_type, @actor_id, @event_type, @event_status, @details, @ip_address, @user_agent, @created_at)`
	if _, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"id":           evt.ID,
		"user_id":      evt.UserID,
		"actor_type":   evt.Actor.Type,
		"actor_id":     evt.Actor.ID,
		"event_type":   evt.EventType,
		"event_status": evt.EventStatus,
		"details":      details,
		"ip_address":   evt.IPAddress,
		"user_agent":   evt.UserAgent,
		"created_at":   evt.CreatedAt.UTC(),
	}); err != nil {
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
// created_at DESC, id DESC. The dynamic WHERE is parameterized into NamedArgs.
func (s *SecurityEventStore) List(ctx context.Context, filter securityevent.ListFilter, req crud.ListRequest) (crud.Page[securityevent.SecurityEvent], error) {
	where, args := securityEventFilter(filter)
	q := pgxdb.ListQuery[securityEventRow]{
		BaseSQL:      `SELECT ` + securityEventColumns + ` FROM security_events` + where,
		Args:         args,
		OrderFields:  securityevent.OrderFields,
		DefaultOrder: securityevent.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r securityEventRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r securityEventRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[securityevent.SecurityEvent]{}, err
	}
	return crud.MapPage(page, securityEventRow.toDomain), nil
}

// securityEventFilter composes the set filter dimensions into a parameterized
// WHERE fragment and its NamedArgs — never string concatenation of values. The
// leading "WHERE 1 = 1" lets the keyset builder append its predicate with AND.
func securityEventFilter(filter securityevent.ListFilter) (string, pgx.NamedArgs) {
	where := " WHERE 1 = 1"
	args := pgx.NamedArgs{}
	if filter.UserID != "" {
		where += " AND user_id = @user_id"
		args["user_id"] = filter.UserID
	}
	if filter.EventType != "" {
		where += " AND event_type = @event_type"
		args["event_type"] = filter.EventType
	}
	if filter.EventStatus != "" {
		where += " AND event_status = @event_status"
		args["event_status"] = filter.EventStatus
	}
	if !filter.Since.IsZero() {
		where += " AND created_at >= @since"
		args["since"] = filter.Since.UTC()
	}
	if !filter.Until.IsZero() {
		where += " AND created_at < @until"
		args["until"] = filter.Until.UTC()
	}
	return where, args
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

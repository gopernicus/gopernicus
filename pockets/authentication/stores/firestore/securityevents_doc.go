package firestore

import (
	"context"
	"fmt"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// The security_events collection's owner file. It has NO claim — migration 0008
// declares one PRIMARY KEY and four non-unique secondary indexes — and it is
// APPEND-ONLY BY CONSTRUCTION: the port declares no Update and no Delete, so
// this file offers no writer but putSecurityEvent. An audit trail a store can
// rewrite is not an audit trail.
//
// What it has instead is the widest READ surface in the store. ListFilter
// carries three optional equalities and a half-open time window, and every
// combination is a legal query, so the composite index matrix here is every
// equality SUBSET × the created_at range × both directions (SCHEMA.md §7.3).
// The range field and the leading order field are the same column, which is what
// keeps the matrix to one composite per subset per direction rather than two.

// The security_events field paths the queries filter and order on.
const (
	fieldSecurityEventUserID      = "user_id"
	fieldSecurityEventEventType   = "event_type"
	fieldSecurityEventEventStatus = "event_status"
	fieldSecurityEventCreatedAt   = "created_at"
	fieldSecurityEventID          = "id"
)

// securityEventRef is the row's document — the KeyHash of its primary key.
func securityEventRef(db *firestoredb.DB, id string) *gcfs.DocumentRef {
	return db.Doc(collectionSecurityEvents, securityEventDocID(id))
}

// securityEventsQuery composes ONLY the filter's set fields, which is the
// document-API reading of the SQL adapters' rule that the dynamic WHERE is
// parameterized and carries no clause for an unset dimension: an absent field is
// no constraint, not an equality against "".
//
// The two bounds are the port's, exactly: Since is INCLUSIVE (>=) and Until is
// EXCLUSIVE (<), both on created_at. They are truncated to microseconds the same
// way stored timestamps are, so a boundary value compares equal to the row it
// bounds rather than falling on the wrong side of a sub-microsecond remainder —
// the same normalization turso's FormatTime applies to the bound before it
// reaches SQLite.
//
// It carries no order, limit, or cursor: the connector's List owns all three.
func securityEventsQuery(db *firestoredb.DB, filter securityevent.ListFilter) gcfs.Query {
	q := db.Collection(collectionSecurityEvents).Query
	if filter.UserID != "" {
		q = q.Where(fieldSecurityEventUserID, "==", filter.UserID)
	}
	if filter.EventType != "" {
		q = q.Where(fieldSecurityEventEventType, "==", filter.EventType)
	}
	if filter.EventStatus != "" {
		q = q.Where(fieldSecurityEventEventStatus, "==", filter.EventStatus)
	}
	if !filter.Since.IsZero() {
		q = q.Where(fieldSecurityEventCreatedAt, ">=", firestoredb.TruncateTime(filter.Since))
	}
	if !filter.Until.IsZero() {
		q = q.Where(fieldSecurityEventCreatedAt, "<", firestoredb.TruncateTime(filter.Until))
	}
	return q
}

// newSecurityEventDoc builds the document for an event being APPENDED, minting
// the id when the caller left it empty (the greenfield cryptids.Database
// convention).
func newSecurityEventDoc(evt securityevent.SecurityEvent) (securityEventDoc, error) {
	id := evt.ID
	if id == "" {
		id = firestoredb.NewID()
	}
	details, err := encodeDetails(evt.Details)
	if err != nil {
		return securityEventDoc{}, err
	}
	return securityEventDoc{
		ID:          id,
		UserID:      evt.UserID,
		ActorType:   evt.Actor.Type,
		ActorID:     evt.Actor.ID,
		EventType:   evt.EventType,
		EventStatus: evt.EventStatus,
		Details:     details,
		IPAddress:   evt.IPAddress,
		UserAgent:   evt.UserAgent,
		CreatedAt:   firestoredb.TruncateTime(evt.CreatedAt),
	}, nil
}

// putSecurityEvent appends one event. Create, never Set: the document id IS the
// primary key, so a duplicate id loses at the server instead of overwriting an
// audit record — the one way an append-only rail could still lose history.
func putSecurityEvent(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row securityEventDoc) error {
	return w.Create(ctx, securityEventRef(db, row.ID), row)
}

// decodeSecurityEvent turns one snapshot into an event document.
func decodeSecurityEvent(snap *gcfs.DocumentSnapshot) (securityEventDoc, error) {
	var row securityEventDoc
	if err := snap.DataTo(&row); err != nil {
		return securityEventDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionSecurityEvents, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// decodeSecurityEventRow is the listing's Decode: one snapshot straight to the
// domain entity.
func decodeSecurityEventRow(snap *gcfs.DocumentSnapshot) (securityevent.SecurityEvent, error) {
	row, err := decodeSecurityEvent(snap)
	if err != nil {
		return securityevent.SecurityEvent{}, err
	}
	return row.toDomain()
}

// listSecurityEvents is the ListQuery the rail pages with, under the filter's
// query. Order allow-list and default come straight from the pocket, and PK is
// the row's own `id` field — the contractual tiebreak that keeps a page stable
// when several events share a created_at.
//
// No PostFilter is declared, so a non-blank Search is refused with
// sdk.ErrInvalidInput by the connector's List: securityevent declares no
// SearchFields, and answering a search with an unfiltered page is the false
// green ruling R4 exists to prevent.
func listSecurityEvents(db *firestoredb.DB, filter securityevent.ListFilter) firestoredb.ListQuery[securityevent.SecurityEvent] {
	return firestoredb.ListQuery[securityevent.SecurityEvent]{
		Query:        securityEventsQuery(db, filter),
		OrderFields:  securityevent.OrderFields,
		DefaultOrder: securityevent.DefaultOrder,
		PK:           fieldSecurityEventID,
		Decode:       decodeSecurityEventRow,
		OrderValueOf: func(row securityevent.SecurityEvent, _ string) any { return row.CreatedAt },
		PKOf:         func(row securityevent.SecurityEvent) string { return row.ID },
	}
}

// toDomain projects the document onto the domain aggregate, re-assembling the
// flat actor columns into the Principal and guaranteeing a NON-NIL Details map
// on every path — including a document written before this field existed, whose
// absent value decodes to the empty string and therefore to an empty bag.
func (d securityEventDoc) toDomain() (securityevent.SecurityEvent, error) {
	details, err := decodeDetails(d.Details)
	if err != nil {
		return securityevent.SecurityEvent{}, err
	}
	return securityevent.SecurityEvent{
		ID:          d.ID,
		UserID:      d.UserID,
		Actor:       securityevent.Principal{Type: d.ActorType, ID: d.ActorID},
		EventType:   d.EventType,
		EventStatus: d.EventStatus,
		Details:     details,
		IPAddress:   d.IPAddress,
		UserAgent:   d.UserAgent,
		CreatedAt:   d.CreatedAt,
	}, nil
}

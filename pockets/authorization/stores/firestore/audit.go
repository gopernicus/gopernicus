package firestore

import (
	"context"
	"embed"
	"fmt"
	"time"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// AuditIndexesFS contains the optional history listing indexes. Export and
// deploy them before enabling recording or reading retained history.
//
//go:embed firestore.audit.indexes.json
var AuditIndexesFS embed.FS

const AuditIndexesFile = "firestore.audit.indexes.json"

func ExportAuditIndexes(dst string) error {
	m, err := firestoredb.ParseIndexManifest(AuditIndexesFS, AuditIndexesFile)
	if err != nil {
		return err
	}
	return firestoredb.ExportIndexes(m, dst)
}

type auditDoc struct {
	ID              string    `firestore:"id"`
	EventID         string    `firestore:"event_id"`
	OccurredAt      time.Time `firestore:"occurred_at"`
	ActorType       string    `firestore:"actor_type"`
	ActorID         string    `firestore:"actor_id"`
	System          string    `firestore:"system"`
	Reason          string    `firestore:"reason"`
	Action          string    `firestore:"action"`
	Kind            string    `firestore:"kind"`
	ResourceType    string    `firestore:"resource_type"`
	ResourceID      string    `firestore:"resource_id"`
	SubjectType     string    `firestore:"subject_type"`
	SubjectID       string    `firestore:"subject_id"`
	Relation        string    `firestore:"relation"`
	SubjectRelation string    `firestore:"subject_relation"`
	Role            string    `firestore:"role"`
	ResourceKey     string    `firestore:"resource_key"`
	SubjectKey      string    `firestore:"subject_key"`
	ActorKey        string    `firestore:"actor_key"`
}

type auditStore struct{ db *firestoredb.DB }

var _ audit.Reader = (*auditStore)(nil)

func (s *auditStore) List(ctx context.Context, filter audit.Filter, req list.Request) (list.Page[audit.Record], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if err := filter.Validate(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if err := ctx.Err(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if req.ResolvedStrategy() == list.StrategyCursor && req.Cursor != "" {
		cursor, err := list.DecodeCursor(req.Cursor, "occurred_at")
		if err != nil {
			return list.Page[audit.Record]{}, fmt.Errorf("audit cursor: %w: %w", sdk.ErrInvalidInput, err)
		}
		if cursor != nil {
			if _, ok := cursor.OrderValue.(time.Time); !ok {
				return list.Page[audit.Record]{}, fmt.Errorf("audit cursor requires a timestamp: %w", sdk.ErrInvalidInput)
			}
		}
	}
	q := s.db.Collection(collectionAudit).Query
	if filter.ResourceType != "" {
		q = q.Where("resource_key", "==", resourceKey(filter.ResourceType, filter.ResourceID))
	}
	if filter.SubjectType != "" {
		q = q.Where("subject_key", "==", roleSubjectKey(filter.SubjectType, filter.SubjectID))
	}
	if filter.ActorType != "" {
		q = q.Where("actor_key", "==", roleSubjectKey(filter.ActorType, filter.ActorID))
	}
	query := firestoredb.ListQuery[auditDoc]{
		Query: q, OrderFields: audit.OrderFields, DefaultOrder: audit.DefaultOrder, PK: "id", Decode: decodeAudit,
		OrderValueOf: func(row auditDoc, _ string) any { return row.OccurredAt },
		PKOf:         func(row auditDoc) string { return row.ID },
	}
	page, err := firestoredb.List(ctx, s.db.ReaderFrom(ctx), query, req)
	if err != nil {
		return list.Page[audit.Record]{}, err
	}
	return list.MapPage(page, auditDoc.record), nil
}

func validateAuditSource(ctx context.Context, enabled bool) error {
	if !enabled {
		return nil
	}
	_, err := audit.SourceFromContext(ctx)
	return err
}

func (m factWrites) auditRecords(ctx context.Context, enabled bool) ([]audit.Record, error) {
	if !enabled {
		return nil, nil
	}
	changes := make([]audit.Change, 0, len(m.drops)+len(m.creates)+2*len(m.replaces)+len(m.roleDrops)+len(m.roleAdds))
	tuple := func(action audit.Action, row relationshipDoc) {
		fact := relationships.CreateRelationship{ResourceType: row.ResourceType, ResourceID: row.ResourceID, Relation: row.Relation,
			SubjectType: row.SubjectType, SubjectID: row.SubjectID, SubjectRelation: row.SubjectRelation}
		changes = append(changes, audit.Change{Action: action, Relationship: &fact})
	}
	for _, row := range m.drops {
		tuple(audit.ActionRemoved, row)
	}
	for _, row := range m.creates {
		tuple(audit.ActionAdded, row)
	}
	for _, replacement := range m.replaces {
		tuple(audit.ActionRemoved, replacement.old)
		next := replacement.old
		next.Relation = replacement.relation
		tuple(audit.ActionAdded, next)
	}
	for _, row := range m.roleDrops {
		fact := row.toAssignment()
		changes = append(changes, audit.Change{Action: audit.ActionRemoved, Role: &fact})
	}
	for _, row := range m.roleAdds {
		fact := row.toAssignment()
		changes = append(changes, audit.Change{Action: audit.ActionAdded, Role: &fact})
	}
	return audit.NewRecords(ctx, changes, time.Now())
}

// Audit writes share the fact transaction. There is no partial batch fallback:
// a failed audit write or oversized commit rolls back the entire operation.
func appendAudit(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, records []audit.Record) error {
	for _, record := range records {
		if err := w.Create(ctx, db.Doc(collectionAudit, record.ID), newAuditDoc(record)); err != nil {
			return err
		}
	}
	return nil
}

func newAuditDoc(record audit.Record) auditDoc {
	d := auditDoc{ID: record.ID, EventID: record.EventID, OccurredAt: record.OccurredAt,
		ActorType: record.Source.ActorType, ActorID: record.Source.ActorID, System: record.Source.System, Reason: record.Source.Reason,
		Action: string(record.Change.Action)}
	if r := record.Change.Relationship; r != nil {
		d.Kind = "relationship"
		d.ResourceType, d.ResourceID, d.Relation = r.ResourceType, r.ResourceID, r.Relation
		d.SubjectType, d.SubjectID, d.SubjectRelation = r.SubjectType, r.SubjectID, r.SubjectRelation
	} else {
		r := record.Change.Role
		d.Kind = "role"
		d.ResourceType, d.ResourceID, d.Role = r.ResourceType, r.ResourceID, r.Role
		d.SubjectType, d.SubjectID = r.SubjectType, r.SubjectID
	}
	d.ResourceKey = resourceKey(d.ResourceType, d.ResourceID)
	d.SubjectKey = roleSubjectKey(d.SubjectType, d.SubjectID)
	d.ActorKey = roleSubjectKey(d.ActorType, d.ActorID)
	return d
}

func decodeAudit(snap *gcfs.DocumentSnapshot) (auditDoc, error) {
	var row auditDoc
	if err := snap.DataTo(&row); err != nil {
		return auditDoc{}, fmt.Errorf("authorization firestore store: decoding audit: %s: %w", err, sdk.ErrInvalidInput)
	}
	record := row.record()
	if err := record.Source.Validate(); err != nil {
		return auditDoc{}, err
	}
	if err := record.Change.Validate(); err != nil {
		return auditDoc{}, err
	}
	return row, nil
}

func (d auditDoc) record() audit.Record {
	r := audit.Record{ID: d.ID, EventID: d.EventID, OccurredAt: d.OccurredAt,
		Source: audit.Source{ActorType: d.ActorType, ActorID: d.ActorID, System: d.System, Reason: d.Reason},
		Change: audit.Change{Action: audit.Action(d.Action)}}
	switch d.Kind {
	case "relationship":
		r.Change.Relationship = &relationships.CreateRelationship{ResourceType: d.ResourceType, ResourceID: d.ResourceID,
			Relation: d.Relation, SubjectType: d.SubjectType, SubjectID: d.SubjectID, SubjectRelation: d.SubjectRelation}
	case "role":
		r.Change.Role = &roles.Assignment{SubjectType: d.SubjectType, SubjectID: d.SubjectID, Role: d.Role, ResourceType: d.ResourceType, ResourceID: d.ResourceID}
	}
	return r
}

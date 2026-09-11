package pgx

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/jackc/pgx/v5"
)

const auditColumns = "id, event_id, occurred_at, actor_type, actor_id, system_source, reason, action, fact_kind, resource_type, resource_id, subject_type, subject_id, relation, subject_relation, role"

type auditRow struct {
	ID              string    `db:"id"`
	EventID         string    `db:"event_id"`
	OccurredAt      time.Time `db:"occurred_at"`
	ActorType       string    `db:"actor_type"`
	ActorID         string    `db:"actor_id"`
	System          string    `db:"system_source"`
	Reason          string    `db:"reason"`
	Action          string    `db:"action"`
	Kind            string    `db:"fact_kind"`
	ResourceType    string    `db:"resource_type"`
	ResourceID      string    `db:"resource_id"`
	SubjectType     string    `db:"subject_type"`
	SubjectID       string    `db:"subject_id"`
	Relation        string    `db:"relation"`
	SubjectRelation string    `db:"subject_relation"`
	Role            string    `db:"role"`
}

func (r auditRow) toDomain() audit.Record {
	record := audit.Record{
		ID: r.ID, EventID: r.EventID, OccurredAt: r.OccurredAt.UTC(),
		Source: audit.Source{ActorType: r.ActorType, ActorID: r.ActorID, System: r.System, Reason: r.Reason},
		Change: audit.Change{Action: audit.Action(r.Action)},
	}
	if r.Kind == "relationship" {
		record.Change.Relationship = &relationships.CreateRelationship{
			ResourceType: r.ResourceType, ResourceID: r.ResourceID, Relation: r.Relation,
			SubjectType: r.SubjectType, SubjectID: r.SubjectID, SubjectRelation: r.SubjectRelation,
		}
	} else {
		record.Change.Role = &roles.Assignment{
			ResourceType: r.ResourceType, ResourceID: r.ResourceID, Role: r.Role,
			SubjectType: r.SubjectType, SubjectID: r.SubjectID,
		}
	}
	return record
}

func appendAudit(ctx context.Context, tx *writeTx, cfg config) error {
	if !cfg.audit {
		return nil
	}
	records, err := audit.NewRecords(ctx, tx.changes, time.Now())
	if err != nil {
		return err
	}
	for _, r := range records {
		row := auditRow{ID: r.ID, EventID: r.EventID, ActorType: r.Source.ActorType, ActorID: r.Source.ActorID, System: r.Source.System, Reason: r.Source.Reason, Action: string(r.Change.Action)}
		if fact := r.Change.Relationship; fact != nil {
			row.Kind = "relationship"
			row.ResourceType = fact.ResourceType
			row.ResourceID = fact.ResourceID
			row.SubjectType = fact.SubjectType
			row.SubjectID = fact.SubjectID
			row.Relation = fact.Relation
			row.SubjectRelation = fact.SubjectRelation
		} else {
			fact := r.Change.Role
			row.Kind = "role"
			row.ResourceType = fact.ResourceType
			row.ResourceID = fact.ResourceID
			row.SubjectType = fact.SubjectType
			row.SubjectID = fact.SubjectID
			row.Role = fact.Role
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+cfg.schema.Table("iam_audit")+` (`+auditColumns+`) VALUES (@id, @event_id, @occurred_at, @actor_type, @actor_id, @system_source, @reason, @action, @fact_kind, @resource_type, @resource_id, @subject_type, @subject_id, @relation, @subject_relation, @role)`, pgx.NamedArgs{"id": row.ID, "event_id": row.EventID, "occurred_at": r.OccurredAt, "actor_type": row.ActorType, "actor_id": row.ActorID, "system_source": row.System, "reason": row.Reason, "action": row.Action, "fact_kind": row.Kind, "resource_type": row.ResourceType, "resource_id": row.ResourceID, "subject_type": row.SubjectType, "subject_id": row.SubjectID, "relation": row.Relation, "subject_relation": row.SubjectRelation, "role": row.Role}); err != nil {
			return err
		}
	}
	return nil
}

type auditStore struct {
	db     *pgxdb.DB
	schema pgxdb.Schema
}

var _ audit.Reader = (*auditStore)(nil)

func (s *auditStore) List(ctx context.Context, filter audit.Filter, req list.Request) (list.Page[audit.Record], error) {
	if err := filter.Validate(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if err := ctx.Err(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if err := req.Validate(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if req.ResolvedStrategy() != list.StrategyOffset {
		cursor, err := list.DecodeCursor(req.Cursor, "occurred_at")
		if err != nil {
			return list.Page[audit.Record]{}, err
		}
		if cursor != nil {
			if _, ok := cursor.OrderValue.(time.Time); !ok {
				return list.Page[audit.Record]{}, fmt.Errorf("audit cursor must contain a timestamp: %w", sdk.ErrInvalidInput)
			}
		}
	}

	where := " WHERE 1=1"
	args := pgx.NamedArgs{}
	for _, pair := range []struct{ name, a, b string }{{"resource", filter.ResourceType, filter.ResourceID}, {"subject", filter.SubjectType, filter.SubjectID}, {"actor", filter.ActorType, filter.ActorID}} {
		if pair.a != "" {
			where += " AND " + pair.name + "_type=@" + pair.name + "_type AND " + pair.name + "_id=@" + pair.name + "_id"
			args[pair.name+"_type"] = pair.a
			args[pair.name+"_id"] = pair.b
		}
	}
	q := pgxdb.ListQuery[auditRow]{
		BaseSQL:      `SELECT ` + auditColumns + ` FROM ` + s.schema.Table("iam_audit") + where,
		Args:         args,
		OrderFields:  audit.OrderFields,
		DefaultOrder: audit.DefaultOrder,
		PK:           "id",
		PKOf:         func(r auditRow) string { return r.ID },
		OrderValueOf: func(r auditRow, field string) any { return r.OccurredAt },
	}
	page, err := pgxdb.List(ctx, s.db.QuerierFrom(ctx), q, req)
	if err != nil {
		return list.Page[audit.Record]{}, err
	}
	return list.MapPage(page, auditRow.toDomain), nil
}

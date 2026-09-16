package pgx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/jackc/pgx/v5"
)

const auditColumns = "id, event_id, occurred_at, actor_type, actor_id, system_source, reason, action, encoding, scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation"

type auditRow struct {
	ID              string           `db:"id"`
	EventID         string           `db:"event_id"`
	OccurredAt      time.Time        `db:"occurred_at"`
	ActorType       string           `db:"actor_type"`
	ActorID         string           `db:"actor_id"`
	System          string           `db:"system_source"`
	Reason          string           `db:"reason"`
	Action          string           `db:"action"`
	Encoding        string           `db:"encoding"`
	ScopeKind       tuples.ScopeKind `db:"scope_kind"`
	ResourceType    string           `db:"resource_type"`
	ResourceID      string           `db:"resource_id"`
	Relation        string           `db:"relation"`
	SubjectType     string           `db:"subject_type"`
	SubjectID       string           `db:"subject_id"`
	SubjectRelation string           `db:"subject_relation"`
}

func (r auditRow) toDomain() audit.Record {
	return audit.Record{ID: r.ID, EventID: r.EventID, OccurredAt: r.OccurredAt.UTC(), Encoding: r.Encoding,
		Source: audit.Source{ActorType: r.ActorType, ActorID: r.ActorID, System: r.System, Reason: r.Reason},
		Change: audit.Change{Action: audit.Action(r.Action), Tuple: tuples.Tuple{Scope: tuples.Scope{Kind: r.ScopeKind, Type: r.ResourceType, ID: r.ResourceID}, Relation: r.Relation, Subject: tuples.SubjectRef{Type: r.SubjectType, ID: r.SubjectID, Relation: r.SubjectRelation}}}}
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
		f := r.Change.Tuple
		a := &tupleArgs{}
		values := []string{}
		for _, value := range []any{r.ID, r.EventID, r.OccurredAt, r.Source.ActorType, r.Source.ActorID, r.Source.System, r.Source.Reason, string(r.Change.Action), r.Encoding, int(f.Scope.Kind), f.Scope.Type, f.Scope.ID, f.Relation, f.Subject.Type, f.Subject.ID, f.Subject.Relation} {
			values = append(values, a.bind(value))
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+cfg.schema.Table("iam_audit")+` (`+auditColumns+`) VALUES (`+strings.Join(values, ",")+`)`, a.params()...); err != nil {
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

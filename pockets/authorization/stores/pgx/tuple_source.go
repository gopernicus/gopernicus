package pgx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

func (s *tupleSource) Snapshot(ctx context.Context, mirroredReceipt string) (tuplecache.Snapshot, error) {
	var snapshot tuplecache.Snapshot
	if !s.CacheableContext(ctx) {
		return snapshot, fmt.Errorf("tuple cache: ambient snapshot: %w", sdk.ErrInvalidInput)
	}
	tx, err := s.db.BeginRead(ctx)
	if err != nil {
		return snapshot, err
	}
	defer tx.Rollback()
	_, snapshot.Receipt, err = s.readReceipt(ctx, tx)
	if err != nil {
		return snapshot, err
	}
	snapshot.Full = mirroredReceipt == "" || mirroredReceipt != snapshot.Receipt
	if !snapshot.Full {
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+s.cfg.schema.Table("iam_tuple_outbox")+" WHERE reset)").Scan(&snapshot.Full); err != nil {
			return snapshot, err
		}
	}
	columns := "id::text, before_tuple, after_tuple, reset"
	if snapshot.Full {
		// Current facts replace all pending changes. Only their exact identities
		// are needed for acknowledgement, even if obsolete payloads are invalid.
		columns = "id::text"
	}
	rows, err := tx.Query(ctx, "SELECT "+columns+" FROM "+s.cfg.schema.Table("iam_tuple_outbox")+" ORDER BY id")
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var change tuplecache.Change
		if snapshot.Full {
			if err := rows.Scan(&change.ID); err != nil {
				rows.Close()
				return snapshot, err
			}
			snapshot.Changes = append(snapshot.Changes, change)
			continue
		}
		var before, after []byte
		var reset bool
		if err := rows.Scan(&change.ID, &before, &after, &reset); err != nil {
			rows.Close()
			return snapshot, err
		}
		change.Before, err = decodeTuple(before)
		if err != nil {
			rows.Close()
			return snapshot, err
		}
		change.After, err = decodeTuple(after)
		if err != nil {
			rows.Close()
			return snapshot, err
		}
		if reset && (change.Before != nil || change.After != nil) || !reset && change.Before == nil && change.After == nil {
			rows.Close()
			return snapshot, tuplecache.ErrUnavailable
		}
		snapshot.Changes = append(snapshot.Changes, change)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snapshot, err
	}
	if snapshot.Full {
		rows, err := tx.Query(ctx, "SELECT resource_type,resource_id,relation,subject_type,subject_id,subject_relation FROM "+s.cfg.schema.Table("iam_relationships")+" ORDER BY resource_type COLLATE \"C\",resource_id COLLATE \"C\",relation COLLATE \"C\",subject_type COLLATE \"C\",subject_id COLLATE \"C\",subject_relation COLLATE \"C\"")
		if err != nil {
			return snapshot, err
		}
		for rows.Next() {
			var tuple relationships.CreateRelationship
			if err := rows.Scan(&tuple.ResourceType, &tuple.ResourceID, &tuple.Relation, &tuple.SubjectType, &tuple.SubjectID, &tuple.SubjectRelation); err != nil {
				rows.Close()
				return snapshot, err
			}
			if err := tuple.Validate(); err != nil {
				rows.Close()
				return snapshot, fmt.Errorf("tuple cache current fact: %w: %v", tuplecache.ErrUnavailable, err)
			}
			snapshot.Tuples = append(snapshot.Tuples, tuple)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return snapshot, err
		}
	}
	return snapshot, tx.Commit()
}

func decodeTuple(data []byte) (*relationships.CreateRelationship, error) {
	if data == nil {
		return nil, nil
	}
	var row struct {
		ResourceType    *string `json:"resource_type"`
		ResourceID      *string `json:"resource_id"`
		Relation        *string `json:"relation"`
		SubjectType     *string `json:"subject_type"`
		SubjectID       *string `json:"subject_id"`
		SubjectRelation *string `json:"subject_relation"`
	}
	if err := json.Unmarshal(data, &row); err != nil {
		return nil, fmt.Errorf("tuple cache payload: %w: %v", tuplecache.ErrUnavailable, err)
	}
	if row.ResourceType == nil || row.ResourceID == nil || row.Relation == nil || row.SubjectType == nil || row.SubjectID == nil || row.SubjectRelation == nil {
		return nil, tuplecache.ErrUnavailable
	}
	tuple := &relationships.CreateRelationship{ResourceType: *row.ResourceType, ResourceID: *row.ResourceID, Relation: *row.Relation, SubjectType: *row.SubjectType, SubjectID: *row.SubjectID, SubjectRelation: *row.SubjectRelation}
	if err := tuple.Validate(); err != nil {
		return nil, fmt.Errorf("tuple cache payload: %w: %v", tuplecache.ErrUnavailable, err)
	}

	return tuple, nil
}

func (s *tupleSource) Acknowledge(ctx context.Context, expectedReceipt, receipt string, ids []string) error {
	if !s.CacheableContext(ctx) || receipt == "" {
		return fmt.Errorf("tuple cache: ambient acknowledgement or empty receipt: %w", sdk.ErrInvalidInput)
	}
	eventIDs := make([]int64, len(ids))
	for i, id := range ids {
		parsed, err := strconv.ParseInt(id, 10, 64)
		if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != id {
			return fmt.Errorf("tuple cache invalid event ID: %w", sdk.ErrInvalidInput)
		}
		eventIDs[i] = parsed
	}
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		result, err := tx.Exec(ctx, "UPDATE "+s.cfg.schema.Table("iam_tuple_cache")+" SET receipt=$1 WHERE slot=1 AND protocol=1 AND identity=$2 AND receipt=$3", receipt, s.identity, expectedReceipt)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return tuplecache.ErrConflict
		}
		if len(ids) == 0 {
			return nil
		}
		// Compare exact event identities. Never delete through a sequence maximum:
		// an earlier allocated ID can commit after this snapshot was captured.
		_, err = tx.Exec(ctx, "DELETE FROM "+s.cfg.schema.Table("iam_tuple_outbox")+" WHERE id = ANY($1::bigint[])", eventIDs)
		return err
	})
}

var _ tuplecache.Source = (*tupleSource)(nil)

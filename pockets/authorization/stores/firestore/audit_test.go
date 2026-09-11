package firestore

import (
	"context"
	"reflect"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
)

func TestAuditPlanContainsOnlyNaturalFactDeltas(t *testing.T) {
	ctx := audit.WithSource(context.Background(), audit.Source{System: "test"})
	old := relationshipDoc{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectType: "group", SubjectID: "g1", SubjectRelation: "member"}
	grant := roleDoc{SubjectType: "user", SubjectID: "u1", Role: "editor"}
	records, err := (factWrites{replaces: []tupleReplacement{{old: old, relation: "editor"}}, roleAdds: []roleDoc{grant}}).auditRecords(ctx, true)
	if err != nil || len(records) != 3 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	for _, record := range records {
		if got := newAuditDoc(record).record(); !reflect.DeepEqual(got, record) {
			t.Fatalf("document round trip changed fact: %+v / %+v", got, record)
		}
		if record.OccurredAt.Nanosecond()%1000 != 0 {
			t.Fatal("timestamp lost microsecond precision")
		}
	}
	if records[0].EventID != records[1].EventID || records[1].EventID != records[2].EventID {
		t.Fatal("one operation produced multiple groups")
	}
	records, err = (factWrites{creates: []relationshipDoc{old}, drops: []relationshipDoc{old}}).auditRecords(ctx, true)
	if err != nil || len(records) != 0 {
		t.Fatalf("opposing deltas survived: %+v %v", records, err)
	}
	if records, err = (factWrites{creates: []relationshipDoc{old}}).auditRecords(context.Background(), false); err != nil || len(records) != 0 {
		t.Fatal("disabled audit required metadata")
	}
}

func TestAuditDocumentRoundTripOwnsBothFactKinds(t *testing.T) {
	tuple := relationships.CreateRelationship{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectType: "group", SubjectID: "g1", SubjectRelation: "member"}
	role := roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "editor"}
	for _, change := range []audit.Change{{Action: audit.ActionAdded, Relationship: &tuple}, {Action: audit.ActionRemoved, Role: &role}} {
		want := audit.Record{ID: "event:00000000000000000001", EventID: "event", OccurredAt: time.Now().UTC().Truncate(time.Microsecond), Source: audit.Source{ActorType: "user", ActorID: "actor", Reason: "reason"}, Change: change}
		doc := newAuditDoc(want)
		got := doc.record()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v want %+v", got, want)
		}
		if got.Change.Role != nil {
			got.Change.Role.Role = "changed"
		} else {
			got.Change.Relationship.SubjectRelation = "changed"
		}
		if !reflect.DeepEqual(doc.record(), want) {
			t.Fatal("reader returned aliased fact")
		}
	}
}

func TestAuditIndexManifestCoversEveryFilterAndDirection(t *testing.T) {
	manifest, err := firestoredb.ParseIndexManifest(AuditIndexesFS, AuditIndexesFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Indexes) != 16 {
		t.Fatalf("got %d audit indexes, want 16", len(manifest.Indexes))
	}
	for mask := 0; mask < 8; mask++ {
		for _, direction := range []string{firestoredb.OrderAscending, firestoredb.OrderDescending} {
			fields := []firestoredb.IndexField{}
			for i, key := range []string{"actor_key", "resource_key", "subject_key"} {
				if mask&(1<<i) != 0 {
					fields = append(fields, firestoredb.IndexField{FieldPath: key, Order: firestoredb.OrderAscending})
				}
			}
			fields = append(fields, firestoredb.IndexField{FieldPath: "occurred_at", Order: direction}, firestoredb.IndexField{FieldPath: "id", Order: direction})
			found := false
			for _, idx := range manifest.Indexes {
				if idx.CollectionGroup == "iam_audit" && idx.QueryScope == firestoredb.ScopeCollection && reflect.DeepEqual(idx.Fields, fields) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing audit filter mask %d direction %s", mask, direction)
			}
		}
	}
	base, err := firestoredb.ParseIndexManifest(IndexesFS, IndexesFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, idx := range base.Indexes {
		if idx.CollectionGroup == "iam_audit" {
			t.Fatal("disabled recording requires optional audit indexes")
		}
	}
}

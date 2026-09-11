//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestTupleStorageUpgradePreservesFactsAndIsRerunnable(t *testing.T) {
	ctx := context.Background()
	db, store := newRelationships(t)
	w := db.WriterFrom(ctx)
	const rows = 103 // cross the maintenance page boundary, including deleted claims
	for i := 0; i < rows; i++ {
		row := newRow(ctfUserset("doc", "d1", "viewer", "group", docID("g", i), "member"))
		if err := putTuple(ctx, db, w, row); err != nil {
			t.Fatal(err)
		}
		tuple, claim := claimRefs(db, row)
		legacyID := docID("old-id", i)
		if err := w.Update(ctx, tuple, []gcfs.Update{
			{Path: "tuple_key_prefix", Value: gcfs.Delete},
			{Path: "tuple_key_suffix", Value: gcfs.Delete},
			{Path: "relationship_id", Value: legacyID},
			{Path: "created_at", Value: time.Unix(1, 0)},
		}); err != nil {
			t.Fatal(err)
		}
		if err := w.Update(ctx, claim, []gcfs.Update{{Path: "relationship_id", Value: legacyID}}); err != nil {
			t.Fatal(err)
		}
		if err := w.Create(ctx, db.Doc("iam_relationship_ids", firestoredb.KeyHash(legacyID)), map[string]any{"relationship_id": legacyID, "tuple_id": tuple.ID}); err != nil {
			t.Fatal(err)
		}
	}
	assignment := ra("u1", "editor", "doc", "d1")
	if err := putRole(ctx, db, w, newRoleDoc(assignment)); err != nil {
		t.Fatal(err)
	}
	roleRef := roleRef(db, assignment.SubjectType, assignment.SubjectID, assignment.Role, assignment.ResourceType, assignment.ResourceID)
	if err := w.Update(ctx, roleRef, []gcfs.Update{{Path: "created_at", Value: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	before := map[string]map[string]any{}
	for run := 0; run < 2; run++ {
		if err := UpgradeTupleStorage(ctx, db); err != nil {
			t.Fatalf("upgrade run %d: %v", run, err)
		}
		for _, collection := range []string{collectionRelationships, collectionSubjectClaims, collectionRoles} {
			forEachDoc(t, db.ReaderFrom(ctx), db.Collection(collection).Query, func(snap *gcfs.DocumentSnapshot) {
				data := snap.Data()
				for _, removed := range []string{"relationship_id", "created_at"} {
					if _, exists := data[removed]; exists {
						t.Fatalf("%s retained %s", snap.Ref.Path, removed)
					}
				}
				if run == 0 {
					before[snap.Ref.Path] = data
				} else if !reflect.DeepEqual(before[snap.Ref.Path], data) {
					t.Fatalf("repeated upgrade changed %s", snap.Ref.Path)
				}
			})
		}
		if n := countDocuments(ctx, db.ReaderFrom(ctx), db.Collection("iam_relationship_ids").Query); n != 0 {
			t.Fatalf("upgrade retained %d obsolete ID claims", n)
		}
		page, err := store.ListRelationshipsByResource(ctx, "doc", "d1", relationships.ResourceRelationshipFilter{}, list.Request{Limit: 100, WithCount: true})
		if err != nil || page.Total == nil || *page.Total != rows || len(page.Items) != 100 || !page.HasMore {
			t.Fatalf("upgrade lost listing facts: rows=%d total=%v error=%v", len(page.Items), page.Total, err)
		}
		last, err := store.ListRelationshipsByResource(ctx, "doc", "d1", relationships.ResourceRelationshipFilter{}, list.Request{Limit: 100, Cursor: page.NextCursor})
		if err != nil || len(last.Items) != rows-100 || last.HasMore {
			t.Fatalf("upgrade lost final page: %+v error=%v", last, err)
		}
		page.Items = append(page.Items, last.Items...)
		for i, row := range page.Items {
			if row.SubjectID != docID("g", i) || row.SubjectRelation != "member" {
				t.Fatalf("upgrade changed tuple ordering/identity: %+v", row)
			}
		}
		if held, err := newRoleStore(db, false).HasExactRole(ctx, "user", "u1", "editor", "doc", "d1"); err != nil || !held {
			t.Fatalf("upgrade lost role: %v %v", held, err)
		}
		assertNoOrphans(t, db)
	}
}

func TestTupleStorageUpgradeRejectsInvalidLegacyKeys(t *testing.T) {
	for name, bad := range map[string]string{"delimiter": "bad\x01key", "overlong": strings.Repeat("x", 257)} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, _ := newRelationships(t)
			row := newRow(ctf("doc", "d1", "viewer", "user", bad))
			ref := db.Doc(collectionRelationships, tupleID(row))
			if err := db.WriterFrom(ctx).Create(ctx, ref, row); err != nil {
				t.Fatal(err)
			}
			before, err := db.ReaderFrom(ctx).Get(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			if err := UpgradeTupleStorage(ctx, db); !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), ref.Path) {
				t.Fatalf("invalid legacy tuple must identify the failed document: %v", err)
			}
			after, err := db.ReaderFrom(ctx).Get(ctx, ref)
			if err != nil || !before.UpdateTime.Equal(after.UpdateTime) {
				t.Fatalf("invalid legacy tuple was rewritten: %v", err)
			}
		})
	}
}

//go:build integration && !live

package firestore

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestTupleListingsPreserveFullLengthNaturalOrder(t *testing.T) {
	ctx := context.Background()
	db, store := newRelationships(t)
	max := strings.Repeat("😀", 64)
	subjectRelations := []string{"", "a", "a!", max, strings.Repeat("😀", 63) + "😁"}
	var tuples []relationships.CreateRelationship
	for _, subject := range []string{"a", "a!", "a/b", max} {
		for _, relation := range subjectRelations {
			tuples = append(tuples, ctfUserset(max, max, max, max, subject, relation))
		}
	}
	if err := store.CreateRelationships(ctx, tuples); err != nil {
		t.Fatal(err)
	}
	for _, row := range storedResourceRows(t, db, max, max) {
		if len(row.TupleKeyPrefix) > 1284 || len(row.TupleKeySuffix) > 257 {
			t.Fatalf("indexed tuple key exceeds its component bound: %+v", row)
		}
	}
	for _, direction := range []string{list.ASC, list.DESC} {
		t.Run(string(direction), func(t *testing.T) {
			want := make([]string, len(tuples))
			for i, tuple := range tuples {
				want[i] = sqlConcat(tuple.ResourceType, tuple.ResourceID, tuple.Relation, tuple.SubjectType, tuple.SubjectID, tuple.SubjectRelation)
			}
			slices.Sort(want)
			if direction == list.DESC {
				slices.Reverse(want)
			}
			order := list.NewOrder("tuple_key", direction)
			var got []string
			var second, third list.Page[relationships.ResourceRelationship]
			cursor := ""
			for i := 0; i <= len(want); i++ {
				page, err := store.ListRelationshipsByResource(ctx, max, max, relationships.ResourceRelationshipFilter{}, list.Request{Limit: 2, Cursor: cursor, Order: order, WithCount: true})
				if err != nil {
					t.Fatal(err)
				}
				if page.Total == nil || *page.Total != int64(len(want)) {
					t.Fatalf("count disagrees with tuple population: %+v", page.Total)
				}
				for _, row := range page.Items {
					got = append(got, sqlConcat(max, max, row.Relation, row.SubjectType, row.SubjectID, row.SubjectRelation))
				}
				if i == 1 {
					second = page
				}
				if i == 2 {
					third = page
				}
				if !page.HasMore {
					break
				}
				cursor = page.NextCursor
			}
			if !slices.Equal(got, want) {
				t.Fatalf("paged tuple order differs: got %d rows, want %d", len(got), len(want))
			}
			back, err := store.ListRelationshipsByResource(ctx, max, max, relationships.ResourceRelationshipFilter{}, list.Request{Limit: 2, Cursor: third.PreviousCursor, Order: order})
			if err != nil || !third.HasPrev || !slices.Equal(back.Items, second.Items) {
				t.Fatalf("reverse cursor lost tuple identity: %+v / %+v error=%v", back.Items, second.Items, err)
			}
			offset, err := store.ListRelationshipsByResource(ctx, max, max, relationships.ResourceRelationshipFilter{}, list.Request{Limit: 2, Offset: 2, Strategy: list.StrategyOffset, Order: order})
			if err != nil || !slices.Equal(offset.Items, second.Items) {
				t.Fatalf("offset order differs: %+v / %+v error=%v", offset.Items, second.Items, err)
			}
			subjects, err := store.ListRelationshipsBySubject(ctx, max, max, relationships.SubjectRelationshipFilter{}, list.Request{Limit: 20, Order: order})
			if err != nil || len(subjects.Items) != len(subjectRelations) {
				t.Fatalf("subject listing lost userset variants: %+v error=%v", subjects.Items, err)
			}
			for i, row := range subjects.Items {
				wantIndex := i
				if direction == list.DESC {
					wantIndex = len(subjectRelations) - 1 - i
				}
				if row.SubjectRelation != subjectRelations[wantIndex] {
					t.Fatalf("subject projection lost exact userset relation: %+v", subjects.Items)
				}
			}
		})
	}
}

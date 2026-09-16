package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	authroles "github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestExactRolesPreviousPageAtLimitOne(t *testing.T) {
	ctx := context.Background()
	facts := NewTuples()
	store, err := authroles.NewService(facts)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := authroles.NewWriter(facts)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"u3", "u1", "u2"} {
		if err := writer.AssignRole(ctx, authroles.Assignment{SubjectType: "user", SubjectID: id, Role: "viewer", Scope: tuples.On("doc", "d1")}); err != nil {
			t.Fatal(err)
		}
	}
	for _, direction := range []string{list.ASC, list.DESC} {
		t.Run(direction, func(t *testing.T) {
			req := list.Request{Limit: 1, Order: list.NewOrder("tuple_key", direction)}
			var previousID string
			for i := 0; i < 3; i++ {
				page, err := store.ListRoleAssignmentsByScope(ctx, tuples.On("doc", "d1"), req)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 1 || page.HasPrev != (i > 0) || page.HasMore != (i < 2) {
					t.Fatalf("page %d: %+v", i, page)
				}
				if page.HasPrev {
					backRequest := req
					backRequest.Cursor = page.PreviousCursor
					back, err := store.ListRoleAssignmentsByScope(ctx, tuples.On("doc", "d1"), backRequest)
					if err != nil {
						t.Fatal(err)
					}
					if len(back.Items) != 1 || back.Items[0].SubjectID != previousID {
						t.Fatalf("previous items=%v, want subject %s", back.Items, previousID)
					}
				}
				previousID = page.Items[0].SubjectID
				req.Cursor = page.NextCursor
			}
		})
	}
}

func TestExactRolesRejectsWrongCursorValueType(t *testing.T) {
	token, err := list.EncodeCursor("tuple_key", int64(1), "user\x00u1\x00viewer")
	if err != nil {
		t.Fatal(err)
	}
	store, err := authroles.NewService(NewTuples())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ListRoleAssignmentsByScope(context.Background(), tuples.On("doc", "d1"), list.Request{Cursor: token})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("error=%v, want ErrInvalidInput", err)
	}
}

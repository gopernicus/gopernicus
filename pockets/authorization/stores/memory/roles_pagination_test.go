package memory

import (
	"context"
	"errors"
	"testing"

	authroles "github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestEffectiveRolesPreviousPageAtLimitOne(t *testing.T) {
	ctx := context.Background()
	store := NewRoles()
	for _, id := range []string{"u3", "u1", "u2"} {
		if err := store.Assign(ctx, authroles.Assignment{SubjectType: "user", SubjectID: id, Role: "viewer", ResourceType: "doc", ResourceID: "d1"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, direction := range []string{list.ASC, list.DESC} {
		t.Run(direction, func(t *testing.T) {
			req := list.Request{Limit: 1, Order: list.NewOrder("grant_key", direction)}
			var previousID string
			for i := 0; i < 3; i++ {
				page, err := store.ListEffectiveByResource(ctx, "doc", "d1", req)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 1 || page.HasPrev != (i > 0) || page.HasMore != (i < 2) {
					t.Fatalf("page %d: %+v", i, page)
				}
				if page.HasPrev {
					backRequest := req
					backRequest.Cursor = page.PreviousCursor
					back, err := store.ListEffectiveByResource(ctx, "doc", "d1", backRequest)
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

func TestEffectiveRolesRejectsWrongCursorValueType(t *testing.T) {
	token, err := list.EncodeCursor("grant_key", int64(1), "user\x00u1\x00viewer")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewRoles().ListEffectiveByResource(context.Background(), "doc", "d1", list.Request{Cursor: token})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("error=%v, want ErrInvalidInput", err)
	}
}

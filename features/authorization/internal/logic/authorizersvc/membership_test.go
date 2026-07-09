package authorizersvc

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
)

func TestRemoveMemberLastOwnerProtection(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples, relationship.CreateRelationship{
		ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1",
	})

	// Sole owner cannot be removed.
	err := svc.RemoveMember(context.Background(), "post", "p1", "user", "u1")
	if !errors.Is(err, ErrCannotRemoveLastOwner) {
		t.Fatalf("want ErrCannotRemoveLastOwner, got %v", err)
	}
	if got, _ := store.CountByResourceAndRelation(context.Background(), "post", "p1", "owner"); got != 1 {
		t.Fatalf("owner must remain after rejected removal, count=%d", got)
	}
}

func TestRemoveMemberSucceedsWithCoOwner(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u2"},
	)
	if err := svc.RemoveMember(context.Background(), "post", "p1", "user", "u1"); err != nil {
		t.Fatalf("removal with a co-owner should succeed: %v", err)
	}
	if got, _ := store.CountByResourceAndRelation(context.Background(), "post", "p1", "owner"); got != 1 {
		t.Fatalf("one owner should remain, count=%d", got)
	}
}

// A non-owner member is removed without a last-owner check.
func TestRemoveMemberNonOwner(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "org", SubjectType: "org", SubjectID: "o1"},
	)
	if err := svc.RemoveMember(context.Background(), "post", "p1", "org", "o1"); err != nil {
		t.Fatalf("removing a non-owner should succeed: %v", err)
	}
}

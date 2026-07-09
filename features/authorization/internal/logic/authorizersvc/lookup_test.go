package authorizersvc

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
)

func TestLookupResourcesDirect(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p2", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	)
	res, err := svc.LookupResources(context.Background(), Subject{Type: "user", ID: "u1"}, "delete", "post")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if res.Unrestricted {
		t.Fatalf("non-admin must not be unrestricted")
	}
	if len(res.IDs) != 2 {
		t.Fatalf("want 2 ids, got %v", res.IDs)
	}
}

func TestLookupResourcesEmptyIsNonNil(t *testing.T) {
	svc := newTestService(t, &fakeStore{}, cryptids.IDGenerator{})
	res, err := svc.LookupResources(context.Background(), Subject{Type: "user", ID: "nobody"}, "delete", "post")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if res.Unrestricted {
		t.Fatalf("no access must not be unrestricted")
	}
	if res.IDs == nil {
		t.Fatalf("IDs must be non-nil when Unrestricted is false")
	}
	if len(res.IDs) != 0 {
		t.Fatalf("want empty ids, got %v", res.IDs)
	}
}

func TestLookupResourcesPlatformAdminUnrestricted(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	store.tuples = append(store.tuples, relationship.CreateRelationship{
		ResourceType: "platform", ResourceID: "main", Relation: "admin", SubjectType: "user", SubjectID: "admin1",
	})
	res, err := svc.LookupResources(context.Background(), Subject{Type: "user", ID: "admin1"}, "delete", "post")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if !res.Unrestricted {
		t.Fatalf("platform admin must be Unrestricted")
	}
	if res.IDs != nil {
		t.Fatalf("Unrestricted result carries no IDs, got %v", res.IDs)
	}
}

func TestLookupResourcesThrough(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store, cryptids.IDGenerator{})
	// u1 admins org o1; posts p1,p2 belong to o1 → both surface via Through.
	store.tuples = append(store.tuples,
		relationship.CreateRelationship{ResourceType: "org", ResourceID: "o1", Relation: "admin", SubjectType: "user", SubjectID: "u1"},
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "org", SubjectType: "org", SubjectID: "o1"},
		relationship.CreateRelationship{ResourceType: "post", ResourceID: "p2", Relation: "org", SubjectType: "org", SubjectID: "o1"},
	)
	res, err := svc.LookupResources(context.Background(), Subject{Type: "user", ID: "u1"}, "view", "post")
	if err != nil {
		t.Fatalf("LookupResources: %v", err)
	}
	if len(res.IDs) != 2 {
		t.Fatalf("want 2 ids via through, got %v", res.IDs)
	}
}

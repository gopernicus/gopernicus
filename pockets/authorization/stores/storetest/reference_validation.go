package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
)

func runRelationshipReferenceValidation(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	ctx := context.Background()
	s := newRepos(t).Relationships
	good := ct("doc", "good", "viewer", "user", "u")
	for i, name := range []string{"resource type", "resource id", "relation", "subject type", "subject id", "subject relation"} {
		t.Run(name, func(t *testing.T) {
			bad := ctUserset("doc", "bad", "viewer", "group", "g", "member")
			fields := []*string{&bad.ResourceType, &bad.ResourceID, &bad.Relation, &bad.SubjectType, &bad.SubjectID, &bad.SubjectRelation}
			*fields[i] = "a\x01b"
			if err := s.CreateRelationships(ctx, []relationships.CreateRelationship{good, bad}); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("CreateRelationships must reject a key separator: %v", err)
			}
			exists, err := s.CheckRelationExists(ctx, good.ResourceType, good.ResourceID, good.Relation, good.SubjectType, good.SubjectID)
			if err != nil || exists {
				t.Fatalf("invalid batch partially published its valid prefix: exists=%v err=%v", exists, err)
			}
		})
	}
}

func runRoleReferenceValidation(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	ctx := context.Background()
	s := newRepos(t).Roles
	for i, name := range []string{"subject type", "subject id", "role", "resource type", "resource id"} {
		t.Run(name, func(t *testing.T) {
			bad := roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "editor", ResourceType: "doc", ResourceID: "d"}
			fields := []*string{&bad.SubjectType, &bad.SubjectID, &bad.Role, &bad.ResourceType, &bad.ResourceID}
			*fields[i] = "a\x01b"
			if err := s.Assign(ctx, bad); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Assign must reject a key separator: %v", err)
			}
		})
	}
	if err := s.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "editor", ResourceType: "doc"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Assign must reject a half-scoped grant: %v", err)
	}
}

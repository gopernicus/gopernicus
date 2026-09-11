package roles

import (
	"context"
	"errors"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestAssignmentValidationRejectsNaturalKeySeparatorsBeforeStore(t *testing.T) {
	ctx := context.Background()
	for i, field := range []string{"subject type", "subject id", "role", "resource type", "resource id"} {
		t.Run(field, func(t *testing.T) {
			args := []string{"user", "u1", "editor", "doc", "d1"}
			args[i] = "a\x01b"
			store := &fakeRoleStore{err: errors.New("store must not be called")}
			svc := newRoleFixture(t, store)
			if err := svc.AssignRole(ctx, args[0], args[1], args[2], args[3], args[4]); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("AssignRole = %v", err)
			}
			if err := svc.UnassignRole(ctx, args[0], args[1], args[2], args[3], args[4]); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("UnassignRole = %v", err)
			}
			if _, err := svc.HasRole(ctx, authmodel.PrincipalRef{Type: args[0], ID: args[1]}, args[2], args[3], args[4]); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("HasRole = %v", err)
			}
		})
	}
}

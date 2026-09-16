package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
)

// RunLookupAmbient requires a snapshot-capable transactor. Lookups borrow its
// pending transaction without committing it or opening an independent snapshot.
func RunLookupAmbient(t *testing.T, factory func(*testing.T) (Repositories, transaction.Transactor)) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			repos, tx := factory(t)
			components, err := authorization.New(repos.Repositories, authorization.WithModel(lookupSnapshotSchema()))
			if err != nil {
				t.Fatal(err)
			}
			principal := authmodel.PrincipalRef{Type: "user", ID: "alice"}
			aborted := errors.New("host aborts")
			err = tx.Transact(t.Context(), func(ctx context.Context) error {
				if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{{ResourceType: "space", ResourceID: "a", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}}); err != nil {
					return err
				}
				all, err := components.Decisions.LookupAllResourceIDs(ctx, principal, "view", "space")
				if err != nil || !slices.Equal(all.IDs, []string{"a"}) {
					return fmt.Errorf("lookup lost pending write: %v/%v", all, err)
				}
				page, err := components.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{Principal: principal, Permission: "view", ResourceType: "space", Limit: 1})
				if err != nil || !slices.Equal(page.IDs, all.IDs) || page.HasMore {
					return fmt.Errorf("page lost pending write: %v/%v", page, err)
				}
				outside, err := components.Decisions.LookupAllResourceIDs(t.Context(), principal, "view", "space")
				if err != nil || len(outside.IDs) != 0 {
					return fmt.Errorf("lookup committed borrowed transaction: %v/%v", outside, err)
				}
				if !commit {
					return aborted
				}
				return nil
			})
			want := error(nil)
			if !commit {
				want = aborted
			}
			if !errors.Is(err, want) {
				t.Fatalf("transaction=%v; want %v", err, want)
			}
			all, err := components.Decisions.LookupAllResourceIDs(t.Context(), principal, "view", "space")
			if err != nil || (len(all.IDs) == 1) != commit {
				t.Fatalf("after transaction: %v/%v", all, err)
			}
		})
	}
}

package storetest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// RunLookupConcurrent exercises complete and paged lookups with relevant
// concurrent membership changes. Each committed state grants both IDs or neither.
func RunLookupConcurrent(t *testing.T, repos Repositories) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	var rows []relationships.CreateRelationship
	for _, group := range []string{"g0", "g1"} {
		for _, id := range []string{"a", "b"} {
			rows = append(rows, relationships.CreateRelationship{ResourceType: "space", ResourceID: id, Relation: "viewer", SubjectType: "group", SubjectID: group, SubjectRelation: "member"})
		}
	}
	if err := repos.Relationships.CreateRelationships(ctx, rows); err != nil {
		t.Fatal(err)
	}
	components, err := authorization.New(repos.Repositories, authorization.WithModel(lookupSnapshotSchema()), authorization.WithLimits(authmodel.EvaluationLimits{MaxBatchSize: 1}))
	if err != nil {
		t.Fatal(err)
	}
	const readers, writers, iterations = 16, 2, 32
	failures := make(chan error, readers+writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	launch := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := fn(); err != nil {
				failures <- err
			}
		}()
	}
	for i := 0; i < writers; i++ {
		group := fmt.Sprintf("g%d", i)
		launch(func() error {
			for j := 0; j < iterations; j++ {
				if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{{ResourceType: "group", ResourceID: group, Relation: "member", SubjectType: "user", SubjectID: "alice"}}); err != nil {
					return fmt.Errorf("writer grant: %w", err)
				}
				if err := repos.Relationships.DeleteRelationshipTarget(ctx, "group", group, "member", relationships.SubjectRef{Type: "user", ID: "alice"}); err != nil {
					return fmt.Errorf("writer revoke: %w", err)
				}
			}
			return nil
		})
	}
	for i := 0; i < readers; i++ {
		launch(func() error {
			principal := authmodel.PrincipalRef{Type: "user", ID: "alice"}
			for j := 0; j < iterations; j++ {
				all, err := components.Decisions.LookupAllResourceIDs(ctx, principal, "view", "space")
				if err != nil {
					return fmt.Errorf("concurrent all: %w", err)
				}
				if all.IDs == nil || (len(all.IDs) != 0 && !slices.Equal(all.IDs, []string{"a", "b"})) {
					return fmt.Errorf("mixed all result: %v", all)
				}
				page, err := components.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{Principal: principal, Permission: "view", ResourceType: "space", Limit: 1})
				if err != nil {
					return fmt.Errorf("concurrent page: %w", err)
				}
				if page.IDs == nil || (len(page.IDs) != 0 && !slices.Equal(page.IDs, []string{"a"})) || page.HasMore != (len(page.IDs) == 1) {
					return fmt.Errorf("mixed page/lookahead: %v", page)
				}
			}
			return nil
		})
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	t.Logf("%d readers, %d writers: %d lookups and %d membership writes", readers, writers, readers*iterations*2, writers*iterations*2)
}

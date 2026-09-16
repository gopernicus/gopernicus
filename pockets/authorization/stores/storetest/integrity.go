package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

func runRawIntegrity(t *testing.T, factory func(*testing.T) Repositories) {
	for _, action := range []string{"tuple", "reconcile", "scope", "relationship", "subject", "batch"} {
		t.Run(action, func(t *testing.T) {
			r := factory(t)
			if r.Relationships == nil && (action == "relationship" || action == "subject") {
				t.Skip("relationship facade not wired")
			}
			ctx := t.Context()
			owner := roleFact("user", "a", "owner", "doc", "d")
			if err := r.Tuples.ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{owner}}); err != nil {
				t.Fatal(err)
			}
			var err error
			switch action {
			case "tuple":
				err = r.Tuples.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{owner}})
			case "reconcile":
				err = r.Tuples.ReconcileTuples(ctx, owner.Scope, "owner", nil)
			case "scope":
				err = r.Tuples.DeleteScope(ctx, owner.Scope)
			case "relationship":
				err = r.Relationships.DeleteResourceRelationships(ctx, "doc", "d")
			case "subject":
				err = r.Relationships.DeleteByResourceAndSubject(ctx, "doc", "d", "user", "a")
			case "batch":
				err = r.Tuples.ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{roleFact("user", "new", "viewer", "doc", "d"), roleFact("user", "new", "viewer", "doc", "orphan")}})
			}
			if !errors.Is(err, mutations.ErrInvariantBlocked) {
				t.Fatalf("ordinary %s bypassed integrity: %v", action, err)
			}
			rows, err := r.Tuples.Lookup(ctx, tuples.Query{})
			if err != nil || len(rows) != 1 || rows[0] != owner {
				t.Fatalf("failed write published partial facts: %+v %v", rows, err)
			}
		})
	}
	t.Run("AddressedAbsentScope", func(t *testing.T) {
		r := factory(t)
		absent := roleFact("user", "a", "viewer", "doc", "empty")
		for _, write := range []func() error{
			func() error { return r.Tuples.ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{absent}}) },
			func() error { return r.Tuples.ReconcileTuples(t.Context(), absent.Scope, "viewer", nil) },
			func() error { return r.Tuples.DeleteScope(t.Context(), absent.Scope) },
		} {
			if err := write(); !errors.Is(err, mutations.ErrInvariantBlocked) {
				t.Fatalf("absent scope bypassed integrity: %v", err)
			}
		}
		t.Run("SubjectFacade", func(t *testing.T) {
			if r.Relationships == nil {
				t.Skip("relationship facade not wired")
			}
			if err := r.Relationships.DeleteByResourceAndSubject(t.Context(), "doc", "empty", "user", "a"); !errors.Is(err, mutations.ErrInvariantBlocked) {
				t.Fatalf("absent subject scope bypassed integrity: %v", err)
			}
		})
	})
	t.Run("AtomicOwnerReplacement", func(t *testing.T) {
		r := factory(t)
		a := roleFact("user", "a", "owner", "doc", "d")
		b := roleFact("user", "b", "owner", "doc", "d")
		if err := r.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{a}}); err != nil {
			t.Fatal(err)
		}
		if err := r.Tuples.ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{a}, Add: []tuples.Tuple{b}}); err != nil {
			t.Fatal(err)
		}
		rows, err := r.Tuples.Lookup(t.Context(), tuples.Query{})
		if err != nil || len(rows) != 1 || rows[0] != b {
			t.Fatalf("owner replacement: %+v %v", rows, err)
		}
	})
	t.Run("ConcurrentRawAndCommandRemoval", func(t *testing.T) {
		r := factory(t)
		if r.Mutations == nil {
			t.Skip("mutation repository not wired")
		}
		for round := range 6 {
			id := fmt.Sprint(round)
			a := roleFact("user", "a", "owner", "doc", id)
			b := roleFact("user", "b", "owner", "doc", id)
			if err := r.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{a, b}}); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			outcomes := make(chan error, 2)
			go func() {
				<-start
				outcomes <- r.Tuples.ApplyTuples(context.Background(), tuples.Changes{Remove: []tuples.Tuple{a}})
			}()
			go func() {
				<-start
				_, err := r.Mutations.Apply(context.Background(), revoke(id, "owner", "b"), nil)
				outcomes <- err
			}()
			close(start)
			successes := 0
			for range 2 {
				if err := <-outcomes; err == nil {
					successes++
				} else if !errors.Is(err, mutations.ErrInvariantBlocked) {
					t.Fatal(err)
				}
			}
			rows, err := r.Tuples.Lookup(t.Context(), tuples.Query{Scope: &a.Scope, Relation: "owner"})
			if err != nil || len(rows) != 1 || successes != 1 {
				t.Fatalf("concurrent removals: %+v successes=%d err=%v", rows, successes, err)
			}
		}
	})
}

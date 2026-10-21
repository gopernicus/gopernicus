// Package authorizationtest is the conformance suite for
// sdk/capabilities/authorization implementations: Run(t, implementation,
// factory) pins the read protocol over four fixtures carried as data (facts
// plus expected answers). It imports stdlib + sdk + the capability only.
//
// Each implementation keeps its own schemas: the factory installs its own
// dialect's schema for Fixture.Name and seeds Fixture.Facts through its native
// API (the protocol has no writer yet). A fixture without a schema in that
// dialect must FAIL the factory, never skip. A documented divergence is
// asserted as a divergence through the Diverge map, keyed by implementation.
// The optional SchemaReader and Limiter families skip loudly when the
// interface is absent; a declared one must pass.
package authorizationtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

// maxDrainPages stops a lookup that never finishes.
const maxDrainPages = 10_000

// Factory builds a FRESH authority for f, installs its own dialect's schema for
// f.Name and seeds f.Facts natively. It calls t.Fatal when it has no schema
// for f.Name.
type Factory func(t *testing.T, f Fixture) authorization.Authorizer

// Run executes every family over every fixture. implementation names the
// implementation under test (Engine, Authzed, OpenFGA, or a new name) and
// selects its Diverge entries.
func Run(t *testing.T, implementation string, factory Factory) {
	t.Helper()
	if implementation == "" || factory == nil {
		t.Fatal("authorizationtest: Run needs an implementation name and a factory")
	}
	for _, f := range Fixtures() {
		t.Run(f.Name, func(t *testing.T) {
			a := factory(t, f)
			if a == nil {
				t.Fatalf("factory returned nil for fixture %q; a missing schema must fail, never skip", f.Name)
			}
			t.Run("Check", func(t *testing.T) { runChecks(t, implementation, f, a) })
			t.Run("CheckBatch", func(t *testing.T) { runCheckBatch(t, implementation, f, a) })
			t.Run("CheckBatchAllOrNothing", func(t *testing.T) { runBatchAllOrNothing(t, implementation, f, a) })
			t.Run("Lookup", func(t *testing.T) { runLookups(t, implementation, f, a) })
			t.Run("Limiter", func(t *testing.T) { runLimiter(t, implementation, f, a) })
			t.Run("SchemaReader", func(t *testing.T) { runSchema(t, implementation, f, a) })
			if f.Name == "documents" {
				t.Run("Cursor", func(t *testing.T) { runCursor(t, a) })
				t.Run("FilterAuthorized", func(t *testing.T) { runFilterAuthorized(t, a) })
				t.Run("LookupAll", func(t *testing.T) { runLookupAll(t, a) })
			}
		})
	}
}

func runChecks(t *testing.T, implementation string, f Fixture, a authorization.Authorizer) {
	for _, c := range f.Checks {
		t.Run(c.Name, func(t *testing.T) {
			want := c.Expect(implementation)
			got, err := a.Check(context.Background(), c.Req)
			if !outcome(t, fmt.Sprintf("Check(%+v)", c.Req), err, want.Err) {
				return
			}
			if got.Allowed != want.Allowed {
				t.Fatalf("Check(%+v) = %v, want %v", c.Req, got.Allowed, want.Allowed)
			}
		})
	}
}

// runCheckBatch sends every non-erroring check in one call, plus a duplicate,
// and requires request-order results of the same length.
func runCheckBatch(t *testing.T, implementation string, f Fixture, a authorization.Authorizer) {
	var reqs []authorization.CheckRequest
	var wants []bool
	for _, c := range f.Checks {
		if w := c.Expect(implementation); w.Err == nil {
			reqs = append(reqs, c.Req)
			wants = append(wants, w.Allowed)
		}
	}
	if len(reqs) == 0 {
		t.Fatalf("fixture %q has no answerable check", f.Name)
	}
	reqs = append(reqs, reqs[0])
	wants = append(wants, wants[0])
	got, err := a.CheckBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("CheckBatch: %v", err)
	}
	if len(got) != len(reqs) {
		t.Fatalf("CheckBatch returned %d decisions for %d requests", len(got), len(reqs))
	}
	for i := range reqs {
		if got[i].Allowed != wants[i] {
			t.Errorf("CheckBatch[%d] %+v = %v, want %v", i, reqs[i], got[i].Allowed, wants[i])
		}
	}
	one, err := a.CheckBatch(context.Background(), reqs[:1])
	if err != nil || len(one) != 1 || one[0].Allowed != wants[0] {
		t.Fatalf("single-item CheckBatch = %v, %v; want [%v]", one, err, wants[0])
	}
}

func runBatchAllOrNothing(t *testing.T, implementation string, f Fixture, a authorization.Authorizer) {
	var good, bad authorization.CheckRequest
	var wantErr error
	haveGood := false
	for _, c := range f.Checks {
		w := c.Expect(implementation)
		if w.Err == nil && !haveGood {
			good, haveGood = c.Req, true
		}
		if w.Err != nil && wantErr == nil {
			bad, wantErr = c.Req, w.Err
		}
	}
	if wantErr == nil || !haveGood {
		t.Skip("fixture has no erroring check beside an answerable one")
	}
	got, err := a.CheckBatch(context.Background(), []authorization.CheckRequest{good, bad, good})
	if !errors.Is(err, wantErr) {
		t.Fatalf("CheckBatch with a failing item = (%v, %v), want errors.Is %v", got, err, wantErr)
	}
	if got != nil {
		t.Fatalf("CheckBatch returned partial results %v with its error", got)
	}
}

func runLookups(t *testing.T, implementation string, f Fixture, a authorization.Authorizer) {
	for _, c := range f.Lookups {
		t.Run(c.Name, func(t *testing.T) {
			want := c.Expect(implementation)
			ids, pages, err := drain(context.Background(), a, c.Req)
			if !outcome(t, fmt.Sprintf("lookup %+v", c.Req), err, want.Err) {
				return
			}
			if !sameSet(ids, want.IDs) {
				t.Fatalf("lookup union over %d page(s) = %v, want %v", pages, ids, want.IDs)
			}
		})
	}
}

func runLimiter(t *testing.T, implementation string, f Fixture, a authorization.Authorizer) {
	l, ok := a.(authorization.Limiter)
	if !ok {
		t.Skip("LOUD SKIP: Limiter is not implemented; batch and page bounds are unverified")
	}
	limits := l.Limits()
	if limits.MaxCheckBatch < 1 || limits.MaxLookupPage < 1 {
		t.Fatalf("Limits() = %+v; both bounds must be positive", limits)
	}
	var sample authorization.CheckRequest
	var lookupSample authorization.LookupRequest
	for _, c := range f.Checks {
		if c.Expect(implementation).Err == nil {
			sample = c.Req
			break
		}
	}
	for _, c := range f.Lookups {
		if c.Expect(implementation).Err == nil {
			lookupSample = c.Req
			break
		}
	}
	reqs := make([]authorization.CheckRequest, limits.MaxCheckBatch+1)
	for i := range reqs {
		reqs[i] = sample
	}
	if got, err := a.CheckBatch(context.Background(), reqs); !errors.Is(err, sdk.ErrInvalidInput) || got != nil {
		t.Errorf("oversize CheckBatch (%d) = (%d results, %v), want sdk.ErrInvalidInput", len(reqs), len(got), err)
	}
	lookupSample.Limit = limits.MaxLookupPage + 1
	lookupSample.After = ""
	if _, err := a.LookupResources(context.Background(), lookupSample); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("Limit above MaxLookupPage = %v, want sdk.ErrInvalidInput", err)
	}
}

func runSchema(t *testing.T, implementation string, f Fixture, a authorization.Authorizer) {
	s, ok := a.(authorization.SchemaReader)
	if !ok {
		t.Skip("LOUD SKIP: SchemaReader is not implemented; consumers validate policies only at evaluation")
	}
	for _, c := range f.Checks {
		if c.Expect(implementation).Err != nil {
			continue
		}
		rt, name := c.Req.Resource.Type, c.Req.Permission
		if !s.DeclaresResource(rt) || !s.DeclaresPermission(rt, name) {
			t.Errorf("schema does not declare %s#%s (check %q)", rt, name, c.Name)
		}
	}
	for _, fact := range f.Facts {
		if !s.DeclaresSubject(fact.Resource.Type, fact.Relation, fact.Subject.Type) {
			t.Errorf("schema does not admit %s#%s@%s", fact.Resource.Type, fact.Relation, fact.Subject.Type)
		}
	}
	if s.DeclaresResource("authorizationtest_undeclared") {
		t.Error("schema declares an undeclared resource type")
	}
	if rt := f.Facts[0].Resource.Type; s.DeclaresPermission(rt, "authorizationtest_undeclared") {
		t.Errorf("schema declares an undeclared permission on %s", rt)
	}
}

func runCursor(t *testing.T, a authorization.Authorizer) {
	ctx := context.Background()
	alice := user("alice")
	first, err := a.LookupResources(ctx, lookupReq(alice, "view", "document", 10))
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %+v, %v; want a continuation", first, err)
	}
	next := lookupReq(alice, "view", "document", 10)
	next.After = first.NextCursor
	if _, err := a.LookupResources(ctx, next); err != nil {
		t.Fatalf("own cursor rejected: %v", err)
	}
	for name, req := range map[string]authorization.LookupRequest{
		"garbage":         {Principal: alice, Permission: "view", ResourceType: "document", Limit: 10, After: "not-a-cursor"},
		"other principal": {Principal: user("bob"), Permission: "view", ResourceType: "document", Limit: 10, After: first.NextCursor},
		"other type":      {Principal: alice, Permission: "view", ResourceType: "project", Limit: 10, After: first.NextCursor},
	} {
		if _, err := a.LookupResources(ctx, req); !errors.Is(err, authorization.ErrInvalidCursor) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s cursor = %v, want ErrInvalidCursor wrapping sdk.ErrInvalidInput", name, err)
		}
	}
}

func runFilterAuthorized(t *testing.T, a authorization.Authorizer) {
	var candidates, want []string
	for i := range 300 {
		id := DocID(i)
		candidates = append(candidates, id)
		if i < DocumentCount && i%3 != 0 {
			want = append(want, id)
		}
	}
	got, err := authorization.FilterAuthorized(context.Background(), a, user("alice"), "view", "document", candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("FilterAuthorized kept %d ids, want %d in input order", len(got), len(want))
	}
}

func runLookupAll(t *testing.T, a authorization.Authorizer) {
	ctx := context.Background()
	req := lookupReq(user("alice"), "view", "document", 25)
	ids, err := authorization.LookupAll(ctx, a, req, 1000)
	if err != nil || len(ids) != 2*DocumentCount/3 {
		t.Fatalf("LookupAll = %d ids, %v; want %d", len(ids), err, 2*DocumentCount/3)
	}
	if _, err := authorization.LookupAll(ctx, a, req, 2*DocumentCount/3-1); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("LookupAll over its max = %v, want sdk.ErrUnavailable", err)
	}
}

// outcome reports whether the answer should be compared: it fails on an
// unexpected error and accepts an expected one.
func outcome(t *testing.T, what string, err, wantErr error) bool {
	t.Helper()
	switch {
	case wantErr != nil && !errors.Is(err, wantErr):
		t.Fatalf("%s error = %v, want errors.Is %v", what, err, wantErr)
	case wantErr == nil && err != nil:
		t.Fatalf("%s unexpected error: %v", what, err)
	}
	return wantErr == nil
}

// drain pages a lookup through to the end, asserting the page contract: at
// most Limit IDs (the Limiter's MaxLookupPage when Limit is 0) and no repeat
// within a page.
func drain(ctx context.Context, l authorization.Lookup, req authorization.LookupRequest) ([]string, int, error) {
	limit := req.Limit
	if limit == 0 {
		if lim, ok := l.(authorization.Limiter); ok {
			limit = lim.Limits().MaxLookupPage
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for pages := 1; ; pages++ {
		page, err := l.LookupResources(ctx, req)
		if err != nil {
			return nil, pages, err
		}
		if limit > 0 && len(page.IDs) > limit {
			return nil, pages, fmt.Errorf("authorizationtest: page of %d IDs exceeds limit %d", len(page.IDs), limit)
		}
		inPage := map[string]bool{}
		for _, id := range page.IDs {
			if inPage[id] {
				return nil, pages, fmt.Errorf("authorizationtest: id %q repeated within one page", id)
			}
			inPage[id] = true
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		if page.NextCursor == "" {
			return out, pages, nil
		}
		if pages >= maxDrainPages {
			return nil, pages, errors.New("authorizationtest: lookup never finished")
		}
		req.After = page.NextCursor
	}
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

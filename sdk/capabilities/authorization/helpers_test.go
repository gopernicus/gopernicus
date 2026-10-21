package authorization_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

var alice = authorization.Principal{Type: "user", ID: "alice"}

// evenChecker allows ids ending in an even digit and records batch sizes.
type evenChecker struct {
	batches []int
	limit   int
	fail    bool
	short   bool
}

func (c *evenChecker) Check(ctx context.Context, req authorization.CheckRequest) (authorization.Decision, error) {
	ds, err := c.CheckBatch(ctx, []authorization.CheckRequest{req})
	if err != nil {
		return authorization.Decision{}, err
	}
	return ds[0], nil
}

func (c *evenChecker) CheckBatch(_ context.Context, reqs []authorization.CheckRequest) ([]authorization.Decision, error) {
	c.batches = append(c.batches, len(reqs))
	if c.fail {
		return nil, sdk.ErrUnavailable
	}
	out := make([]authorization.Decision, len(reqs))
	for i, r := range reqs {
		last := r.Resource.ID[len(r.Resource.ID)-1]
		out[i] = authorization.Decision{Allowed: (last-'0')%2 == 0}
	}
	if c.short {
		out = out[1:]
	}
	return out, nil
}

type limitedChecker struct{ *evenChecker }

func (c limitedChecker) Limits() authorization.Limits {
	return authorization.Limits{MaxCheckBatch: c.limit, MaxLookupPage: 10}
}

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("d%03d", i)
	}
	return out
}

func TestFilterAuthorizedChunksAndKeepsOrder(t *testing.T) {
	c := limitedChecker{&evenChecker{limit: 4}}
	in := ids(10)
	in = append(in, in[0])
	got, err := authorization.FilterAuthorized(context.Background(), c, alice, "view", "document", in)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, id := range in {
		if (id[len(id)-1]-'0')%2 == 0 {
			want = append(want, id)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("FilterAuthorized = %v, want %v", got, want)
	}
	if !slices.Equal(c.batches, []int{4, 4, 3}) {
		t.Fatalf("batches = %v, want chunks of MaxCheckBatch 4", c.batches)
	}
}

func TestFilterAuthorizedDefaultChunk(t *testing.T) {
	c := &evenChecker{}
	if _, err := authorization.FilterAuthorized(context.Background(), c, alice, "view", "document", ids(authorization.DefaultCheckChunk+1)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.batches, []int{authorization.DefaultCheckChunk, 1}) {
		t.Fatalf("batches = %v", c.batches)
	}
}

func TestFilterAuthorizedFailsWhole(t *testing.T) {
	if got, err := authorization.FilterAuthorized(context.Background(), &evenChecker{fail: true}, alice, "view", "document", ids(3)); !errors.Is(err, sdk.ErrUnavailable) || got != nil {
		t.Fatalf("FilterAuthorized = %v, %v", got, err)
	}
	if got, err := authorization.FilterAuthorized(context.Background(), &evenChecker{short: true}, alice, "view", "document", ids(3)); err == nil || got != nil {
		t.Fatalf("short CheckBatch accepted: %v, %v", got, err)
	}
}

// pagedLookup serves pages from a script, keyed by After.
type pagedLookup struct {
	pages map[string]authorization.LookupPage
}

func (l pagedLookup) LookupResources(_ context.Context, req authorization.LookupRequest) (authorization.LookupPage, error) {
	page, ok := l.pages[req.After]
	if !ok {
		return authorization.LookupPage{}, authorization.ErrInvalidCursor
	}
	return page, nil
}

func TestLookupAllDedupesAndBounds(t *testing.T) {
	l := pagedLookup{pages: map[string]authorization.LookupPage{
		"":   {IDs: []string{"a", "b"}, NextCursor: "c1"},
		"c1": {IDs: []string{"b", "c"}, NextCursor: "c2"},
		"c2": {NextCursor: ""},
	}}
	req := authorization.LookupRequest{Principal: alice, Permission: "view", ResourceType: "document"}
	got, err := authorization.LookupAll(context.Background(), l, req, 3)
	if err != nil || !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("LookupAll = %v, %v", got, err)
	}
	if _, err := authorization.LookupAll(context.Background(), l, req, 2); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("overflow = %v, want sdk.ErrUnavailable", err)
	}
	if _, err := authorization.LookupAll(context.Background(), l, req, 0); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("max 0 = %v", err)
	}
}

func TestLookupAllRepeatedCursor(t *testing.T) {
	l := pagedLookup{pages: map[string]authorization.LookupPage{
		"":     {IDs: []string{"a"}, NextCursor: "same"},
		"same": {IDs: []string{"b"}, NextCursor: "same"},
	}}
	if _, err := authorization.LookupAll(context.Background(), l, authorization.LookupRequest{}, 10); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("repeated cursor = %v, want sdk.ErrUnavailable", err)
	}
}

func TestCursorEnvelope(t *testing.T) {
	req := authorization.LookupRequest{Principal: alice, Permission: "view", ResourceType: "document", Limit: 5}
	req.After = authorization.EncodeCursor(req, "inner-1")
	inner, err := authorization.DecodeCursor(req)
	if err != nil || inner != "inner-1" {
		t.Fatalf("DecodeCursor = %q, %v", inner, err)
	}
	req.Limit = 50
	if inner, err := authorization.DecodeCursor(req); err != nil || inner != "inner-1" {
		t.Fatalf("a different page size must keep the cursor: %q, %v", inner, err)
	}
	if inner, err := authorization.DecodeCursor(authorization.LookupRequest{}); err != nil || inner != "" {
		t.Fatalf("empty After = %q, %v", inner, err)
	}
	for name, mutate := range map[string]func(*authorization.LookupRequest){
		"other principal":  func(r *authorization.LookupRequest) { r.Principal.ID = "bob" },
		"other permission": func(r *authorization.LookupRequest) { r.Permission = "edit" },
		"other type":       func(r *authorization.LookupRequest) { r.ResourceType = "project" },
		"garbage":          func(r *authorization.LookupRequest) { r.After = "not a cursor" },
		"not json":         func(r *authorization.LookupRequest) { r.After = "bm90IGpzb24" },
		"oversize":         func(r *authorization.LookupRequest) { r.After = strings.Repeat("a", 9<<10) },
	} {
		r := req
		mutate(&r)
		if _, err := authorization.DecodeCursor(r); !errors.Is(err, authorization.ErrInvalidCursor) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: %v, want ErrInvalidCursor", name, err)
		}
	}
}

package relationships

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

func TestBatchTraversalRandomBudgetParity(t *testing.T) {
	for seed := range 100 {
		rnd := rand.New(rand.NewSource(int64(seed)))
		store := &graphStore{fakeStore: fakeStore{tuples: randomSetTuples(rnd)}}
		svc := newLimitedService(t, store, randomSetSchema(rnd), authmodel.EvaluationLimits{
			MaxThroughDepth: 1 + rnd.Intn(5), MaxGraphStates: 1 + rnd.Intn(20),
			MaxEvaluationSteps: 1 + rnd.Intn(50), MaxRelationTargets: 1 + rnd.Intn(4),
		})
		var reqs []authmodel.CheckRequest
		for _, rt := range []string{"space", "doc"} {
			for _, id := range randomCandidates(rnd, rt) {
				req := check("u1", rt, id)
				if rnd.Intn(3) == 0 {
					req.Principal.ID = "u2"
				}
				reqs = append(reqs, req)
			}
		}
		t.Run(fmt.Sprint(seed), func(t *testing.T) { assertBatchParity(t, svc, reqs) })
	}
}

func TestBatchTraversalPolymorphicTargets(t *testing.T) {
	store := &fakeStore{tuples: []CreateRelationship{
		tuple("doc", "d1", "parent", "folder", "shared", ""),
		tuple("doc", "d2", "parent", "org", "shared", ""),
		tuple("doc", "d3", "parent", "org", "shared", "member"), // off-model
		tuple("folder", "shared", "viewer", "user", "u1", ""),
		tuple("org", "shared", "member", "user", "u2", ""),
	}}
	svc := newLimitedService(t, store, mixedTargetSchema(true), authmodel.EvaluationLimits{})
	reqs := []authmodel.CheckRequest{
		check("u1", "doc", "d1"), check("u1", "doc", "d2"), check("u2", "doc", "d2"),
		check("u1", "doc", "d3"), check("u1", "folder", "shared"), check("u1", "doc", "d1"),
	}
	assertBatchParity(t, svc, reqs)
	got, err := svc.FilterAuthorized(t.Context(), reqs[0].Principal, "view", "doc", []string{"d2", "d1", "d3", "d1"})
	if err != nil || !slices.Equal(got, []string{"d1", "d1"}) {
		t.Fatalf("polymorphic filter: %v/%v", got, err)
	}
}

// plainBatchReader hides the optional set capability, as an older custom
// reader would. Its scoped reads remain the only authorized source.
type plainBatchReader struct{ CheckReader }

func TestBatchTraversalReaderCompatibility(t *testing.T) {
	store := &fakeStore{tuples: containerTuples(3)}
	svc := newTestService(t, store)
	reqs := []authmodel.CheckRequest{check("u1", "post", "p0"), check("u1", "post", "p1")}
	got, err := svc.checkBatch(t.Context(), plainBatchReader{svc.reader}, reqs)
	if err != nil || len(got) != 2 || !got[0].Allowed || !got[1].Allowed {
		t.Fatalf("compatibility batch=%v/%v", got, err)
	}
	if store.setReads() != 0 || len(store.targetsCalls) != 2 {
		t.Fatalf("unexpected fallback reads: %d sets, %v targets", store.setReads(), store.targetsCalls)
	}
}

type faultSetReader struct {
	Reader
	RelationSetReader
	before func(rt string)
	failRT string
	err    error
	calls  int
}

func (r *faultSetReader) FilterRelation(ctx context.Context, rt string, ids []string, rel, st, sid string, limit int) ([]string, error) {
	r.calls++
	if r.before != nil {
		r.before(rt)
	}
	if rt == r.failRT {
		return nil, r.err
	}
	return r.RelationSetReader.FilterRelation(ctx, rt, ids, rel, st, sid, limit)
}

func TestBatchTraversalErrorsKeepRequestOrder(t *testing.T) {
	store := &fakeStore{tuples: []CreateRelationship{
		tuple("post", "p", "org", "org", "o", ""),
		tuple("org", "o", "tenant", "tenant", "t", ""),
	}}
	svc := newLimitedService(t, store, chainSchema(), authmodel.EvaluationLimits{MaxThroughDepth: 1})
	failure := errors.New("tenant read failed")
	reader := &faultSetReader{Reader: svc.reader, RelationSetReader: svc.reader.(RelationSetReader), failRT: "tenant", err: failure}
	deep := check("u1", "post", "p")
	quick := authmodel.CheckRequest{Principal: deep.Principal, Resource: authmodel.Resource{Type: "tenant", ID: "t"}, Permission: "manage"}
	for _, tc := range []struct {
		reqs []authmodel.CheckRequest
		err  error
	}{{[]authmodel.CheckRequest{deep, quick}, authmodel.ErrEvaluationLimit}, {[]authmodel.CheckRequest{quick, deep}, failure}} {
		if got, err := svc.checkBatch(t.Context(), reader, tc.reqs); !errors.Is(err, tc.err) || got != nil {
			t.Fatalf("batch=%v/%v, want %v", got, err, tc.err)
		}
	}
}

func TestBatchTraversalCancellationAndPanic(t *testing.T) {
	store := &fakeStore{tuples: containerTuples(3)}
	svc := newTestService(t, store)
	reqs := []authmodel.CheckRequest{check("u1", "post", "p0"), check("u1", "post", "p1")}
	for _, mode := range []string{"cancel", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("reader panic")
			reader := &faultSetReader{Reader: svc.reader, RelationSetReader: svc.reader.(RelationSetReader)}
			reader.before = func(string) {
				if mode == "panic" {
					panic(failure)
				}
				cancel()
			}
			func() {
				defer func() {
					if got := recover(); got != nil {
						if mode != "panic" || got != failure {
							t.Fatalf("panic=%v", got)
						}
					} else if mode == "panic" {
						t.Fatal("panic swallowed")
					}
				}()
				if got, err := svc.checkBatch(ctx, reader, reqs); mode == "cancel" && (!errors.Is(err, context.Canceled) || got != nil) {
					t.Fatalf("canceled batch=%v/%v", got, err)
				}
			}()
			if reader.calls != 1 {
				t.Fatalf("read continued after cancellation/panic: %d calls", reader.calls)
			}
			// A fresh operation must not inherit any suspended state or errors.
			assertBatchParity(t, svc, reqs)
		})
	}
}

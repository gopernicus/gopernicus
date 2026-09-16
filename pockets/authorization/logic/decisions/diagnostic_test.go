package decisions

import (
	"context"
	"errors"
	"fmt"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

type diagnosticSnapshot struct {
	*tupleFixture
	completed bool
}

func (d *diagnosticSnapshot) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	err := d.tupleFixture.ReadTupleSnapshot(ctx, fn)
	d.completed = true
	return err
}

func TestScopedDenialDiagnosticIsOptionalAndUsesCompletedSnapshot(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			global := exact(tuples.Global(), "editor")
			_, f := expressionService(t, global)
			source := &diagnosticSnapshot{tupleFixture: f}
			var events []DiagnosticCode
			opts := []Option{WithModel(permissionModel(RoleIn("editor")))}
			if enabled {
				opts = append(opts, WithDiagnosticObserver(func(_ context.Context, event DiagnosticCode) {
					if !source.completed {
						t.Error("observer ran before snapshot completion")
					}
					events = append(events, event)
				}))
			}
			s, err := NewService(source, opts...)
			if err != nil {
				t.Fatal(err)
			}
			// Concurrent removal after the scoped read cannot change the diagnostic's view.
			f.afterRead = func() { delete(f.facts, global) }
			got, err := s.Check(t.Context(), authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: "one"}})
			if err != nil || got.Allowed || got.ReasonCode != authmodel.ReasonDenied {
				t.Fatalf("diagnostic changed decision: %+v, %v", got, err)
			}
			wantReads, wantEvents := 1, 0
			if enabled {
				wantReads, wantEvents = 2, 1
			}
			if f.containsCalls != wantReads || len(events) != wantEvents {
				t.Fatalf("reads=%d events=%v", f.containsCalls, events)
			}
			if enabled && events[0] != DiagnosticGlobalGrantNotApplied {
				t.Fatalf("event=%q", events[0])
			}
		})
	}
}

func TestDiagnosticDoesNotChangeResultsOrPublishFailedOperations(t *testing.T) {
	failure := errors.New("read failed")
	for _, tc := range []struct {
		name        string
		expr        Expression
		setup       func(*tupleFixture)
		wantAllowed bool
		wantErr     error
		wantEvents  int
	}{
		{name: "explicit fallback", expr: Any(RoleIn("editor", authmodel.Resource{Type: "doc", ID: "one"}), Role("editor")), wantAllowed: true},
		{name: "probe error", expr: RoleIn("editor", authmodel.Resource{Type: "doc", ID: "one"}), setup: func(f *tupleFixture) { f.afterRead = func() { f.fail = failure } }},
		{name: "snapshot failure", expr: RoleIn("editor", authmodel.Resource{Type: "doc", ID: "one"}), setup: func(f *tupleFixture) { f.completion = failure }, wantErr: failure},
		{name: "evaluation failure", expr: RoleIn("editor", authmodel.Resource{Type: "doc", ID: "one"}), setup: func(f *tupleFixture) { f.fail = failure }, wantErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, f := expressionService(t, exact(tuples.Global(), "editor"))
			if tc.setup != nil {
				tc.setup(f)
			}
			events := 0
			s, err := NewService(f, WithDiagnosticObserver(func(context.Context, DiagnosticCode) { events++ }))
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Evaluate(t.Context(), expressionPrincipal, tc.expr)
			if !errors.Is(err, tc.wantErr) || got.Allowed != tc.wantAllowed || events != tc.wantEvents {
				t.Fatalf("result=%+v error=%v events=%d", got, err, events)
			}
		})
	}
}

func TestDiagnosticProbesAreBoundedAndBoundViewsDoNotEmit(t *testing.T) {
	_, f := expressionService(t)
	events := 0
	s, err := NewService(f, WithDiagnosticObserver(func(context.Context, DiagnosticCode) { events++ }))
	if err != nil {
		t.Fatal(err)
	}
	var leaves []Expression
	for i := 0; i < 20; i++ {
		leaves = append(leaves, RoleIn(fmt.Sprintf("r%d", i), authmodel.Resource{Type: "doc", ID: "one"}))
	}
	got, err := s.Evaluate(t.Context(), expressionPrincipal, Any(leaves...))
	if err != nil || got.Allowed || f.containsCalls != 20+maxDiagnosticProbes || events != 0 {
		t.Fatalf("bounded probe result=%+v err=%v reads=%d events=%d", got, err, f.containsCalls, events)
	}
	f.facts[exact(tuples.Global(), "editor")] = true
	f.containsCalls = 0
	got, err = s.EvaluateExpressionWith(t.Context(), f, expressionPrincipal, RoleIn("editor", authmodel.Resource{Type: "doc", ID: "one"}))
	if err != nil || got.Allowed || f.containsCalls != 1 || events != 0 {
		t.Fatalf("bound view published before its owner completes: %+v, %v, %d, %d", got, err, f.containsCalls, events)
	}
}

func TestCanceledSnapshotDiscardsDiagnostic(t *testing.T) {
	_, f := expressionService(t, exact(tuples.Global(), "editor"))
	ctx, cancel := context.WithCancel(t.Context())
	source := cancelDiagnosticSnapshot{tupleFixture: f, cancel: cancel}
	events := 0
	s, err := NewService(source, WithDiagnosticObserver(func(context.Context, DiagnosticCode) { events++ }))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Evaluate(ctx, expressionPrincipal, RoleIn("editor", authmodel.Resource{Type: "doc", ID: "one"}))
	if !errors.Is(err, context.Canceled) || got.Allowed || events != 0 {
		t.Fatalf("canceled diagnostic: %+v, %v, events=%d", got, err, events)
	}
}

type cancelDiagnosticSnapshot struct {
	*tupleFixture
	cancel context.CancelFunc
}

func (s cancelDiagnosticSnapshot) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	err := s.tupleFixture.ReadTupleSnapshot(ctx, fn)
	s.cancel()
	return err
}

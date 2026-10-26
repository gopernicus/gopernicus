package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// RunTupleCacheBackend checks the tuplecache.Backend contract shared by every
// backend: one independent mirror per binding, and identical refusals.
// factory returns a fresh, empty backend.
func RunTupleCacheBackend(t *testing.T, factory func(*testing.T) tuplecache.Backend) {
	ctx := context.Background()
	alice := tuples.Tuple{Scope: tuples.On("space", "a"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	bob := tuples.Tuple{Scope: tuples.On("space", "a"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "bob"}}
	forward := tuplecache.SetKey{Scope: alice.Scope, Relation: alice.Relation}
	a1 := tuplecache.State{Binding: "binding-a", Receipt: "a1"}
	b1 := tuplecache.State{Binding: "binding-b", Receipt: "b1"}
	full := func(rows ...tuples.Tuple) tuplecache.Snapshot { return tuplecache.Snapshot{Full: true, Tuples: rows} }
	publish := func(t *testing.T, b tuplecache.Backend, expected, next tuplecache.State, snapshot tuplecache.Snapshot) {
		t.Helper()
		if err := b.Publish(ctx, expected, next, snapshot, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// state drops FullAt, which only State reports; it is checked on its own below.
	state := func(t *testing.T, b tuplecache.Backend, binding string) tuplecache.State {
		t.Helper()
		got, err := b.State(ctx, binding)
		if err != nil {
			t.Fatal(err)
		}
		got.FullAt = time.Time{}
		return got
	}

	t.Run("UnknownBindingIsZero", func(t *testing.T) {
		b := factory(t)
		publish(t, b, tuplecache.State{}, a1, full(alice))
		if got := state(t, b, b1.Binding); got != (tuplecache.State{}) {
			t.Fatalf("unknown binding: %+v", got)
		}
	})
	t.Run("BindingsAreIndependent", func(t *testing.T) {
		b := factory(t)
		publish(t, b, tuplecache.State{}, a1, full(alice))
		publish(t, b, tuplecache.State{}, b1, full(bob))
		for _, c := range []struct {
			state tuplecache.State
			want  tuples.Tuple
		}{{a1, alice}, {b1, bob}} {
			if got := state(t, b, c.state.Binding); got != c.state {
				t.Fatalf("state %+v, want %+v", got, c.state)
			}
			sets, err := b.Read(ctx, c.state, []tuplecache.SetKey{forward})
			if err != nil || len(sets) != 1 || !slices.Equal(sets[0], []tuples.Tuple{c.want}) {
				t.Fatalf("%s read %v/%v", c.state.Binding, sets, err)
			}
		}
	})
	t.Run("ExpiredMirrorReportsReceipt", func(t *testing.T) {
		b := factory(t)
		if err := b.Publish(ctx, tuplecache.State{}, a1, full(alice), 50*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if got := state(t, b, a1.Binding); got != a1 {
			t.Fatalf("expired mirror lost its receipt: %+v", got)
		}
		if _, err := b.Read(ctx, a1, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatalf("expired mirror read: %v", err)
		}
	})
	t.Run("ForeignReceiptIsUnavailable", func(t *testing.T) {
		b := factory(t)
		publish(t, b, tuplecache.State{}, a1, full(alice))
		publish(t, b, tuplecache.State{}, b1, full(bob))
		if _, err := b.Read(ctx, tuplecache.State{Binding: a1.Binding, Receipt: b1.Receipt}, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatalf("binding A with B's receipt: %v", err)
		}
	})
	t.Run("EmptyExpectedOverExistingMirrorConflicts", func(t *testing.T) {
		b := factory(t)
		publish(t, b, tuplecache.State{}, a1, full(alice))
		next := tuplecache.State{Binding: a1.Binding, Receipt: "a2"}
		if err := b.Publish(ctx, tuplecache.State{}, next, full(bob), time.Minute); !errors.Is(err, tuplecache.ErrConflict) {
			t.Fatalf("empty expected over a mirror: %v", err)
		}
		if got := state(t, b, a1.Binding); got != a1 {
			t.Fatalf("conflict changed the mirror: %+v", got)
		}
	})
	t.Run("CrossBindingExpectedIsErrBinding", func(t *testing.T) {
		b := factory(t)
		publish(t, b, tuplecache.State{}, a1, full(alice))
		if err := b.Publish(ctx, a1, b1, full(bob), time.Minute); !errors.Is(err, tuplecache.ErrBinding) {
			t.Fatalf("cross-binding expected: %v", err)
		}
		if got := state(t, b, b1.Binding); got != (tuplecache.State{}) {
			t.Fatalf("refused publication created a mirror: %+v", got)
		}
	})
	t.Run("DeltaWithoutMirrorIsUnavailable", func(t *testing.T) {
		b := factory(t)
		delta := tuplecache.Snapshot{Changes: []tuplecache.Change{{ID: "1", After: &alice}}}
		if err := b.Publish(ctx, tuplecache.State{}, a1, delta, time.Minute); !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatalf("delta without a mirror: %v", err)
		}
	})
	t.Run("UnstrippedStateAddressesTheMirror", func(t *testing.T) {
		b := factory(t)
		publish(t, b, tuplecache.State{}, a1, full(alice))
		current, err := b.State(ctx, a1.Binding)
		if err != nil {
			t.Fatal(err)
		}
		if sets, err := b.Read(ctx, current, []tuplecache.SetKey{forward}); err != nil || len(sets) != 1 || !slices.Equal(sets[0], []tuples.Tuple{alice}) {
			t.Fatalf("read with the State result: %v/%v", sets, err)
		}
		a2 := tuplecache.State{Binding: a1.Binding, Receipt: "a2"}
		publish(t, b, current, a2, tuplecache.Snapshot{Changes: []tuplecache.Change{{ID: "1", After: &bob}}})
		if got := state(t, b, a1.Binding); got != a2 {
			t.Fatalf("delta with the State result as expected: %+v", got)
		}
	})
	t.Run("FullAtMarksFullPublications", func(t *testing.T) {
		b := factory(t)
		publish(t, b, tuplecache.State{}, a1, full(alice))
		first, err := b.State(ctx, a1.Binding)
		if err != nil {
			t.Fatal(err)
		}
		if first.FullAt.IsZero() {
			t.Skip("backend does not report FullAt")
		}
		a2 := tuplecache.State{Binding: a1.Binding, Receipt: "a2"}
		publish(t, b, a1, a2, tuplecache.Snapshot{Changes: []tuplecache.Change{{ID: "1", After: &bob}}})
		if got, err := b.State(ctx, a1.Binding); err != nil || !got.FullAt.Equal(first.FullAt) {
			t.Fatalf("delta moved FullAt: %v -> %+v/%v", first.FullAt, got, err)
		}
		time.Sleep(2 * time.Millisecond)
		a3 := tuplecache.State{Binding: a1.Binding, Receipt: "a3"}
		publish(t, b, a2, a3, full(alice))
		third, err := b.State(ctx, a1.Binding)
		if err != nil || !third.FullAt.After(first.FullAt) {
			t.Fatalf("full publication kept FullAt: %v -> %+v/%v", first.FullAt, third, err)
		}
		// A next differing from expected only in FullAt is the same receipt:
		// changes still require a new one.
		same := tuplecache.State{Binding: third.Binding, Receipt: third.Receipt, FullAt: third.FullAt.Add(time.Hour)}
		if err := b.Publish(ctx, third, same, full(bob), time.Minute); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("same receipt, different FullAt: %v", err)
		}
		if got := state(t, b, a1.Binding); got != a3 {
			t.Fatalf("refused publication changed the mirror: %+v", got)
		}
	})
	t.Run("InvalidBindingIsInvalidInput", func(t *testing.T) {
		b := factory(t)
		for _, binding := range []string{"a{b", "a}b", "a b", "café"} {
			if _, err := b.State(ctx, binding); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("State(%q): %v", binding, err)
			}
			bad := tuplecache.State{Binding: binding, Receipt: "r"}
			if _, err := b.Read(ctx, bad, nil); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Read(%q): %v", binding, err)
			}
			if err := b.Publish(ctx, tuplecache.State{}, bad, full(alice), time.Minute); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Publish(%q): %v", binding, err)
			}
		}
		if _, err := b.State(ctx, ""); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("State(\"\"): %v", err)
		}
	})
}

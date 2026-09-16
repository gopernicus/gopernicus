package roles

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type fakeRoleStore struct {
	tuples.Storer
	rows                     map[tuples.Tuple]bool
	err, completion          error
	reads, snapshots, writes int
	batch                    []tuples.Tuple
	query                    tuples.Query
	page                     list.Page[tuples.Tuple]
	cancel                   context.CancelFunc
}

func (f *fakeRoleStore) Contains(ctx context.Context, t tuples.Tuple) (bool, error) {
	f.reads++
	if f.cancel != nil {
		f.cancel()
	}
	return f.rows[t], f.err
}
func (f *fakeRoleStore) ContainsMany(ctx context.Context, ts []tuples.Tuple) ([]bool, error) {
	f.batch = append([]tuples.Tuple(nil), ts...)
	values := make([]bool, len(ts))
	for i, t := range ts {
		var err error
		values[i], err = f.Contains(ctx, t)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}
func (f *fakeRoleStore) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	f.snapshots++
	if err := fn(ctx, f); err != nil {
		return err
	}
	return f.completion
}
func (f *fakeRoleStore) ApplyTuples(ctx context.Context, c tuples.Changes) error {
	f.writes++
	if f.err != nil {
		return f.err
	}
	if f.rows == nil {
		f.rows = map[tuples.Tuple]bool{}
	}
	for _, t := range c.Add {
		f.rows[t] = true
	}
	for _, t := range c.Remove {
		delete(f.rows, t)
	}
	return nil
}
func (f *fakeRoleStore) ListTuples(ctx context.Context, q tuples.Query, r list.Request) (list.Page[tuples.Tuple], error) {
	f.query = q
	return f.page, f.err
}

type roleFixture struct {
	*Service
	*Writer
}

func newRoleFixture(t *testing.T, store tuples.Storer) roleFixture {
	t.Helper()
	s, e := NewService(store)
	if e != nil {
		t.Fatal(e)
	}
	w, e := NewWriter(store)
	if e != nil {
		t.Fatal(e)
	}
	return roleFixture{s, w}
}

package authorizationhttp

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

type failedFacts struct {
	*memory.Tuples
	err error
}

func (f *failedFacts) Contains(context.Context, tuples.Tuple) (bool, error) { return false, f.err }
func (f *failedFacts) ContainsMany(context.Context, []tuples.Tuple) ([]bool, error) {
	return nil, f.err
}
func (f *failedFacts) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	return fn(ctx, f)
}

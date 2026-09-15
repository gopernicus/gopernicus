package decisions

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

type operationResult struct {
	result      authmodel.CheckResult
	explanation authmodel.Explanation
	batch       []authmodel.CheckResult
}

func newTupleCache(cfg config) (*tuplecache.TupleCache, error) {
	if cfg.backend == nil {
		return nil, nil
	}
	if typedNil(cfg.backend) || cfg.TupleSource == nil || typedNil(cfg.TupleSource) {
		return nil, fmt.Errorf("authorization: TupleCache requires source and backend: %w", sdk.ErrInvalidInput)
	}
	if cfg.Relationships == nil {
		return nil, fmt.Errorf("authorization: TupleCache requires relationships: %w", sdk.ErrInvalidInput)
	}
	if cfg.Relationships.TupleCacheBinding() != cfg.TupleSource.Binding() {
		return nil, tuplecache.ErrBinding
	}
	if cfg.Roles != nil {
		binding, ok := cfg.Roles.(interface{ TupleCacheBinding() string })
		if !ok || binding.TupleCacheBinding() != cfg.TupleSource.Binding() {
			return nil, tuplecache.ErrBinding
		}
	}
	return tuplecache.New(cfg.TupleSource, cfg.backend, tuplecache.WithPolicy(cfg.tuplePolicy))
}

func (c *Service) TupleCache() *tuplecache.TupleCache {
	if c == nil {
		return nil
	}
	return c.tupleCache
}

func (c *Service) runTupleCache(ctx context.Context, evaluate func(context.Context, tuplecache.CheckReads) (operationResult, error)) (operationResult, error) {
	var result operationResult
	err := c.tupleCache.Run(ctx, func(ctx context.Context, reads tuplecache.CheckReads) error {
		var err error
		result, err = evaluate(ctx, reads)
		return err
	})
	if err != nil {
		return operationResult{}, err
	}
	return result, err
}

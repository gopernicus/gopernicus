package decisions

import (
	"context"
	"errors"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

var errCacheMiss = errors.New("authorization cache-only read unavailable")

type operationResult struct {
	result      authmodel.CheckResult
	explanation authmodel.Explanation
	batch       []authmodel.CheckResult
}

func (r *CacheRuntime) run(ctx context.Context, evaluate func(context.Context, CheckReads) (operationResult, error)) (operationResult, error) {
	if err := ctx.Err(); err != nil {
		return operationResult{}, err
	}
	observed, eligible := r.capture(ctx)
	if !eligible {
		return evaluate(ctx, nil)
	}
	cacheCtx, cancel := context.WithTimeout(ctx, r.policy.CacheTimeout)
	cached := &cacheReads{runtime: r, version: observed.version}
	result, err := evaluate(cacheCtx, cached)
	expired := cacheCtx.Err() != nil
	cancel()
	if callerErr := ctx.Err(); callerErr != nil {
		return operationResult{}, callerErr
	}
	if err != nil && !errors.Is(err, errCacheMiss) && !(expired && errors.Is(err, context.DeadlineExceeded)) {
		return result, err
	}
	if err == nil && !expired && r.valid(observed) {
		r.count(func(s *CacheStats) { s.HitComplete++ })
		return result, nil
	}
	r.count(func(s *CacheStats) { s.MissFallback++ })
	var staging *cacheReads
	var evaluationErr error
	result = operationResult{}
	err = r.source.ReadSnapshot(ctx, func(snapshotCtx context.Context, version CacheVersion, reads CheckReads) error {
		if err := version.Validate(); err != nil {
			r.latch()
			return err
		}
		if version.Epoch != observed.version.Epoch || version.Generation < observed.version.Generation {
			r.latch()
			return ErrCacheVersion
		}
		if reads == nil || typedNil(reads) {
			return ErrCacheVersion
		}
		staging = &cacheReads{runtime: r, version: version, durable: reads}
		var err error
		result, err = evaluate(snapshotCtx, staging)
		evaluationErr = err
		return err
	})
	if errors.Is(err, ErrCacheVersion) {
		r.latch()
	}
	if callerErr := ctx.Err(); callerErr != nil {
		return operationResult{}, callerErr
	}
	if err != nil {
		if evaluationErr != nil {
			return result, err
		}
		return operationResult{}, err
	}
	if staging == nil {
		return operationResult{}, ErrCacheVersion
	}
	staging.publish(ctx)
	if err := ctx.Err(); err != nil {
		return operationResult{}, err
	}
	return result, nil
}

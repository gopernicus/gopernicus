package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// A host can load this policy from its own account/plan repository. Resolution
// can fail and must happen before admission. The SDK needs only the resulting
// key and Limit; it has no account, plan, subscription or authentication schema.
type catalogLimitPolicy func(context.Context, string) (ratelimiter.Limit, error)

func allowCatalog(ctx context.Context, limiter ratelimiter.Allower, policy catalogLimitPolicy, accountID string) (ratelimiter.Result, error) {
	limit, err := policy(ctx, accountID)
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("resolve catalog limit: %w", err)
	}
	return limiter.Allow(ctx, "catalog:v1:"+accountID, limit)
}

func Example_hostRateLimitPolicy() {
	ctx := context.Background()
	limiter := ratelimiter.NewMemory(ratelimiter.WithMaxEntries(100))
	policy := func(ctx context.Context, accountID string) (ratelimiter.Limit, error) {
		if err := ctx.Err(); err != nil {
			return ratelimiter.Limit{}, err
		}
		switch accountID {
		case "demo":
			return ratelimiter.PerMinute(1), nil // This host chooses its budgets.
		case "team":
			return ratelimiter.PerMinute(10), nil
		default:
			return ratelimiter.Limit{}, sdk.ErrNotFound
		}
	}
	for range 2 {
		result, err := allowCatalog(ctx, limiter, policy, "demo")
		fmt.Println(result.Allowed, err)
	}
	_, err := allowCatalog(ctx, limiter, policy, "unknown")
	fmt.Println("unknown account:", errors.Is(err, sdk.ErrNotFound))

	// A worker resolves the same host policy, then waits using the same port.
	limit, err := policy(ctx, "team")
	if err != nil {
		panic(err)
	}
	fmt.Println("worker:", ratelimiter.Acquire(ctx, limiter, "catalog:v1:team", limit))
	// Output:
	// true <nil>
	// false <nil>
	// unknown account: true
	// worker: <nil>
}

func TestPolicyFailureDoesNotConsumeCatalogBudget(t *testing.T) {
	limiter := ratelimiter.NewMemory()
	policy := func(context.Context, string) (ratelimiter.Limit, error) {
		return ratelimiter.Limit{}, sdk.ErrUnavailable
	}
	if _, err := allowCatalog(context.Background(), limiter, policy, "demo"); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("policy failure = %v", err)
	}
	policy = func(context.Context, string) (ratelimiter.Limit, error) { return ratelimiter.PerMinute(1), nil }
	result, err := allowCatalog(context.Background(), limiter, policy, "demo")
	if err != nil || !result.Allowed {
		t.Fatalf("failed resolution consumed quota: %+v, %v", result, err)
	}
}

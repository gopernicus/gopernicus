package cacher

import (
	"context"
	"time"
)

var (
	_ Storer        = Noop{}
	_ PrefixDeleter = Noop{}
)

// Noop disables caching: reads miss and writes are discarded. It still checks
// cancellation and rejects negative TTL. Its zero value is ready to use.
type Noop struct{}

func (Noop) Get(ctx context.Context, _ string) ([]byte, bool, error) {
	return nil, false, ctx.Err()
}

func (Noop) GetMany(ctx context.Context, _ []string) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return make(map[string][]byte), nil
}

func (Noop) Set(ctx context.Context, _ string, _ []byte, ttl time.Duration) error {
	return validateSet(ctx, ttl)
}

func (Noop) Delete(ctx context.Context, _ string) error { return ctx.Err() }

func (Noop) DeletePrefix(ctx context.Context, _ string) error { return ctx.Err() }

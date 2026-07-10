package goredis

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/capabilities/tracing"
)

// Connection defaults applied to zero-value Config fields by Open. They mirror
// go-redis's own sane defaults so a zero Config still yields a usable client.
const (
	defaultAddr         = "localhost:6379"
	defaultMaxRetries   = 3
	defaultDialTimeout  = 5 * time.Second
	defaultReadTimeout  = 3 * time.Second
	defaultWriteTimeout = 3 * time.Second
	defaultPoolSize     = 10
	defaultMinIdleConns = 2
)

// Config holds the go-redis connection settings for Open. Its `env:` tags let a
// host populate it with sdk/foundation/environment.ParseEnvTags (keys are already namespaced by
// component: REDIS_ADDR, REDIS_PASSWORD, ...; the host passes its own app
// namespace, exactly as bus.go's Options carries EVENT_BUS_* keys). Populating
// from the environment is a convenience, not an import edge — a zero Config is
// filled with the documented defaults by Open, so struct-literal construction
// and bring-your-own-client both stay first-class.
type Config struct {
	Addr         string        `env:"REDIS_ADDR"           default:"localhost:6379"`
	Password     string        `env:"REDIS_PASSWORD"       default:""`
	DB           int           `env:"REDIS_DB"             default:"0"`
	TLSEnabled   bool          `env:"REDIS_TLS_ENABLED"    default:"false"`
	MaxRetries   int           `env:"REDIS_MAX_RETRIES"    default:"3"`
	DialTimeout  time.Duration `env:"REDIS_DIAL_TIMEOUT"   default:"5s"`
	ReadTimeout  time.Duration `env:"REDIS_READ_TIMEOUT"   default:"3s"`
	WriteTimeout time.Duration `env:"REDIS_WRITE_TIMEOUT"  default:"3s"`
	PoolSize     int           `env:"REDIS_POOL_SIZE"      default:"10"`
	MinIdleConns int           `env:"REDIS_MIN_IDLE_CONNS" default:"2"`
}

// ClientOption configures Open — currently to install go-redis instrumentation
// hooks on the client before the fail-fast ping.
type ClientOption func(*clientOptions)

// clientOptions collects the hooks an Open call installs.
type clientOptions struct {
	hooks []redis.Hook
}

// WithLogging installs a LoggingHook so command errors are logged always and,
// when a slow threshold is configured, commands slower than it. A nil logger
// falls back to slog.Default().
func WithLogging(log *slog.Logger, opts ...LoggingOption) ClientOption {
	return func(o *clientOptions) {
		o.hooks = append(o.hooks, LoggingHook(log, opts...))
	}
}

// WithTracing installs a TracingHook so each command runs inside a span from
// tracer, the sdk/capabilities/tracing port. A nil tracer yields a Noop-backed hook.
func WithTracing(tracer tracing.Tracer) ClientOption {
	return func(o *clientOptions) {
		o.hooks = append(o.hooks, TracingHook(tracer))
	}
}

// Open builds a *redis.Client from cfg, installs any hooks the ClientOptions
// carry, and verifies the server with a fail-fast PING before returning. Zero
// fields take the documented defaults; go-redis's own defaults fill anything
// left unset.
//
// Open performs a construction-time network round trip: the PING blocks until it
// succeeds, ctx is done, or the dial times out — pass a ctx with a deadline to
// bound it (the same construction-time reachability check the datastores/pgxdb
// Open performs). On ping failure the client is closed and the error returned.
//
// It returns the RAW *redis.Client with no wrapper type. The facility
// constructors (New, NewCacher, NewLimiter) all take a *redis.Client directly,
// so a client from Open is interchangeable with a bring-your-own
// redis.NewClient client, and one client can feed every facility at once.
func Open(ctx context.Context, cfg Config, opts ...ClientOption) (*redis.Client, error) {
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = defaultDialTimeout
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = defaultReadTimeout
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = defaultWriteTimeout
	}
	if cfg.PoolSize == 0 {
		cfg.PoolSize = defaultPoolSize
	}
	if cfg.MinIdleConns == 0 {
		cfg.MinIdleConns = defaultMinIdleConns
	}

	redisOpts := &redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		MaxRetries:   cfg.MaxRetries,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
	}
	if cfg.TLSEnabled {
		redisOpts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	var co clientOptions
	for _, opt := range opts {
		opt(&co)
	}

	rdb := redis.NewClient(redisOpts)
	for _, hook := range co.hooks {
		rdb.AddHook(hook)
	}

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("goredis: pinging redis at %s: %w", cfg.Addr, err)
	}

	return rdb, nil
}

// StatusCheck returns nil if it can successfully talk to Redis. It mirrors the
// datastores/pgxdb StatusCheck: when ctx carries no deadline it bounds the PING
// with a one-second timeout, otherwise it honors the caller's deadline.
func StatusCheck(ctx context.Context, rdb *redis.Client) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	return rdb.Ping(ctx).Err()
}

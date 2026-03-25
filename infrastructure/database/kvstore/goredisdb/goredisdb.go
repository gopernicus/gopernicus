// Package goredisdb provides a Redis client for the infrastructure layer.
// It follows the same patterns as pgxdb — providing connection management only.
// Returns *redis.Client directly for use by stores.
package goredisdb

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is a type alias for the underlying Redis client so callers
// don't need to import go-redis directly.
type Client = redis.Client

// Options represents the exportable Redis configuration.
// Use sdk/environment.ParseEnvTags to populate from environment variables.
//
//	var cfg goredisdb.Options
//	environment.ParseEnvTags("MYAPP", &cfg) // reads MYAPP_REDIS_ADDR, etc.
type Options struct {
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

// options holds the internal runtime configuration.
type options struct {
	addr           string
	password       string
	db             int
	tlsEnabled     bool
	maxRetries     int
	dialTimeout    time.Duration
	readTimeout    time.Duration
	writeTimeout   time.Duration
	poolSize       int
	minIdleConns   int
	connectTimeout time.Duration
	hooks          []redis.Hook
}

// Option is a function that configures the Redis options.
type Option func(*options)

// WithAddr overrides the Redis address.
func WithAddr(addr string) Option {
	return func(o *options) { o.addr = addr }
}

// WithPassword sets the Redis password.
func WithPassword(password string) Option {
	return func(o *options) { o.password = password }
}

// WithDB sets the Redis database number.
func WithDB(db int) Option {
	return func(o *options) { o.db = db }
}

// WithMaxRetries sets the maximum number of retries.
func WithMaxRetries(retries int) Option {
	return func(o *options) { o.maxRetries = retries }
}

// WithDialTimeout sets the dial timeout.
func WithDialTimeout(timeout time.Duration) Option {
	return func(o *options) { o.dialTimeout = timeout }
}

// WithReadTimeout sets the read timeout.
func WithReadTimeout(timeout time.Duration) Option {
	return func(o *options) { o.readTimeout = timeout }
}

// WithWriteTimeout sets the write timeout.
func WithWriteTimeout(timeout time.Duration) Option {
	return func(o *options) { o.writeTimeout = timeout }
}

// WithPoolSize sets the connection pool size.
func WithPoolSize(size int) Option {
	return func(o *options) { o.poolSize = size }
}

// WithMinIdleConns sets the minimum number of idle connections.
func WithMinIdleConns(conns int) Option {
	return func(o *options) { o.minIdleConns = conns }
}

// WithConnectTimeout sets the connection timeout for initial ping.
func WithConnectTimeout(timeout time.Duration) Option {
	return func(o *options) { o.connectTimeout = timeout }
}

// WithHook adds a hook to the Redis client (e.g., for tracing).
func WithHook(hook redis.Hook) Option {
	return func(o *options) { o.hooks = append(o.hooks, hook) }
}

// New creates a new Redis client with given config and applies options.
// Returns *redis.Client directly (same pattern as pgxdb returning *pgxpool.Pool).
func New(cfg Options, opts ...Option) (*redis.Client, error) {
	internalOpts := &options{
		addr:           cfg.Addr,
		password:       cfg.Password,
		db:             cfg.DB,
		tlsEnabled:     cfg.TLSEnabled,
		maxRetries:     cfg.MaxRetries,
		dialTimeout:    cfg.DialTimeout,
		readTimeout:    cfg.ReadTimeout,
		writeTimeout:   cfg.WriteTimeout,
		poolSize:       cfg.PoolSize,
		minIdleConns:   cfg.MinIdleConns,
		connectTimeout: 10 * time.Second,
	}

	for _, opt := range opts {
		opt(internalOpts)
	}

	return openRedis(internalOpts)
}

// NewTestClient creates a test Redis client with relaxed defaults.
func NewTestClient(addr string, opts ...Option) (*redis.Client, error) {
	cfg := Options{
		Addr:         addr,
		MaxRetries:   3,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     5,
		MinIdleConns: 1,
	}
	return New(cfg, opts...)
}

// openRedis creates the actual Redis connection.
func openRedis(opts *options) (*redis.Client, error) {
	redisOpts := &redis.Options{
		Addr:         opts.addr,
		Password:     opts.password,
		DB:           opts.db,
		MaxRetries:   opts.maxRetries,
		DialTimeout:  opts.dialTimeout,
		ReadTimeout:  opts.readTimeout,
		WriteTimeout: opts.writeTimeout,
		PoolSize:     opts.poolSize,
		MinIdleConns: opts.minIdleConns,
	}

	if opts.tlsEnabled {
		redisOpts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	rdb := redis.NewClient(redisOpts)

	for _, hook := range opts.hooks {
		rdb.AddHook(hook)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.connectTimeout)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, fmt.Errorf("pinging redis: %w", err)
	}

	return rdb, nil
}

// StatusCheck returns nil if it can successfully talk to Redis.
func StatusCheck(ctx context.Context, client *redis.Client) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	return client.Ping(ctx).Err()
}

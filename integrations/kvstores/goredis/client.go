package goredis

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/tracing"
)

// Connection defaults applied to zero-value Config fields by Open. They mirror
// go-redis's own sane defaults so a zero Config still yields a usable client.
const (
	defaultHost         = "localhost"
	defaultPort         = 6379
	defaultMaxRetries   = 3
	defaultDialTimeout  = 5 * time.Second
	defaultReadTimeout  = 3 * time.Second
	defaultWriteTimeout = 3 * time.Second
	defaultPoolSize     = 10
	defaultMinIdleConns = 2
)

// Config holds the go-redis connection settings for Open. Its `env:` tags let a
// host populate it with sdk/pkg/environment.ParseEnvTags (keys are already namespaced by
// component: REDIS_HOST, REDIS_PASSWORD, ...; the host passes its own app
// namespace). Populating
// from the environment is a convenience, not an import edge — a zero Config is
// filled with the documented defaults by Open, so struct-literal construction
// and bring-your-own-client both stay first-class.
//
// There are two ways to say where the server is, and URL wins:
//
//   - URL is a redis://, rediss:// or unix:// connection URL as go-redis's
//     ParseURL reads it (rediss:// turns TLS on; the userinfo is the ACL user and
//     password; the path or ?db= is the database; scalar options such as
//     ?dial_timeout=3s are honoured). When it is set it supplies the address, the
//     user, the password, the database and TLS, and Host, Port, Username,
//     Password, DB and TLSEnabled are NOT READ. The retry, timeout and pool
//     fields still apply to whatever the URL leaves unsaid. It carries a
//     password: never log it. Open's own errors name the resolved host:port and
//     nothing else.
//   - Host and Port otherwise. Port set: the address is Host:Port, and a Host
//     that already carries a port is refused rather than guessed at. Port unset:
//     Host is read as written ("cache.internal:6380"), and a Host with no port at
//     all gets Redis's 6379.
//
// Username names the ACL user to authenticate as. Empty keeps the
// password-only AUTH, which Redis and Valkey read as the "default" user; a
// managed service whose credential belongs to any other user needs it set.
type Config struct {
	URL          string        `env:"REDIS_URL"            default:""`
	Host         string        `env:"REDIS_HOST"           default:"localhost"`
	Port         int           `env:"REDIS_PORT"           default:"0"`
	Username     string        `env:"REDIS_USERNAME"       default:""`
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
	err   error
}

// WithLogging installs a LoggingHook so command errors are logged always and,
// when a slow threshold is configured, commands slower than it. A nil logger
// falls back to slog.Default(). Repeated calls add hooks in order. The supplied
// LoggingOption slice is copied for reuse; log remains a borrowed dependency.
func WithLogging(log *slog.Logger, opts ...LoggingOption) ClientOption {
	snapshot := append([]LoggingOption(nil), opts...)
	return func(o *clientOptions) {
		for _, opt := range snapshot {
			if opt == nil {
				o.err = fmt.Errorf("goredis: nil LoggingOption: %w", sdk.ErrInvalidInput)
				return
			}
		}
		o.hooks = append(o.hooks, LoggingHook(log, snapshot...))
	}
}

// WithTracing installs a TracingHook so each command runs inside a span from
// tracer, the sdk/capabilities/tracing port. A nil tracer yields a Noop-backed hook.
// Repeated calls add hooks in order.
func WithTracing(tracer tracing.Tracer) ClientOption {
	return func(o *clientOptions) {
		o.hooks = append(o.hooks, TracingHook(tracer))
	}
}

// Open builds a *redis.Client from cfg, installs any hooks the ClientOptions
// carry, and verifies the server with a fail-fast PING before returning. Nil
// ClientOption or nested LoggingOption values return an error wrapping
// sdk.ErrInvalidInput before allocating a client. Zero
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
	redisOpts, err := cfg.options()
	if err != nil {
		return nil, err
	}

	var co clientOptions
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("goredis: nil ClientOption: %w", sdk.ErrInvalidInput)
		}
		opt(&co)
		if co.err != nil {
			return nil, co.err
		}
	}

	rdb := redis.NewClient(redisOpts)
	for _, hook := range co.hooks {
		rdb.AddHook(hook)
	}

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, fmt.Errorf("goredis: pinging redis at %s: %w", redisOpts.Addr, err)
	}

	return rdb, nil
}

// options resolves cfg into the go-redis options Open dials with: the
// documented defaults for zero fields, then URL or Host/Port as Config's doc
// comment describes. It is a method rather than inline in Open so the
// resolution is testable without a server.
func (cfg Config) options() (*redis.Options, error) {
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

	var opts *redis.Options
	if strings.TrimSpace(cfg.URL) != "" {
		parsed, err := redis.ParseURL(strings.TrimSpace(cfg.URL))
		if err != nil {
			return nil, fmt.Errorf("goredis: REDIS_URL: %s: %w", urlFault(err), sdk.ErrInvalidInput)
		}
		opts = parsed
		if opts.TLSConfig != nil && opts.TLSConfig.MinVersion < tls.VersionTLS12 {
			opts.TLSConfig.MinVersion = tls.VersionTLS12
		}
	} else {
		addr, err := cfg.address()
		if err != nil {
			return nil, err
		}
		opts = &redis.Options{Addr: addr, Username: cfg.Username, Password: cfg.Password, DB: cfg.DB}
		if cfg.TLSEnabled {
			opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
	}

	// A URL may name any of these itself (?dial_timeout=3s); whatever it leaves
	// zero takes the field, which by now holds the documented default.
	if opts.MaxRetries == 0 {
		opts.MaxRetries = cfg.MaxRetries
	}
	if opts.DialTimeout == 0 {
		opts.DialTimeout = cfg.DialTimeout
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = cfg.ReadTimeout
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = cfg.WriteTimeout
	}
	if opts.PoolSize == 0 {
		opts.PoolSize = cfg.PoolSize
	}
	if opts.MinIdleConns == 0 {
		opts.MinIdleConns = cfg.MinIdleConns
	}
	// go-redis otherwise replaces the I/O context with Background, so the
	// caller's deadline would not bound an established connection's reads.
	opts.ContextTimeoutEnabled = true
	return opts, nil
}

// address joins Host and Port. See Config's doc comment for the three cases.
func (cfg Config) address() (string, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = defaultHost
	}
	if cfg.Port == 0 {
		if _, _, err := net.SplitHostPort(host); err == nil {
			return host, nil
		}
		return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(defaultPort)), nil
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return "", fmt.Errorf("goredis: REDIS_PORT %d is not a port (1-65535): %w", cfg.Port, sdk.ErrInvalidInput)
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return "", fmt.Errorf("goredis: REDIS_HOST %q already carries a port and REDIS_PORT is %d; set the port in one place: %w",
			host, cfg.Port, sdk.ErrInvalidInput)
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(cfg.Port)), nil
}

// urlFault is ParseURL's reason WITHOUT the URL. url.Parse quotes the whole
// input into its error, and the input holds a password.
func urlFault(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}

// StatusCheck returns nil if it can successfully talk to Redis. It mirrors the
// datastores/pgxdb StatusCheck: when ctx carries no deadline it bounds the PING
// with a one-second timeout, otherwise it honors the caller's deadline.
// A borrowed client must enable redis.Options.ContextTimeoutEnabled for I/O
// deadlines; clients returned by Open already do. The client is never modified.
func StatusCheck(ctx context.Context, rdb *redis.Client) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	err := rdb.Ping(ctx).Err()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

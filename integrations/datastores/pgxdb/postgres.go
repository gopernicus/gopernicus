// Package pgxdb is the datastore connector for PostgreSQL: it bridges the
// pgx/v5 driver (pool via pgxpool) to a small wrapper (connection, tx,
// migrations). It is a reusable connector — it owns "how to talk to Postgres,"
// not any app's queries. App-specific repositories live in the app's
// providers/ and consume this package's *DB.
//
// It is its own module (github.com/gopernicus/gopernicus/integrations/datastores/pgxdb), depending
// only on sdk (the sentinels MapError targets, plus the ports it satisfies —
// crud.Transactor and ratelimiter.Limiter) and pgx/v5.
//
// Its exported surface mirrors the turso connector's by convention (Config /
// Open / DB / MapError / StatusCheck / RunMigrations). Nothing mechanically proves
// the two stay aligned — see the module README's non-guarantee note.
package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgreSQL error codes MapError recognizes.
// See: https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	uniqueViolation           = "23505" // unique_violation
	foreignKeyViolation       = "23503" // foreign_key_violation
	checkViolation            = "23514" // check_violation
	notNullViolation          = "23502" // not_null_violation
	invalidTextRepresentation = "22P02" // invalid_text_representation
)

// Startup-packet parameters the connector defaults. pgconn folds every
// non-connection DSN key into ConnConfig.RuntimeParams (pgconn/config.go:351-356)
// and maps PGTZ onto "timezone" / PGOPTIONS onto "options" (:466-469), so the
// host's own choice — DSN key, PGTZ, or an "-c TimeZone=…" options value — is
// visible in that map and is never overridden.
const (
	runtimeParamTimeZone = "timezone"
	runtimeParamOptions  = "options"

	// timeZoneOptionMarker matches "-c timezone=…" / "-c TimeZone=…" inside an
	// options value; the comparison is case-folded.
	timeZoneOptionMarker = "timezone="

	// defaultTimeZone is the session zone applied when the host named none.
	defaultTimeZone = "UTC"
)

// Config holds the PostgreSQL connection settings. Hosts populate it directly
// or via env-tag helpers; Open never reads process environment itself. DSN wins
// over the split Host/Port/User/Password/Database/SSLMode fields.
//
// LogQueries, Logger, and Tracer are deliberate, interim exceptions to
// "no per-connector observability field": pgx exposes exactly one tracing
// seam (pgxpool.ConnConfig.Tracer), so Config forwards it directly rather
// than inventing an options wrapper for a single value. They hold until
// sdk/capabilities/tracing lands. Query logging is symmetric with the turso connector: both
// carry an opt-in LogQueries/Logger with the same dev-only, args-verbatim
// posture — pgx installs it as a native ConnConfig.Tracer, turso threads it
// through its DB/Tx wrapper because database/sql exposes no tracer hook. Tracer
// has no turso analogue: it composes an external pgx.QueryTracer (e.g.
// OpenTelemetry) into that native seam, which SQLite's driver does not expose.
//
// The connector owns pgxpool.Config.AfterConnect: it registers the UTC-located
// timestamptz codecs there on every connection (see the "Time zone" section of
// the module README). Config exposes no AfterConnect field today; should one be
// added, it will CHAIN after the connector's registration, never replace it.
type Config struct {
	DSN      string `env:"DB_URL"`
	Host     string `env:"DB_HOST"`
	Port     string `env:"DB_PORT"`
	User     string `env:"DB_USER"`
	Password string `env:"DB_PASSWORD"`
	Database string `env:"DB_NAME"`
	SSLMode  string `env:"DB_SSLMODE"`

	MaxConns       int           `env:"DB_MAX_CONNS"`
	MinConns       int           `env:"DB_MIN_CONNS"`
	MaxLifetime    time.Duration `env:"DB_MAX_CONN_LIFETIME"`
	MaxIdleTime    time.Duration `env:"DB_MAX_CONN_IDLE_TIME"`
	ConnectTimeout time.Duration `env:"DB_CONNECT_TIMEOUT"`

	// HealthCheckPeriod sets pgxpool's idle-connection liveness check
	// interval. Applied only when non-zero, like MaxConns/MinConns.
	HealthCheckPeriod time.Duration `env:"DB_HEALTH_CHECK_PERIOD"`

	// LogQueries installs a LoggingQueryTracer. It logs SQL args verbatim, so
	// this is dev-only tooling.
	LogQueries bool `env:"DB_LOG_QUERIES" default:"false"`

	// Logger is used only when LogQueries is true. If nil, slog.Default() is
	// used. It is not populated by env parsers.
	Logger *slog.Logger

	// Tracer, when non-nil, is installed as poolConfig.ConnConfig.Tracer
	// before the pool is created. If LogQueries is also true, both tracers are
	// composed. See the asymmetry note above.
	Tracer jackpgx.QueryTracer

	// Retry, when its Attempts is > 1, makes Open verify boot connectivity with a
	// retried real round-trip (StatusCheck: Ping + SELECT 1) under a full-jitter
	// exponential backoff, targeting the orchestration race where the database is
	// not yet accepting connections. The zero value disables all retry: Open
	// pings exactly once (today's behavior). It is not populated by env parsers.
	//
	// This governs ONLY the boot connectivity check. No statement is ever
	// auto-retried by the connector — statement retry is store-owned, explicit,
	// and per-call, because a method verb does not encode idempotency
	// (Query/QueryRow carry RETURNING writes).
	Retry RetryPolicy
}

func (cfg Config) connectionString() (string, error) {
	if cfg.DSN != "" {
		return cfg.DSN, nil
	}
	if !cfg.hasSplitConnectionFields() {
		return "", fmt.Errorf("postgres: empty DSN")
	}

	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == "" {
		port = "5432"
	}
	database := cfg.Database
	if database == "" {
		database = "postgres"
	}

	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, port),
		Path:   database,
	}
	if cfg.User != "" {
		if cfg.Password != "" {
			u.User = url.UserPassword(cfg.User, cfg.Password)
		} else {
			u.User = url.User(cfg.User)
		}
	}
	if cfg.SSLMode != "" {
		q := u.Query()
		q.Set("sslmode", cfg.SSLMode)
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}

func (cfg Config) hasSplitConnectionFields() bool {
	return cfg.Host != "" ||
		cfg.Port != "" ||
		cfg.User != "" ||
		cfg.Password != "" ||
		cfg.Database != "" ||
		cfg.SSLMode != ""
}

// Redacted returns the connection target with any URL password masked.
func (cfg Config) Redacted() string {
	dsn, err := cfg.connectionString()
	if err != nil {
		return redactedDSN
	}
	return RedactDSN(dsn)
}

func (cfg Config) queryTracer() jackpgx.QueryTracer {
	tracer := cfg.Tracer
	if !cfg.LogQueries {
		return tracer
	}

	loggingTracer := NewLoggingQueryTracer(cfg.Logger)
	if tracer == nil {
		return loggingTracer
	}
	return NewMultiQueryTracer(tracer, loggingTracer)
}

// registerUTCTimestamptz registers UTC-located timestamptz codecs on one
// connection's type map, so a scanned timestamptz (and timestamptz[]) is a
// time.Time located in UTC rather than time.Local. It changes presentation, not
// the instant: pgx's binary decoder builds the value with time.Unix and its text
// decoder parses the session-zone offset, and both then apply the codec's
// ScanLocation (pgtype/timestamptz.go:272-278, :326-328).
//
// The array type is registered explicitly over the NEW element type. pgx's
// default "_timestamptz" ArrayCodec captured a pointer to the DEFAULT (that is,
// time.Local-scanning) element type at init — pgtype_default.go:169 registers it
// as ArrayCodec{ElementType: defaultMap.oidToType[TimestamptzOID]} — and the
// array decoders consult that captured codec first when planning the per-element
// scan, falling back to the map only when it declines (array_codec.go:268, :306).
// A plain []time.Time element is one the captured codec declines, so the map
// would win there anyway; a pgtype.Timestamptz element would not. Registering the
// array type keeps every element destination on the UTC codec.
//
// tstzrange / tstzmultirange are deliberately out of scope: they capture the
// same default element pointer (pgtype_default.go:119, :127) and no store in
// this repo scans them.
func registerUTCTimestamptz(m *pgtype.Map) {
	tz := &pgtype.Type{
		Name:  "timestamptz",
		OID:   pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
	}
	m.RegisterType(tz)
	m.RegisterType(&pgtype.Type{
		Name:  "_timestamptz",
		OID:   pgtype.TimestamptzArrayOID,
		Codec: &pgtype.ArrayCodec{ElementType: tz},
	})
}

// hasSessionTimeZone reports whether the host already named a session time zone
// — a "timezone" runtime parameter (from the DSN or PGTZ) or a "timezone=" entry
// inside an options value (from the DSN or PGOPTIONS). Parameter names are
// case-insensitive to the server, so the key comparison is case-folded too:
// writing a second, differently-cased key would put two competing time-zone
// parameters in the startup packet.
func hasSessionTimeZone(params map[string]string) bool {
	for k, v := range params {
		if strings.EqualFold(k, runtimeParamTimeZone) {
			return true
		}
		if strings.EqualFold(k, runtimeParamOptions) && strings.Contains(strings.ToLower(v), timeZoneOptionMarker) {
			return true
		}
	}
	return false
}

// poolConfig builds the pgxpool configuration Open connects with: pgx's own
// parse of the DSN, then this Config's pool sizes and tracer, the connector's
// AfterConnect codec registration, and the default session time zone. The DSN
// string itself is never rewritten — pgconn has already parsed it into
// ConnConfig, so the parsed form is what gets adjusted.
func (cfg Config) poolConfig(dsn string) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing connection string: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = int32(cfg.MaxConns)
	}
	if cfg.MinConns > 0 {
		poolConfig.MinConns = int32(cfg.MinConns)
	}
	poolConfig.MaxConnLifetime = cfg.MaxLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxIdleTime
	if cfg.HealthCheckPeriod > 0 {
		poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod
	}
	if tracer := cfg.queryTracer(); tracer != nil {
		poolConfig.ConnConfig.Tracer = tracer
	}

	poolConfig.AfterConnect = func(_ context.Context, conn *jackpgx.Conn) error {
		registerUTCTimestamptz(conn.TypeMap())
		return nil
	}

	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = map[string]string{}
	}
	if !hasSessionTimeZone(poolConfig.ConnConfig.RuntimeParams) {
		poolConfig.ConnConfig.RuntimeParams[runtimeParamTimeZone] = defaultTimeZone
	}

	return poolConfig, nil
}

// Open connects to a PostgreSQL database via a pgxpool and verifies it with a
// ping. Pool sizes are applied only when non-zero, leaving pgx's own defaults
// in place otherwise. Every connection scans timestamptz in UTC and, unless the
// host named a zone, runs with a UTC session time zone (see poolConfig).
func Open(cfg Config) (*DB, error) {
	dsn, err := cfg.connectionString()
	if err != nil {
		return nil, err
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}

	poolConfig, err := cfg.poolConfig(dsn)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}
	db := &DB{pool: pool}

	// Retry runs the boot connectivity check (StatusCheck: Ping + SELECT 1) under
	// the policy's jittered backoff, targeting the orchestration race where the
	// pool cannot yet acquire a connection. The zero value keeps the single pool
	// ping exactly.
	if cfg.Retry.Attempts > 1 {
		if err := retry(ctx, cfg.Retry, func(ctx context.Context) error {
			return StatusCheck(ctx, db)
		}); err != nil {
			pool.Close()
			return nil, fmt.Errorf("verifying database: %w", err)
		}
		return db, nil
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return db, nil
}

// MapError converts a pgx / PostgreSQL driver error into an sdk/errs sentinel.
// Detection is by SQLSTATE code via pgconn.PgError (vs turso's substring match
// on SQLite messages). Unrecognized errors pass through unchanged. Callers map
// both query errors and Scan errors (jackpgx.ErrNoRows → ErrNotFound) through this.
//
// 22P02 (invalid_text_representation — a malformed uuid or integer literal
// reaching Postgres, typically an unvalidated path parameter) is the one code
// whose server message is kept, as the sentence before the sentinel, because it
// names the offending value and a host that dropped it lost that from its log.
// web.ErrFromDomain answers the generic 400, so the message never reaches a
// client. The other codes return the bare sentinel.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, jackpgx.ErrNoRows) {
		return sdk.ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case uniqueViolation:
			return sdk.ErrAlreadyExists
		case foreignKeyViolation:
			return sdk.ErrInvalidReference
		case checkViolation, notNullViolation:
			return sdk.ErrInvalidInput
		case invalidTextRepresentation:
			return fmt.Errorf("%s: %w", pgErr.Message, sdk.ErrInvalidInput)
		}
	}
	return err
}

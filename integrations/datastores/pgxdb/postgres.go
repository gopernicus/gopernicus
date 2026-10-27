// Package pgxdb is the datastore connector for PostgreSQL: it bridges the
// pgx/v5 driver (pool via pgxpool) to a small wrapper (connection, tx,
// migrations). It is a reusable connector — it owns "how to talk to Postgres,"
// not any app's queries. App-specific repositories live in the app's
// providers/ and consume this package's *DB.
//
// It is its own module (github.com/gopernicus/gopernicus/integrations/datastores/pgxdb), depending
// only on sdk (the sentinels MapError targets, plus the ports it satisfies —
// transaction.Transactor and ratelimiter.Limiter) and pgx/v5.
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
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"

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
	restrictViolation         = "23001" // restrict_violation: ON DELETE/UPDATE RESTRICT on PostgreSQL 18
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

// Startup-packet parameters Config.StatementTimeout and Config.LockTimeout set,
// in whole milliseconds (the server's base unit for both).
const (
	runtimeParamStatementTimeout = "statement_timeout"
	runtimeParamLockTimeout      = "lock_timeout"
)

// Config holds the PostgreSQL connection settings. Hosts populate it directly
// or via env-tag helpers. pgx's DSN parser also honors its standard PG* environment
// defaults. DSN wins over the split Host/Port/User/Password/Database/SSLMode fields.
//
// LogQueries, Logger, and Tracer are deliberate, interim exceptions to
// "no per-connector observability field": pgx exposes exactly one tracing
// seam (pgxpool.ConnConfig.Tracer), so Config forwards it directly rather
// than inventing an options wrapper for a single value. They hold until
// sdk/capabilities/tracing lands. Query logging is symmetric with the turso connector: both
// carry an opt-in LogQueries/LogQueryArgs/Logger with the same dev-only posture
// (argument count and types by default, values only with LogQueryArgs) — pgx
// installs it as a native ConnConfig.Tracer, turso threads it
// through its DB/Tx wrapper because database/sql exposes no tracer hook. Tracer
// has no turso analogue: it composes an external pgx.QueryTracer (e.g.
// OpenTelemetry) into that native seam, which SQLite's driver does not expose.
//
// The connector owns pgxpool.Config.AfterConnect: it registers the UTC-located
// timestamptz codecs there on every connection (see the "Time zone" section of
// the module README). Config exposes no AfterConnect field today; should one be
// added, it will CHAIN after the connector's registration, never replace it.
//
// DSN and Password are sdk.Secret: they render as sdk.Redacted through fmt,
// slog and encoding/json, so read them with Reveal() and display the target
// with Redacted(). A DSN composed from a rendered Secret carries sdk.Redacted
// instead of the password; Open refuses such a DSN or Password with
// sdk.ErrRedactedSecret before any connection attempt.
type Config struct {
	DSN      sdk.Secret `env:"DB_URL"`
	Host     string     `env:"DB_HOST"`
	Port     string     `env:"DB_PORT"`
	User     string     `env:"DB_USER"`
	Password sdk.Secret `env:"DB_PASSWORD"`
	Database string     `env:"DB_NAME"`
	SSLMode  string     `env:"DB_SSLMODE"`

	// Zero pool settings retain the DSN's values or pgx defaults; positive
	// settings override them. Negative values are invalid.
	MaxConns       int           `env:"DB_MAX_CONNS"`
	MinConns       int           `env:"DB_MIN_CONNS"`
	MaxLifetime    time.Duration `env:"DB_MAX_CONN_LIFETIME"`
	MaxIdleTime    time.Duration `env:"DB_MAX_CONN_IDLE_TIME"`
	ConnectTimeout time.Duration `env:"DB_CONNECT_TIMEOUT"`

	// HealthCheckPeriod sets pgxpool's idle-connection liveness check
	// interval. Applied only when non-zero, like MaxConns/MinConns.
	HealthCheckPeriod time.Duration `env:"DB_HEALTH_CHECK_PERIOD"`

	// StatementTimeout and LockTimeout bound every statement and every lock
	// wait on every pooled connection, sent as the statement_timeout and
	// lock_timeout startup parameters in whole milliseconds. Zero leaves the
	// server's (or role's) setting in place. Negative values, and positive
	// values under 1ms (which would send "0", the server's "disabled"), are
	// invalid. A connection string or PGOPTIONS that already names the same
	// parameter is refused rather than silently overridden. Set them for the
	// server process only: a migrator leaves both zero (index builds).
	StatementTimeout time.Duration `env:"DB_STATEMENT_TIMEOUT"`
	LockTimeout      time.Duration `env:"DB_LOCK_TIMEOUT"`

	// LogQueries installs a LoggingQueryTracer, which logs each query's SQL
	// with its argument count and types, never their values. It is dev-only
	// tooling; a production RuntimeMode refuses it.
	LogQueries bool `env:"DB_LOG_QUERIES" default:"false"`

	// LogQueryArgs widens the LogQueries tracer to log bind arguments and
	// driver error text verbatim; those carry secrets and PII. It is inert
	// without LogQueries. A development RuntimeMode logs a WARN while it is
	// set; a production RuntimeMode refuses it even alone.
	LogQueryArgs bool `env:"DB_LOG_QUERY_ARGS" default:"false"`

	// Logger receives the query log when LogQueries is true and, under a
	// non-zero RuntimeMode, the posture lines Open writes. If nil,
	// slog.Default() is used. It is not populated by env parsers.
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

	// RuntimeMode is the host's deployment posture. The zero value is
	// mode-blind: Open behaves exactly as it did before the field existed.
	// Posture refusals are mode-gated; the version advisory (one WARN when the
	// server is below MinServerVersionNum) logs in every mode. A non-zero mode
	// must be environment.ModeDevelopment or ModeProduction.
	// Production refuses, before any connection attempt, a connection target
	// whose TLS does not verify the server (ErrTLSRequired) and query
	// logging (ErrQueryLoggingRefused); development logs one WARN per such
	// condition. See the module README's "Production posture" section. It
	// carries no env tag: the host reads its mode from the process environment
	// in its composition root and sets it here.
	RuntimeMode environment.Mode

	// TLSUnverifiedAcknowledged admits, in production, a target that encrypts
	// without verifying the server — a bare sslmode=require, the shape of a
	// provider that serves a self-signed certificate. It is inert when no
	// target needs it; when honored, Open logs a posture_waiver WARN. No env tag.
	TLSUnverifiedAcknowledged bool

	// TLSDisabledAcknowledged admits every target in production, plaintext
	// included. It is inert when no target needs it; when honored, Open logs a
	// posture_waiver WARN. No env tag.
	TLSDisabledAcknowledged bool

	// AppendOnlyWritableAcknowledged admits, in production, a runtime role that
	// DB.ProbeAppendOnly finds can modify an append-only table
	// (ErrAppendOnlyTableWritable) — the single-role host. It never admits
	// ErrAppendOnlyTableUnwritable. It is inert when nothing needs it; when
	// honored, ProbeAppendOnly logs one posture_waiver WARN per table. No env tag.
	AppendOnlyWritableAcknowledged bool
}

func (cfg Config) connectionString() (string, error) {
	if cfg.DSN != "" {
		return cfg.DSN.Reveal(), nil
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
			u.User = url.UserPassword(cfg.User, cfg.Password.Reveal())
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

// refuseRedactedSecrets names each secret field whose value carries
// sdk.Redacted: it was composed from a rendered Secret, not its Reveal.
func (cfg Config) refuseRedactedSecrets() error {
	var refusals []error
	for _, field := range []struct {
		name  string
		value sdk.Secret
	}{
		{"DSN", cfg.DSN},
		{"Password", cfg.Password},
	} {
		if strings.Contains(field.value.Reveal(), sdk.Redacted) {
			refusals = append(refusals, fmt.Errorf("postgres: Config.%s: %w", field.name, sdk.ErrRedactedSecret))
		}
	}
	return errors.Join(refusals...)
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

	loggingTracer := NewLoggingQueryTracerWithConfig(LoggingQueryTracerConfig{Logger: cfg.Logger, LogArgs: cfg.LogQueryArgs})
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

// namesRuntimeParam reports whether params already set the server parameter
// name: as its own key (case-folded, as the server matches parameter names) or
// inside an options value. Unlike hasSessionTimeZone's substring test, options
// entries are parsed and their names compared exactly, because "lock_timeout="
// is a substring of "-c deadlock_timeout=…".
func namesRuntimeParam(params map[string]string, name string) bool {
	for k, v := range params {
		if strings.EqualFold(k, name) {
			return true
		}
		if strings.EqualFold(k, runtimeParamOptions) && optionsNameParam(v, name) {
			return true
		}
	}
	return false
}

// optionsNameParam reports whether an options value sets name. The server
// reads options as command-line switches — "-c name=value", "-cname=value" or
// "--name=value" — and treats '-' in a parameter name as '_'.
func optionsNameParam(options, name string) bool {
	fields := strings.Fields(options)
	for i := 0; i < len(fields); i++ {
		var setting string
		switch field := fields[i]; {
		case field == "-c":
			if i+1 == len(fields) {
				return false
			}
			i++
			setting = fields[i]
		case strings.HasPrefix(field, "--"):
			setting = field[len("--"):]
		case strings.HasPrefix(field, "-c"):
			setting = field[len("-c"):]
		default:
			continue
		}
		key, _, ok := strings.Cut(setting, "=")
		if ok && strings.EqualFold(strings.ReplaceAll(key, "-", "_"), name) {
			return true
		}
	}
	return false
}

// poolConfig builds the pgxpool configuration Open connects with: pgx's own
// parse of the DSN, the RuntimeMode posture check over every parsed connection
// target, then this Config's pool sizes and tracer, the connector's
// AfterConnect codec registration, the default session time zone, and the
// statement and lock timeouts. The DSN string itself is never rewritten —
// pgconn has already parsed it into ConnConfig, so the parsed form is what gets
// adjusted.
func (cfg Config) poolConfig(dsn string) (*pgxpool.Config, error) {
	if cfg.MaxConns < 0 || cfg.MinConns < 0 || cfg.MaxConns > math.MaxInt32 || cfg.MinConns > math.MaxInt32 {
		return nil, fmt.Errorf("postgres: pool counts must be between 0 and %d: %w", math.MaxInt32, sdk.ErrInvalidInput)
	}
	for _, setting := range []struct {
		name  string
		value time.Duration
	}{
		{"MaxLifetime", cfg.MaxLifetime},
		{"MaxIdleTime", cfg.MaxIdleTime},
		{"HealthCheckPeriod", cfg.HealthCheckPeriod},
		{"ConnectTimeout", cfg.ConnectTimeout},
		{"StatementTimeout", cfg.StatementTimeout},
		{"LockTimeout", cfg.LockTimeout},
	} {
		if setting.value < 0 {
			return nil, fmt.Errorf("postgres: %s cannot be negative: %w", setting.name, sdk.ErrInvalidInput)
		}
	}
	timeouts := []struct {
		name  string
		param string
		value time.Duration
	}{
		{"StatementTimeout", runtimeParamStatementTimeout, cfg.StatementTimeout},
		{"LockTimeout", runtimeParamLockTimeout, cfg.LockTimeout},
	}
	for _, timeout := range timeouts {
		if timeout.value > 0 && timeout.value < time.Millisecond {
			return nil, fmt.Errorf("postgres: %s must be zero or at least 1ms: %w", timeout.name, sdk.ErrInvalidInput)
		}
	}
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing connection string: %w", err)
	}
	if err := cfg.checkPosture(&poolConfig.ConnConfig.Config); err != nil {
		return nil, err
	}
	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = int32(cfg.MaxConns)
	}
	if cfg.MinConns > 0 {
		poolConfig.MinConns = int32(cfg.MinConns)
	}
	if cfg.MaxLifetime > 0 {
		poolConfig.MaxConnLifetime = cfg.MaxLifetime
	}
	if cfg.MaxIdleTime > 0 {
		poolConfig.MaxConnIdleTime = cfg.MaxIdleTime
	}
	if cfg.HealthCheckPeriod > 0 {
		poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod
	}
	if poolConfig.MinConns < 0 || poolConfig.MinConns > poolConfig.MaxConns ||
		poolConfig.MinIdleConns < 0 || poolConfig.MinIdleConns > poolConfig.MaxConns {
		return nil, fmt.Errorf("postgres: minimum pool counts must be between 0 and MaxConns: %w", sdk.ErrInvalidInput)
	}
	if poolConfig.HealthCheckPeriod <= 0 || poolConfig.MaxConnLifetime < 0 ||
		poolConfig.MaxConnIdleTime < 0 || poolConfig.MaxConnLifetimeJitter < 0 {
		return nil, fmt.Errorf("postgres: pool durations must be nonnegative and HealthCheckPeriod must be positive: %w", sdk.ErrInvalidInput)
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
	for _, timeout := range timeouts {
		if timeout.value == 0 {
			continue
		}
		if namesRuntimeParam(poolConfig.ConnConfig.RuntimeParams, timeout.param) {
			return nil, fmt.Errorf("postgres: %s is set and the connection string or PGOPTIONS already names %s; set one: %w", timeout.name, timeout.param, sdk.ErrInvalidInput)
		}
		poolConfig.ConnConfig.RuntimeParams[timeout.param] = strconv.FormatInt(timeout.value.Milliseconds(), 10)
	}

	return poolConfig, nil
}

// Open connects to a PostgreSQL database via a pgxpool and verifies it with a
// ping. Pool sizes are applied only when non-zero, leaving pgx's own defaults
// in place otherwise. Every connection scans timestamptz in UTC and, unless the
// host named a zone, runs with a UTC session time zone (see poolConfig).
// The context bounds startup only; the caller owns the returned DB's lifetime.
// A non-zero RuntimeMode is validated and enforced before the pool exists, so a
// refused configuration never attempts a connection. A DSN or Password that
// contains sdk.Redacted is refused first, in every mode, with
// sdk.ErrRedactedSecret. After the connectivity check Open probes the server
// version and, below MinServerVersionNum, logs one WARN through Logger; the
// probe never fails Open (RunMigrations is where the floor refuses).
func Open(ctx context.Context, cfg Config) (*DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.refuseRedactedSecrets(); err != nil {
		return nil, err
	}
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

	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}
	db := &DB{
		pool:                           pool,
		mode:                           cfg.RuntimeMode,
		logger:                         cfg.Logger,
		appendOnlyWritableAcknowledged: cfg.AppendOnlyWritableAcknowledged,
	}

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
		adviseServerVersion(ctx, db, cfg.Logger, dsn, poolConfig.ConnConfig.Password)
		return db, nil
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	adviseServerVersion(ctx, db, cfg.Logger, dsn, poolConfig.ConnConfig.Password)
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
// client. 42501 (insufficient_privilege — a missing grant, or an append-only
// table's trigger refusing a write) maps to sdk.ErrForbidden and keeps the
// *pgconn.PgError in the chain, because callers match its SQLSTATE with
// errors.As. The other codes return the bare sentinel.
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
		case foreignKeyViolation, restrictViolation:
			return sdk.ErrInvalidReference
		case checkViolation, notNullViolation:
			return sdk.ErrInvalidInput
		case invalidTextRepresentation:
			return fmt.Errorf("%s: %w", pgErr.Message, sdk.ErrInvalidInput)
		case insufficientPrivilege:
			return fmt.Errorf("%w: %w", pgErr, sdk.ErrForbidden)
		}
	}
	return err
}

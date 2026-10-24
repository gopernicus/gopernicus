package pgxdb

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Production posture errors. Open returns them, wrapped with the specific
// reason, only under a production Config.RuntimeMode; match them with
// errors.Is. When both apply, Open joins them so one boot names every refusal.
var (
	// ErrTLSRequired refuses a connection target whose TLS does not verify the
	// server: plaintext (sslmode=disable, or the plaintext fallback that
	// sslmode=prefer and allow keep) or unverified (a bare sslmode=require).
	// The wrapped text names the class, the redacted target and the fix; it
	// never echoes the DSN.
	ErrTLSRequired = errors.New("postgres: production requires verified TLS on every connection target")

	// ErrQueryLoggingRefused refuses Config.LogQueries, Config.LogQueryArgs
	// (even alone), or a Config.Tracer that is or wraps a *LoggingQueryTracer
	// whatever its LogArgs: query logging is development tooling. The wrapped
	// text names each source and its env variable. No acknowledgement admits it.
	ErrQueryLoggingRefused = errors.New("postgres: production refuses query logging")
)

// The TLS classes of one parsed connection target.
const (
	tlsLocal      tlsClass = "local_socket"
	tlsVerified   tlsClass = "verified"
	tlsUnverified tlsClass = "unverified"
	tlsPlaintext  tlsClass = "plaintext"
)

// Acknowledgement names, as posture_waiver log lines and refusals spell them.
const (
	ackTLSUnverified = "TLSUnverifiedAcknowledged"
	ackTLSDisabled   = "TLSDisabledAcknowledged"
)

// tlsFix is the remedy every TLS refusal and development warning names.
const tlsFix = "use sslmode=verify-full with sslrootcert=system or the provider CA, or sslmode=verify-ca with sslrootcert where the certificate does not name the host"

// tlsClass is how a connection target protects its transport.
type tlsClass string

// connTarget is one host pgx may dial: the primary ConnConfig or one of its
// Fallbacks.
type connTarget struct {
	host  string
	port  uint16
	class tlsClass
}

func (t connTarget) address() string {
	return net.JoinHostPort(t.host, strconv.Itoa(int(t.port)))
}

// checkPosture enforces RuntimeMode over pgx's own parse of the DSN, so PG*
// environment defaults, ~/.postgresql/root.crt and multi-host fallbacks are
// judged exactly as pgx will dial them. The zero mode returns nil and logs
// nothing. Production collects every refusal; development logs one WARN per
// condition instead. A unix-socket target logs one INFO line in either mode.
func (cfg Config) checkPosture(conn *pgconn.Config) error {
	if cfg.RuntimeMode == "" {
		return nil
	}
	if err := environment.ValidateMode(cfg.RuntimeMode); err != nil {
		return fmt.Errorf("postgres: RuntimeMode: %w", err)
	}
	production := cfg.RuntimeMode.IsProduction()
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	var refusals []error
	if sources := cfg.queryLoggingSources(); len(sources) > 0 {
		source := slog.String("source", strings.Join(sources, ","))
		switch {
		case production:
			refusals = append(refusals, fmt.Errorf("%w: %s; unset it", ErrQueryLoggingRefused, strings.Join(sources, " and ")))
		case cfg.LogQueries && cfg.LogQueryArgs:
			logger.Warn("postgres: query logging is on and logs bind arguments verbatim; production refuses it", source)
		case cfg.LogQueries || wrapsLoggingTracer(cfg.Tracer):
			logger.Warn("postgres: query logging is on; production refuses it", source)
		default:
			logger.Warn("postgres: LogQueryArgs is inert without LogQueries; production refuses it", source)
		}
	}

	targets := connTargets(conn)
	for _, t := range targets {
		if t.class == tlsLocal {
			logger.Info("postgres: connection target is a unix socket; TLS does not apply",
				slog.String("tls", string(tlsLocal)), slog.String("target", t.host))
		}
	}
	if weak, ok := weakestTarget(targets); ok {
		switch waiver := cfg.tlsWaiver(weak.class); {
		case !production:
			logger.Warn("postgres: connection target does not verify the server; production refuses it",
				slog.String("tls", string(weak.class)), slog.String("target", weak.address()), slog.String("fix", tlsFix))
		case waiver != "":
			logger.Warn("postgres: production TLS requirement waived by acknowledgement",
				slog.String("posture_waiver", waiver), slog.String("tls", string(weak.class)), slog.String("target", weak.address()))
		default:
			refusals = append(refusals, cfg.tlsRefusal(weak))
		}
	}
	return errors.Join(refusals...)
}

// queryLoggingSources names each query-logging setting, with its env variable
// where it has one.
func (cfg Config) queryLoggingSources() []string {
	var sources []string
	if cfg.LogQueries {
		sources = append(sources, "Config.LogQueries (DB_LOG_QUERIES)")
	}
	if cfg.LogQueryArgs {
		sources = append(sources, "Config.LogQueryArgs (DB_LOG_QUERY_ARGS)")
	}
	if wrapsLoggingTracer(cfg.Tracer) {
		sources = append(sources, "Config.Tracer (a *LoggingQueryTracer)")
	}
	return sources
}

// tlsWaiver names the acknowledgement that admits class in production, or ""
// when none is set.
func (cfg Config) tlsWaiver(class tlsClass) string {
	switch {
	case class == tlsUnverified && cfg.TLSUnverifiedAcknowledged:
		return ackTLSUnverified
	case cfg.TLSDisabledAcknowledged:
		return ackTLSDisabled
	}
	return ""
}

// tlsRefusal wraps ErrTLSRequired with the class, the target, the redacted
// connection string and the fix.
func (cfg Config) tlsRefusal(t connTarget) error {
	reason, waiver := "no TLS: sslmode=disable, or the plaintext fallback of sslmode=prefer or allow", ackTLSDisabled
	if t.class == tlsUnverified {
		reason, waiver = "TLS without server verification: sslmode=require with no sslrootcert", ackTLSUnverified
	}
	return fmt.Errorf("%w: target %s of %s is %s (%s); %s, or set Config.%s",
		ErrTLSRequired, t.address(), cfg.Redacted(), t.class, reason, tlsFix, waiver)
}

// connTargets lists every target in pgx's dial order: the primary, then each
// fallback.
func connTargets(conn *pgconn.Config) []connTarget {
	targets := []connTarget{{conn.Host, conn.Port, classifyTarget(conn.Host, conn.Port, conn.TLSConfig)}}
	for _, fb := range conn.Fallbacks {
		if fb != nil {
			targets = append(targets, connTarget{fb.Host, fb.Port, classifyTarget(fb.Host, fb.Port, fb.TLSConfig)})
		}
	}
	return targets
}

// classifyTarget reads one target's class off pgx's parsed TLS settings. pgx
// never wraps a unix socket in TLS. It implements sslmode=verify-ca, and
// require with an sslrootcert, as InsecureSkipVerify plus a
// VerifyPeerCertificate chain check (pgconn/config.go), so a set callback is
// verification; skip-verify without one is the bare sslmode=require.
func classifyTarget(host string, port uint16, tlsConfig *tls.Config) tlsClass {
	if network, _ := pgconn.NetworkAddress(host, port); network == "unix" {
		return tlsLocal
	}
	switch {
	case tlsConfig == nil:
		return tlsPlaintext
	case !tlsConfig.InsecureSkipVerify || tlsConfig.VerifyPeerCertificate != nil:
		return tlsVerified
	default:
		return tlsUnverified
	}
}

// weakestTarget returns the first plaintext target, else the first unverified
// one; ok is false when every target is verified or local.
func weakestTarget(targets []connTarget) (weakest connTarget, ok bool) {
	for _, t := range targets {
		switch t.class {
		case tlsPlaintext:
			return t, true
		case tlsUnverified:
			if !ok {
				weakest, ok = t, true
			}
		}
	}
	return weakest, ok
}

// wrapsLoggingTracer reports whether t is a *LoggingQueryTracer, whatever its
// LogArgs, or a *MultiQueryTracer holding one at any depth.
func wrapsLoggingTracer(t jackpgx.QueryTracer) bool {
	switch t := t.(type) {
	case *LoggingQueryTracer:
		return true
	case *MultiQueryTracer:
		if t == nil {
			return false
		}
		for _, inner := range t.Tracers {
			if wrapsLoggingTracer(inner) {
				return true
			}
		}
	}
	return false
}

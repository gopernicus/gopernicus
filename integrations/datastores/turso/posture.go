package turso

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// Production posture errors. Open returns them, wrapped with the specific
// reason, only under a production Config.RuntimeMode; match them with
// errors.Is. When both apply, Open joins them so one boot names every refusal.
var (
	// ErrTLSRequired refuses a hosted URL that does not use TLS: a scheme other
	// than libsql, https or wss, or a libsql URL whose tls query parameter is
	// 0, which the pinned driver rewrites to http. The wrapped text names the
	// redacted target. A "file:" URL is exempt.
	ErrTLSRequired = errors.New("turso: production requires TLS to a hosted database")

	// ErrQueryLoggingRefused refuses Config.LogQueries or Config.LogQueryArgs
	// (even alone): query logging is development tooling. The wrapped text
	// names each source and its env variable. No acknowledgement admits it.
	ErrQueryLoggingRefused = errors.New("turso: production refuses query logging")
)

// ackTLSDisabled is the acknowledgement as posture_waiver log lines and
// refusals spell it.
const ackTLSDisabled = "TLSDisabledAcknowledged"

// checkPosture enforces RuntimeMode over the normalized dsn. It runs in Open
// rather than in dsn(), which Redacted also calls. The zero mode returns nil
// and logs nothing. Production collects every refusal; development logs one
// WARN per condition instead.
func (cfg Config) checkPosture(dsn string) error {
	if cfg.RuntimeMode == "" {
		return nil
	}
	if err := environment.ValidateMode(cfg.RuntimeMode); err != nil {
		return fmt.Errorf("turso: RuntimeMode: %w", err)
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
			logger.Warn("turso: query logging is on and logs bind arguments verbatim; production refuses it", source)
		case cfg.LogQueries:
			logger.Warn("turso: query logging is on; production refuses it", source)
		default:
			logger.Warn("turso: LogQueryArgs is inert without LogQueries; production refuses it", source)
		}
	}

	reason := plaintextReason(dsn)
	if reason == "" {
		return errors.Join(refusals...)
	}
	switch {
	case !production:
		logger.Warn("turso: database URL does not use TLS; production refuses it",
			slog.String("reason", reason), slog.String("target", cfg.Redacted()))
	case cfg.TLSDisabledAcknowledged:
		logger.Warn("turso: production TLS requirement waived by acknowledgement",
			slog.String("posture_waiver", ackTLSDisabled), slog.String("reason", reason), slog.String("target", cfg.Redacted()))
	default:
		refusals = append(refusals, fmt.Errorf("%w: %s %s; use libsql://, https:// or wss://, or set Config.%s",
			ErrTLSRequired, cfg.Redacted(), reason, ackTLSDisabled))
	}
	return errors.Join(refusals...)
}

// queryLoggingSources names each query-logging setting with its env variable.
func (cfg Config) queryLoggingSources() []string {
	var sources []string
	if cfg.LogQueries {
		sources = append(sources, "Config.LogQueries (DB_LOG_QUERIES)")
	}
	if cfg.LogQueryArgs {
		sources = append(sources, "Config.LogQueryArgs (DB_LOG_QUERY_ARGS)")
	}
	return sources
}

// plaintextReason explains why dsn reaches a hosted database without TLS, or
// returns "" when it uses TLS or names a local file. The libsql rule mirrors
// the pinned driver, which reads the first tls value and sends tls=0 as http.
func plaintextReason(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "does not parse"
	}
	switch u.Scheme {
	case localFileScheme, "https", "wss":
		return ""
	case "libsql":
		if u.Query().Get("tls") == "0" {
			return "uses libsql with tls=0, which the driver sends as http"
		}
		return ""
	case "":
		return "has no scheme"
	default:
		return fmt.Sprintf("uses the %s scheme", u.Scheme)
	}
}

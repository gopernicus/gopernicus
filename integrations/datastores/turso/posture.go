package turso

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// ErrTLSRequired refuses, under a production Config.RuntimeMode, a hosted URL
// that does not use TLS: a scheme other than libsql, https or wss, or a libsql
// URL whose tls query parameter is 0, which the pinned driver rewrites to
// http. The wrapped text names the redacted target. A "file:" URL is exempt.
var ErrTLSRequired = errors.New("turso: production requires TLS to a hosted database")

// ackTLSDisabled is the acknowledgement as posture_waiver log lines and
// refusals spell it.
const ackTLSDisabled = "TLSDisabledAcknowledged"

// checkPosture enforces RuntimeMode over the normalized dsn. It runs in Open
// rather than in dsn(), which Redacted also calls. The zero mode returns nil
// and logs nothing.
func (cfg Config) checkPosture(dsn string) error {
	if cfg.RuntimeMode == "" {
		return nil
	}
	if err := environment.ValidateMode(cfg.RuntimeMode); err != nil {
		return fmt.Errorf("turso: RuntimeMode: %w", err)
	}
	reason := plaintextReason(dsn)
	if reason == "" {
		return nil
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	switch {
	case !cfg.RuntimeMode.IsProduction():
		logger.Warn("turso: database URL does not use TLS; production refuses it",
			slog.String("reason", reason), slog.String("target", cfg.Redacted()))
	case cfg.TLSDisabledAcknowledged:
		logger.Warn("turso: production TLS requirement waived by acknowledgement",
			slog.String("posture_waiver", ackTLSDisabled), slog.String("reason", reason), slog.String("target", cfg.Redacted()))
	default:
		return fmt.Errorf("%w: %s %s; use libsql://, https:// or wss://, or set Config.%s",
			ErrTLSRequired, cfg.Redacted(), reason, ackTLSDisabled)
	}
	return nil
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

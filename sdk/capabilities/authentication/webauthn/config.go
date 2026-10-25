package webauthn

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// MaxTimeout is the longest ceremony, and what a zero Config.Timeout selects.
const MaxTimeout = 5 * time.Minute

// Config configures a RelyingParty. Implementations run Validate before
// anything else, so an invalid Config fails boot in every mode.
type Config struct {
	// RPID is the relying party ID, the domain credentials are scoped to
	// ("example.com", "localhost"): lowercase DNS labels with no scheme, port,
	// path or trailing dot, and not an IP address. It is permanent for a
	// deployment: changing it orphans every registered credential.
	RPID string
	// RPName is the relying party name an authenticator may display.
	RPName string
	// Origins admits the clientDataJSON origin of every ceremony. Every exact
	// host and namespace suffix must equal RPID or sit under it.
	Origins web.OriginPolicy
	// Timeout bounds a ceremony. Zero selects MaxTimeout; a negative value or
	// one above MaxTimeout is refused.
	Timeout time.Duration
}

// Validate reports the first problem with c. Every error matches
// sdk.ErrInvalidInput; an origin host outside RPID also matches
// ErrOriginOutsideRPID and names the host.
func (c Config) Validate() error {
	if err := validateRPID(c.RPID); err != nil {
		return err
	}
	if strings.TrimSpace(c.RPName) == "" {
		return fmt.Errorf("webauthn: RPName is required: %w", sdk.ErrInvalidInput)
	}
	domains := c.Origins.Domains()
	if len(domains) == 0 {
		return fmt.Errorf("webauthn: Origins admits no origin: %w", sdk.ErrInvalidInput)
	}
	for _, domain := range domains {
		if domain != c.RPID && !strings.HasSuffix(domain, "."+c.RPID) {
			return fmt.Errorf("webauthn: origin host %q is outside RPID %q: %w", domain, c.RPID, ErrOriginOutsideRPID)
		}
	}
	if c.Timeout < 0 || c.Timeout > MaxTimeout {
		return fmt.Errorf("webauthn: Timeout %v is outside 0 to %v: %w", c.Timeout, MaxTimeout, sdk.ErrInvalidInput)
	}
	return nil
}

func validateRPID(rpID string) error {
	invalid := func(why string) error {
		return fmt.Errorf("webauthn: RPID %q %s: %w", rpID, why, sdk.ErrInvalidInput)
	}
	if rpID == "" {
		return fmt.Errorf("webauthn: RPID is required: %w", sdk.ErrInvalidInput)
	}
	if len(rpID) > 253 {
		return invalid("is longer than 253 bytes")
	}
	labels := strings.Split(rpID, ".")
	for _, label := range labels {
		if !validLabel(label) {
			return invalid("must be lowercase DNS labels (letters, digits, inner hyphens) with no scheme, port, path or trailing dot")
		}
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return invalid("must be a domain, not an IP address")
	}
	return nil
}

// validLabel reports whether s is one lowercase LDH DNS label of 1-63 bytes,
// not starting or ending with a hyphen.
func validLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

package web

import (
	"strconv"
	"time"
)

// Header values SecurityHeaders writes. Every one of them is a
// DefaultHeadersMiddleware default, so a handler may still Set or Del it on its
// own writer.
const (
	securityHSTSDefaultMaxAge    = 365 * 24 * time.Hour
	securityContentTypeOptions   = "nosniff"
	securityReferrerPolicy       = "strict-origin-when-cross-origin"
	securityFrameOptionsDeny     = "DENY"
	securityPermissionsPolicy    = "camera=(), microphone=(), geolocation=()"
	securityHSTSIncludeSubDomain = "; includeSubDomains"
	securityHSTSPreload          = "; preload"
)

// SecurityHeadersConfig adjusts the SecurityHeaders preset for a host whose
// deployment needs a different transport or framing posture.
type SecurityHeadersConfig struct {
	// HSTSMaxAge is the Strict-Transport-Security max-age, truncated to whole
	// seconds. Zero selects one year. A negative value emits max-age=0, which
	// tells browsers to forget an earlier pin: the recovery after HSTS was sent
	// by mistake. DisableHSTS stops sending the header but does not un-pin a
	// browser that already saw it.
	HSTSMaxAge time.Duration

	// HSTSPreload appends "preload". Preload-list submission is a near-permanent
	// commitment made out of band, so the preset never asks for it.
	HSTSPreload bool

	// HSTSExcludeSubDomains drops "includeSubDomains", for a host on an apex
	// whose sibling subdomains still serve plain HTTP.
	HSTSExcludeSubDomains bool

	// DisableHSTS omits Strict-Transport-Security entirely.
	DisableHSTS bool

	// FrameOptions is the X-Frame-Options value. Empty omits the header, for a
	// host whose pages are embedded and which sets CSP frame-ancestors itself.
	// SecurityHeaders uses "DENY".
	FrameOptions string
}

// SecurityHeaders is a preset of DefaultHeadersMiddleware, like NoStore, that
// writes the browser-facing transport and framing defaults before the handler
// runs:
//
//	Strict-Transport-Security: max-age=31536000; includeSubDomains
//	X-Content-Type-Options: nosniff
//	Referrer-Policy: strict-origin-when-cross-origin
//	X-Frame-Options: DENY
//	Permissions-Policy: camera=(), microphone=(), geolocation=()
//
// A handler may Set or Del any of the five on its own writer; a pocket that
// owns a stricter page policy keeps it. No Content-Security-Policy is written:
// a CSP depends on each page's scripts and styles, so pages own it.
//
// TLS usually terminates at the edge, but HSTS is the application's to send.
// Browsers ignore Strict-Transport-Security received over plain HTTP, so local
// development over http://localhost is unaffected. Mount it outside
// cacher.Pages: Strict-Transport-Security is not a stored page header, so set
// inside, it makes every page bypass the cache.
//
// It is SecurityHeadersWithConfig(SecurityHeadersConfig{FrameOptions: "DENY"}).
func SecurityHeaders() Middleware {
	return SecurityHeadersWithConfig(SecurityHeadersConfig{FrameOptions: securityFrameOptionsDeny})
}

// SecurityHeadersWithConfig is SecurityHeaders with the HSTS and framing
// posture adjusted by cfg. The zero config writes every SecurityHeaders header
// except X-Frame-Options; set FrameOptions to keep it.
func SecurityHeadersWithConfig(cfg SecurityHeadersConfig) Middleware {
	headers := map[string]string{
		"X-Content-Type-Options": securityContentTypeOptions,
		"Referrer-Policy":        securityReferrerPolicy,
		"Permissions-Policy":     securityPermissionsPolicy,
	}
	if cfg.FrameOptions != "" {
		headers["X-Frame-Options"] = cfg.FrameOptions
	}
	if !cfg.DisableHSTS {
		headers["Strict-Transport-Security"] = hstsValue(cfg)
	}
	return DefaultHeadersMiddleware(headers)
}

// hstsValue renders the Strict-Transport-Security value for cfg.
func hstsValue(cfg SecurityHeadersConfig) string {
	maxAge := cfg.HSTSMaxAge
	switch {
	case maxAge == 0:
		maxAge = securityHSTSDefaultMaxAge
	case maxAge < 0:
		maxAge = 0
	}
	value := "max-age=" + strconv.FormatInt(int64(maxAge/time.Second), 10)
	if !cfg.HSTSExcludeSubDomains {
		value += securityHSTSIncludeSubDomain
	}
	if cfg.HSTSPreload {
		value += securityHSTSPreload
	}
	return value
}

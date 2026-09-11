package cacher

import (
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// pageTTL deliberately supports a small cache policy. Unknown directives or
// metadata that need validation/variation logic cause a bypass, not a guess.
func pageTTL(header http.Header, ttl time.Duration) (time.Duration, bool) {
	for _, name := range []string{"Vary", "Set-Cookie", "Content-Encoding", "Content-Length", "Content-Range", "Content-Disposition", "Trailer", "Date", "Expires", "Age", "Pragma", "Connection", "Transfer-Encoding", "Upgrade"} {
		if len(header.Values(name)) != 0 {
			return 0, false
		}
	}
	for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		for _, value := range header.Values(name) {
			if strings.Contains(strings.ToLower(value), "nonce-") {
				return 0, false
			}
		}
	}
	if contentType := header.Get("Content-Type"); contentType != "" && !htmlContentType(contentType) {
		return 0, false
	}
	seen := make(map[string]bool)
	for _, line := range header.Values("Cache-Control") {
		for _, directive := range strings.Split(line, ",") {
			name, value, hasValue := strings.Cut(strings.TrimSpace(directive), "=")
			name = strings.ToLower(strings.TrimSpace(name))
			if seen[name] {
				return 0, false
			}
			seen[name] = true
			switch name {
			case "public", "must-revalidate", "proxy-revalidate", "no-transform", "immutable":
				if hasValue {
					return 0, false
				}
			case "max-age", "s-maxage":
				if !hasValue {
					return 0, false
				}
				value = strings.TrimSpace(value)
				if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") && len(value) >= 2 {
					value = value[1 : len(value)-1]
				}
				seconds, err := strconv.ParseUint(value, 10, 63)
				if err != nil || seconds == 0 {
					return 0, false
				}
				// Compare seconds first to avoid overflowing a duration for large ages.
				if seconds <= uint64(ttl/time.Second) {
					candidate := time.Duration(seconds) * time.Second
					if candidate < ttl {
						ttl = candidate
					}
				}
			default:
				return 0, false
			}
		}
	}
	return ttl, ttl > 0
}

func htmlContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "text/html"
}

func pageHeaderAllowed(name string) bool {
	switch name {
	case "Content-Type", "Content-Language", "Cache-Control", "Etag", "Last-Modified", "Content-Security-Policy", "Content-Security-Policy-Report-Only", "Referrer-Policy", "X-Content-Type-Options", "X-Frame-Options", "Permissions-Policy", "Cross-Origin-Opener-Policy", "Cross-Origin-Embedder-Policy", "Cross-Origin-Resource-Policy", "Link":
		return true
	default:
		return false
	}
}

func pageMetadata(header, outer http.Header) (http.Header, bool) {
	out := make(http.Header)
	size := 0
	for name, values := range header {
		// These are recomputed on every request or by net/http. Header values
		// inherited unchanged from outer middleware also stay owned by that layer.
		switch name {
		case "Content-Length", "Date", "Server", "X-Cache", "X-Request-Id", "Traceparent", "Tracestate", "Server-Timing":
			continue
		}
		if !pageHeaderAllowed(name) && slices.Equal(values, outer.Values(name)) {
			continue
		}
		if !pageHeaderAllowed(name) {
			return nil, false
		}
		out[name] = append([]string(nil), values...)
		size += len(name)
		for _, value := range values {
			size += len(value)
		}
		if size > 32<<10 {
			return nil, false
		}
	}
	return out, true
}

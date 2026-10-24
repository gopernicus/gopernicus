package logging

import (
	"context"
	"log/slog"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// DefaultRedactKeys are the attribute keys New redacts in every mode. A host
// adds keys with Options.RedactKeys (LOG_REDACT_KEYS); there is no off switch.
// A host that must log a value under one of these keys renames the attribute.
var DefaultRedactKeys = []string{
	"password", "passwd", "secret", "token", "access_token", "refresh_token",
	"api_key", "apikey", "x_api_key", "authorization", "cookie", "set-cookie",
	"private_key", "client_secret", "credentials_json", "auth_token", "dsn",
}

// RedactHandler replaces the value of every attribute whose key matches its
// key list with sdk.Redacted before passing the record to an inner handler.
// Keys match exactly after lower-casing and treating '-' as '_': there is no
// substring or prefix match (refresh_token_hash does not match token), so a
// host chooses its attribute spelling. Header-derived keys are matched in
// their '_' form: X-API-Key, x-api-key and x_api_key all match x_api_key. A
// matching key replaces its value whatever its kind, a group included; the
// handler recurses only into groups whose key does not match. LogValuer values
// are resolved first, and attributes bound with WithAttrs are redacted too.
//
// It sees attribute keys only: a secret inside an slog.Any map or struct, or
// inside free text such as an error message, passes through. sdk.Secret
// renders as sdk.Redacted under any key.
type RedactHandler struct {
	inner slog.Handler
	keys  map[string]struct{}
}

// NewRedactHandler wraps inner, redacting exactly keys (it does not add
// DefaultRedactKeys; pass them when the defaults should apply).
func NewRedactHandler(inner slog.Handler, keys []string) *RedactHandler {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key = normalizeRedactKey(key); key != "" {
			set[key] = struct{}{}
		}
	}
	return &RedactHandler{inner: inner, keys: set}
}

// Enabled delegates to the inner handler.
func (h *RedactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle redacts the record's attributes, then delegates to the inner handler.
func (h *RedactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	attrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, h.redact(a))
		return true
	})
	out.AddAttrs(attrs...)
	return h.inner.Handle(ctx, out)
}

// WithAttrs redacts a copy of attrs and binds it on the inner handler.
func (h *RedactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = h.redact(a)
	}
	return &RedactHandler{inner: h.inner.WithAttrs(redacted), keys: h.keys}
}

// WithGroup opens the group on the inner handler; later attributes are still
// matched by their own keys.
func (h *RedactHandler) WithGroup(name string) slog.Handler {
	return &RedactHandler{inner: h.inner.WithGroup(name), keys: h.keys}
}

func (h *RedactHandler) redact(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	if _, ok := h.keys[normalizeRedactKey(a.Key)]; ok {
		return slog.String(a.Key, sdk.Redacted)
	}
	if a.Value.Kind() != slog.KindGroup {
		return a
	}
	group := a.Value.Group()
	redacted := make([]slog.Attr, len(group))
	for i, member := range group {
		redacted[i] = h.redact(member)
	}
	return slog.Attr{Key: a.Key, Value: slog.GroupValue(redacted...)}
}

func normalizeRedactKey(key string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "-", "_")
}

package authentication

import "context"

type contextKey int

const clientInfoKey contextKey = iota

// ClientInfo holds request metadata that the bridge/HTTP layer injects
// into the context for security event logging.
type ClientInfo struct {
	IPAddress string
	UserAgent string
}

// WithClientInfo returns a context carrying the client's IP address and
// User-Agent. Call this in HTTP middleware so that [Authenticator] methods
// can include client info in security events automatically.
func WithClientInfo(ctx context.Context, info ClientInfo) context.Context {
	return context.WithValue(ctx, clientInfoKey, info)
}

// clientInfoFromContext extracts ClientInfo from the context.
// Returns zero value if not set.
func clientInfoFromContext(ctx context.Context) ClientInfo {
	info, _ := ctx.Value(clientInfoKey).(ClientInfo)
	return info
}

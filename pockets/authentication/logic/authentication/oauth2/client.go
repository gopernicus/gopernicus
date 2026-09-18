package oauth2

import "context"

// ClientResolver returns metadata only after the host's explicit client trust
// policy accepts the exact ID. That policy is checked on EVERY call, including
// cache hits, so withdrawing trust also disables refresh and new authorization.
// Network adapters enforce bounded retrieval, SSRF protection, metadata identity
// and redirect validation. The service never fetches client-supplied URLs itself.
type ClientResolver interface {
	Resolve(ctx context.Context, clientID string) (Client, error)
}

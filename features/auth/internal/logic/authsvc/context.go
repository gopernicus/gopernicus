package authsvc

import "context"

// contextKey is an unexported type for the identity value stashed on a request
// context, so no other package can collide with or read the raw key. It lives
// here (not sdk) by design: the only consumers of the identity-in-context value
// are RequireUser (writes) and CurrentUser (reads), both inside features/auth.
// Cross-feature identity is exposed through the exported Service.CurrentUser
// method, never this key (see the auth-feature-design doc, §3).
type contextKey int

const userIDKey contextKey = iota

// withUserID returns a copy of ctx carrying userID.
func withUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// userIDFromContext returns the identity stashed by withUserID, if any. A blank
// value reports ("", false).
func userIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDKey).(string)
	return v, ok && v != ""
}

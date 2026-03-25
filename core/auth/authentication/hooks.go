package authentication

import "context"

// AfterUserCreationHook runs after a user is successfully created.
// This is called after the user record is created in the database.
// Fires for both credential-based registration and OAuth flows.
//
// If the hook returns an error, registration fails — the hook is
// treated as a required step. For best-effort work (analytics,
// non-critical setup), handle errors internally and return nil.
type AfterUserCreationHook func(ctx context.Context, user User) error

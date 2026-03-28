package httpmid

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gopernicus/gopernicus/core/auth/authorization"
)

// PermissionChecker checks authorization permissions.
// The [authorization.Authorizer] satisfies this interface structurally.
type PermissionChecker interface {
	Check(ctx context.Context, req authorization.CheckRequest) (authorization.CheckResult, error)
}

// ---------------------------------------------------------------------------
// Core authorization check — no HTTP response, no logging
// ---------------------------------------------------------------------------

// accessDenialKind describes why authorization was denied.
type accessDenialKind int

const (
	denialNoSubject     accessDenialKind = iota // no authenticated subject in context
	denialEmptyResource                         // resource ID callback returned ""
	denialCheckError                            // PermissionChecker.Check returned an error
	denialForbidden                             // check succeeded but permission denied
)

// accessDenial is returned by checkAccess when authorization fails.
type accessDenial struct {
	kind accessDenialKind
	err  error // underlying error for denialNoSubject and denialCheckError
}

// errorKind maps an access denial to the corresponding ErrorKind.
func (d *accessDenial) errorKind() ErrorKind {
	switch d.kind {
	case denialNoSubject:
		return ErrKindUnauthenticated
	case denialEmptyResource:
		return ErrKindBadRequest
	case denialCheckError:
		return ErrKindInternal
	case denialForbidden:
		return ErrKindForbidden
	default:
		return ErrKindInternal
	}
}

// checkAccess performs the core authorization check without writing any HTTP
// response or logging. Returns (result, nil) when access is allowed, or
// (zero, denial) describing why access was denied.
func checkAccess(
	ctx context.Context,
	authorizer PermissionChecker,
	resourceType, permission, resourceID string,
) (authorization.CheckResult, *accessDenial) {
	subject, err := parseAuthzSubject(ctx)
	if err != nil {
		return authorization.CheckResult{}, &accessDenial{kind: denialNoSubject, err: err}
	}

	if resourceID == "" {
		return authorization.CheckResult{}, &accessDenial{kind: denialEmptyResource}
	}

	result, err := authorizer.Check(ctx, authorization.CheckRequest{
		Subject:    subject,
		Permission: permission,
		Resource:   authorization.Resource{Type: resourceType, ID: resourceID},
	})
	if err != nil {
		return authorization.CheckResult{}, &accessDenial{kind: denialCheckError, err: err}
	}

	if !result.Allowed {
		return authorization.CheckResult{}, &accessDenial{kind: denialForbidden}
	}

	return result, nil
}

// logAccessDenial writes structured log entries for authorization failures.
func logAccessDenial(log *slog.Logger, ctx context.Context, d *accessDenial, permission, resourceType, resourceID string) {
	switch d.kind {
	case denialNoSubject:
		log.ErrorContext(ctx, "authorize: no subject in context", "error", d.err)
	case denialEmptyResource:
		log.ErrorContext(ctx, "authorize: empty resource ID")
	case denialCheckError:
		log.ErrorContext(ctx, "authorize: check failed",
			"error", d.err,
			"subject", GetSubject(ctx),
			"permission", permission,
			"resource", resourceType+":"+resourceID,
		)
	case denialForbidden:
		log.WarnContext(ctx, "authorize: denied",
			"subject", GetSubject(ctx),
			"permission", permission,
			"resource", resourceType+":"+resourceID,
		)
	}
}

// handleAccessAllowed stores the relationship in context when the check result
// includes a reason. Always called on successful authorization.
func handleAccessAllowed(r *http.Request, result authorization.CheckResult, resourceType, resourceID string) *http.Request {
	if relation := ParseRelationFromReason(result.Reason); relation != "" {
		ctx := SetRelationship(r.Context(), RelationshipInfo{
			Relation:     relation,
			ResourceType: resourceType,
			ResourceID:   resourceID,
		})
		r = r.WithContext(ctx)
	}
	return r
}

// ---------------------------------------------------------------------------
// Authorize middleware
// ---------------------------------------------------------------------------

// Authorize returns middleware that checks permission on a specific resource.
// The [ErrorRenderer] controls how errors are presented — pass [JSONErrors]
// for API routes or a custom renderer for HTML routes.
// Must be used AFTER [Authenticate] middleware.
//
// The getResourceID callback extracts the resource ID from the request.
//
//	jsonErrs := httpmid.JSONErrors{}
//	mux.Handle("GET /posts/{id}", Authorize(authorizer, log, jsonErrs, "post", "read",
//	    func(r *http.Request) string { return r.PathValue("id") },
//	)(handler))
func Authorize(
	authorizer PermissionChecker,
	log *slog.Logger,
	errors ErrorRenderer,
	resourceType, permission string,
	getResourceID func(r *http.Request) string,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			resourceID := getResourceID(r)

			result, denial := checkAccess(ctx, authorizer, resourceType, permission, resourceID)
			if denial != nil {
				logAccessDenial(log, ctx, denial, permission, resourceType, resourceID)
				errors.RenderError(w, r, denial.errorKind())
				return
			}

			r = handleAccessAllowed(r, result, resourceType, resourceID)
			next.ServeHTTP(w, r)
		})
	}
}

// AuthorizeParam is a convenience wrapper where the resource ID comes from
// a URL path parameter via [http.Request.PathValue].
//
//	mux.Handle("GET /posts/{id}", AuthorizeParam(authorizer, log, jsonErrs, "post", "read", "id")(handler))
func AuthorizeParam(
	authorizer PermissionChecker,
	log *slog.Logger,
	errors ErrorRenderer,
	resourceType, permission, paramName string,
) func(http.Handler) http.Handler {
	return Authorize(authorizer, log, errors, resourceType, permission,
		func(r *http.Request) string { return r.PathValue(paramName) })
}

// AuthorizeType checks permission on a resource TYPE rather than a specific
// instance. Uses "*" as the resource ID. Useful for list/create operations.
//
//	mux.Handle("GET /users", AuthorizeType(authorizer, log, jsonErrs, "user", "list")(handler))
func AuthorizeType(
	authorizer PermissionChecker,
	log *slog.Logger,
	errors ErrorRenderer,
	resourceType, permission string,
) func(http.Handler) http.Handler {
	return Authorize(authorizer, log, errors, resourceType, permission,
		func(_ *http.Request) string { return "*" })
}

// RequirePlatformAdmin checks that the subject has the platform:main#admin
// permission. Must be used AFTER [Authenticate] middleware.
//
//	mux.Handle("GET /admin/users", RequirePlatformAdmin(authorizer, log, jsonErrs)(handler))
func RequirePlatformAdmin(
	authorizer PermissionChecker,
	log *slog.Logger,
	errors ErrorRenderer,
) func(http.Handler) http.Handler {
	return Authorize(authorizer, log, errors, "platform", "admin",
		func(_ *http.Request) string { return "main" })
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseAuthzSubject extracts an authorization.Subject from context values
// set by the Authenticate middleware.
func parseAuthzSubject(ctx context.Context) (authorization.Subject, error) {
	subjectStr := GetSubject(ctx)
	if subjectStr == "" {
		return authorization.Subject{}, fmt.Errorf("no subject in context")
	}

	parts := strings.SplitN(subjectStr, ":", 2)
	if len(parts) != 2 {
		return authorization.Subject{}, fmt.Errorf("invalid subject format: %s", subjectStr)
	}

	return authorization.Subject{
		Type: parts[0],
		ID:   parts[1],
	}, nil
}

// ParseRelationFromReason extracts the direct relation from a CheckResult.Reason.
// Reason format: "direct:owner" or "through:org->direct:admin".
// Returns the innermost direct relation that granted access.
func ParseRelationFromReason(reason string) string {
	const directPrefix = "direct:"
	idx := strings.LastIndex(reason, directPrefix)
	if idx == -1 {
		return ""
	}
	relation := reason[idx+len(directPrefix):]
	if endIdx := strings.Index(relation, "->"); endIdx != -1 {
		relation = relation[:endIdx]
	}
	return relation
}

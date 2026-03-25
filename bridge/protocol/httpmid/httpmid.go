// Package httpmid provides HTTP middleware and request context helpers.
//
// Middleware functions return standard [http.Handler] wrappers and compose
// naturally with any Go HTTP stack.
//
// Context helpers store and retrieve auth state, client IP, and other
// request-scoped data set by middleware earlier in the chain.
//
//	// Reading auth state in a handler:
//	subject := httpmid.GetSubject(r.Context())   // "user:abc123"
//	userID  := httpmid.GetSubjectID(r.Context()) // "abc123"
package httpmid

import (
	"context"
	"strings"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
)

// ---------------------------------------------------------------------------
// Context keys
// ---------------------------------------------------------------------------

type contextKey int

const (
	ctxSubject contextKey = iota
	ctxSubjectType
	ctxSessionID
	ctxUser
	ctxSession
	ctxClientIP
	ctxRelationship
	ctxTenantID
)

// SubjectType distinguishes user authentication from service account authentication.
type SubjectType int

const (
	SubjectTypeUser           SubjectType = iota
	SubjectTypeServiceAccount
)

// ---------------------------------------------------------------------------
// Subject — set by Authenticate middleware
// ---------------------------------------------------------------------------

// SetSubject stores the authenticated subject in the context.
// Format: "user:{id}" or "service_account:{id}".
func SetSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, ctxSubject, subject)
}

// GetSubject returns the authenticated subject, or "" if not set.
func GetSubject(ctx context.Context) string {
	v, _ := ctx.Value(ctxSubject).(string)
	return v
}

// GetSubjectID returns just the ID portion of the subject string.
// For "user:abc123" it returns "abc123".
func GetSubjectID(ctx context.Context) string {
	s := GetSubject(ctx)
	for i := range s {
		if s[i] == ':' {
			return s[i+1:]
		}
	}
	return s
}

// SubjectInfo contains the parsed type and ID of an authenticated subject.
type SubjectInfo struct {
	Type string // "user", "service_account"
	ID   string
}

// GetSubjectInfo parses the authenticated subject string into type and ID.
// For "user:abc123" it returns SubjectInfo{Type: "user", ID: "abc123"}.
// Returns zero value if the subject is not set or cannot be parsed.
func GetSubjectInfo(ctx context.Context) SubjectInfo {
	s := GetSubject(ctx)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return SubjectInfo{Type: s[:i], ID: s[i+1:]}
	}
	return SubjectInfo{ID: s}
}

// MustGetSubject returns the subject or panics if not set.
// The panic is caught by the [Panics] middleware and converted to a 500 error.
func MustGetSubject(ctx context.Context) string {
	s := GetSubject(ctx)
	if s == "" {
		panic("httpmid: subject not set in context — is Authenticate middleware applied?")
	}
	return s
}

// SetSubjectType stores whether this is a user or service account.
func SetSubjectType(ctx context.Context, st SubjectType) context.Context {
	return context.WithValue(ctx, ctxSubjectType, st)
}

// GetSubjectType returns the subject type from context.
func GetSubjectType(ctx context.Context) SubjectType {
	v, _ := ctx.Value(ctxSubjectType).(SubjectType)
	return v
}

// IsUserAuth returns true if the subject is a user (not a service account).
func IsUserAuth(ctx context.Context) bool {
	return GetSubjectType(ctx) == SubjectTypeUser
}

// IsServiceAccountAuth returns true if the subject is a service account.
func IsServiceAccountAuth(ctx context.Context) bool {
	return GetSubjectType(ctx) == SubjectTypeServiceAccount
}

// ---------------------------------------------------------------------------
// Session ID — set by Authenticate middleware with WithUserSession
// ---------------------------------------------------------------------------

// SetSessionID stores the authenticated session ID in the context.
func SetSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ctxSessionID, sessionID)
}

// GetSessionID returns the session ID, or "" if not set.
// Only populated when [WithUserSession] is used.
func GetSessionID(ctx context.Context) string {
	v, _ := ctx.Value(ctxSessionID).(string)
	return v
}

// ---------------------------------------------------------------------------
// User / Session — set by Authenticate middleware with WithUserSession
// ---------------------------------------------------------------------------

// SetUser stores the authenticated user in the context.
func SetUser(ctx context.Context, user *authentication.User) context.Context {
	return context.WithValue(ctx, ctxUser, user)
}

// GetUser returns the authenticated user, or nil if not set.
// Only populated when [WithUserSession] is used.
func GetUser(ctx context.Context) *authentication.User {
	v, _ := ctx.Value(ctxUser).(*authentication.User)
	return v
}

// SetSession stores the authenticated session in the context.
func SetSession(ctx context.Context, session *authentication.Session) context.Context {
	return context.WithValue(ctx, ctxSession, session)
}

// GetSession returns the authenticated session, or nil if not set.
// Only populated when [WithUserSession] is used.
func GetSession(ctx context.Context) *authentication.Session {
	v, _ := ctx.Value(ctxSession).(*authentication.Session)
	return v
}

// ---------------------------------------------------------------------------
// Client IP — set by TrustProxies middleware
// ---------------------------------------------------------------------------

// SetClientIP stores the resolved client IP in the context.
func SetClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxClientIP, ip)
}

// GetClientIP returns the client IP, or "" if not set.
func GetClientIP(ctx context.Context) string {
	v, _ := ctx.Value(ctxClientIP).(string)
	return v
}

// ---------------------------------------------------------------------------
// Relationship — set by Authorize middleware with WithRelationship
// ---------------------------------------------------------------------------

// RelationshipInfo describes the relationship that granted access.
type RelationshipInfo struct {
	Relation     string
	ResourceType string
	ResourceID   string
}

// SetRelationship stores relationship info in the context.
func SetRelationship(ctx context.Context, info RelationshipInfo) context.Context {
	return context.WithValue(ctx, ctxRelationship, info)
}

// GetRelationship returns the relationship info, or (zero, false) if not set.
func GetRelationship(ctx context.Context) (RelationshipInfo, bool) {
	v, ok := ctx.Value(ctxRelationship).(RelationshipInfo)
	return v, ok
}

// ---------------------------------------------------------------------------
// Tenant ID — set by ExtractTenantID or InjectDefaultTenant middleware
// ---------------------------------------------------------------------------

// SetTenantID stores the tenant ID in the context.
func SetTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ctxTenantID, tenantID)
}

// GetTenantID returns the tenant ID, or "" if not set.
func GetTenantID(ctx context.Context) string {
	v, _ := ctx.Value(ctxTenantID).(string)
	return v
}

// MustGetTenantID returns the tenant ID or panics if not set.
func MustGetTenantID(ctx context.Context) string {
	id := GetTenantID(ctx)
	if id == "" {
		panic("httpmid: tenant ID not set in context — is tenant middleware applied?")
	}
	return id
}

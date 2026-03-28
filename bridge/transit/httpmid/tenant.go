package httpmid

import (
	"log/slog"
	"net/http"
)

// DefaultTenantID is used for single-tenant deployments.
const DefaultTenantID = "default"

// ExtractTenantID reads a tenant ID from the URL path parameter and stores
// it in context via [SetTenantID]. Use as middleware on tenant-scoped routes.
//
//	group := handler.Group("/tenants/{tenant_id}",
//	    httpmid.Authenticate(auth, log),
//	    httpmid.ExtractTenantID(log, "tenant_id"),
//	    httpmid.AuthorizeParam(authz, log, "tenant", "read", "tenant_id"),
//	)
func ExtractTenantID(log *slog.Logger, paramName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID := r.PathValue(paramName)
			if tenantID == "" {
				log.WarnContext(r.Context(), "extract_tenant_id: missing param", slog.String("param", paramName))
				// Continue without tenant — authorization will catch this.
				next.ServeHTTP(w, r)
				return
			}

			ctx := SetTenantID(r.Context(), tenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// InjectDefaultTenant sets the tenant ID to [DefaultTenantID].
// Use for single-tenant deployments or routes that use a fixed tenant.
//
//	group := handler.Group("/api",
//	    httpmid.Authenticate(auth, log),
//	    httpmid.InjectDefaultTenant(log),
//	)
func InjectDefaultTenant(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := SetTenantID(r.Context(), DefaultTenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

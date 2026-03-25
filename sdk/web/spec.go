package web

// RouteSpec describes an HTTP route for OpenAPI documentation.
// Bridges export these via OpenAPISpec() methods; the WebHandler
// collects them and serves a complete OpenAPI 3.1 spec.
type RouteSpec struct {
	// Route identity.
	Method string // "GET", "POST", "PUT", "PATCH", "DELETE"
	Path   string // "/users/:user_id" (colon params converted to {param} in spec)

	// Documentation.
	Summary     string   // Short operation summary.
	Description string   // Longer description (optional).
	Tags        []string // OpenAPI tags for grouping.

	// Security.
	Authenticated bool // Requires bearer auth.
	Internal      bool // x-internal: true — filtered by doc tooling.
	Deprecated    bool // Marks operation as deprecated.

	// Request body — nil for GET/DELETE. Pass a zero-value struct
	// instance (e.g., CreateUserRequest{}) for reflection.
	RequestBody any

	// Response body — pass a zero-value struct instance for the
	// success response entity. For list endpoints, pass the single
	// entity type and set Paginated = true.
	ResponseBody any

	// Pagination — adds standard cursor/limit/order query params
	// and wraps response in {data: [], pagination: {}}.
	Paginated bool

	// Additional query parameters beyond pagination.
	SpecQueryParams []SpecQueryParam

	// StatusCode overrides the default (200 GET, 201 POST, 204 DELETE).
	StatusCode int
}

// SpecQueryParam describes a query string parameter.
type SpecQueryParam struct {
	Name        string // "status", "search", etc.
	Description string
	Required    bool
	Schema      ParamSchema
}

// ParamSchema describes the type of a query parameter.
type ParamSchema struct {
	Type   string // "string", "integer", "boolean"
	Format string // "date-time", "email", etc. (optional)
	Enum   []string
}

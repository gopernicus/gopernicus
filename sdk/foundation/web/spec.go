package web

// RouteSpec describes an HTTP route for OpenAPI documentation. It is the unit
// callers pass to BuildOpenAPISpec / WebHandler.ServeOpenAPI; the generator is
// an app-driven spec builder — it never introspects the handler's route table.
type RouteSpec struct {
	// Route identity.
	Method string // "GET", "POST", "PUT", "PATCH", "DELETE".
	Path   string // "/users/{user_id}" — ServeMux-native braces are canonical;
	// legacy ":user_id" colon params are converted to "{param}" as a shim.

	// Documentation.
	Summary     string   // Short operation summary.
	Description string   // Longer description (optional).
	Tags        []string // OpenAPI tags for grouping.

	// Security.
	Authenticated bool // Requires bearer auth; adds a 401 response.
	Internal      bool // x-internal: true — filtered by doc tooling.
	Deprecated    bool // Marks the operation as deprecated.

	// RequestBody is nil for GET/DELETE. Pass a zero-value struct instance
	// (e.g., CreateUserRequest{}) for reflection.
	RequestBody any

	// ResponseBody is a zero-value struct instance for the success response
	// entity. For list endpoints, pass the single entity type and set
	// Paginated = true.
	ResponseBody any

	// Paginated adds the standard cursor/limit/order query params and wraps the
	// response in a cursor page envelope (see crud.Page).
	Paginated bool

	// SpecQueryParams are additional query parameters beyond pagination.
	SpecQueryParams []SpecQueryParam

	// StatusCode overrides the default (200 GET/PUT/PATCH, 201 POST, 204 DELETE).
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
	Type   string // "string", "integer", "boolean".
	Format string // "date-time", "email", etc. (optional).
	Enum   []string
}

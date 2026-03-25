package web

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"
)

// OpenAPIInfo holds metadata for the generated spec.
type OpenAPIInfo struct {
	Title   string
	Version string
}

// ServeOpenAPI registers a GET handler that serves a complete OpenAPI 3.1.0
// JSON spec built from the provided route specs.
//
//	webHandler.ServeOpenAPI("/openapi.json", OpenAPIInfo{Title: "My API", Version: "1.0.0"},
//	    authBridges.OpenAPISpec(),
//	    rebacBridges.OpenAPISpec(),
//	)
func (h *WebHandler) ServeOpenAPI(path string, info OpenAPIInfo, specs ...[]RouteSpec) {
	spec := BuildOpenAPISpec(info, specs...)
	data, _ := json.MarshalIndent(spec, "", "  ")

	h.HandleRaw("GET "+path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}))
}

// BuildOpenAPISpec constructs an OpenAPI 3.1.0 document from route specs.
func BuildOpenAPISpec(info OpenAPIInfo, specs ...[]RouteSpec) map[string]any {
	paths := make(map[string]map[string]any)
	schemas := make(map[string]map[string]any)

	// Collect all routes.
	var allRoutes []RouteSpec
	for _, s := range specs {
		allRoutes = append(allRoutes, s...)
	}

	// Sort by path then method for deterministic output.
	sort.Slice(allRoutes, func(i, j int) bool {
		if allRoutes[i].Path == allRoutes[j].Path {
			return allRoutes[i].Method < allRoutes[j].Method
		}
		return allRoutes[i].Path < allRoutes[j].Path
	})

	for _, route := range allRoutes {
		oaPath := colonToOpenAPIPath(route.Path)
		if paths[oaPath] == nil {
			paths[oaPath] = make(map[string]any)
		}

		op := buildOperation(route, schemas)
		paths[oaPath][strings.ToLower(route.Method)] = op
	}

	// Add standard schemas.
	schemas["Pagination"] = paginationSchema()
	schemas["Error"] = errorSchema()

	// Collect tags.
	tagSet := make(map[string]bool)
	for _, r := range allRoutes {
		for _, t := range r.Tags {
			tagSet[t] = true
		}
	}
	var tags []map[string]string
	tagNames := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tagNames = append(tagNames, t)
	}
	sort.Strings(tagNames)
	for _, t := range tagNames {
		tags = append(tags, map[string]string{"name": t})
	}

	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   info.Title,
			"version": info.Version,
		},
		"paths": paths,
		"components": map[string]any{
			"schemas": schemas,
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
				},
			},
		},
	}

	if len(tags) > 0 {
		doc["tags"] = tags
	}

	return doc
}

func buildOperation(route RouteSpec, schemas map[string]map[string]any) map[string]any {
	op := map[string]any{}

	if route.Summary != "" {
		op["summary"] = route.Summary
	}
	if route.Description != "" {
		op["description"] = route.Description
	}
	if len(route.Tags) > 0 {
		op["tags"] = route.Tags
	}
	if route.Deprecated {
		op["deprecated"] = true
	}
	if route.Internal {
		op["x-internal"] = true
	}
	if route.Authenticated {
		op["security"] = []map[string]any{
			{"bearerAuth": []string{}},
		}
	}

	// Path parameters.
	var params []map[string]any
	for _, seg := range strings.Split(route.Path, "/") {
		if strings.HasPrefix(seg, ":") {
			name := seg[1:]
			params = append(params, map[string]any{
				"name":     name,
				"in":       "path",
				"required": true,
				"schema":   map[string]string{"type": "string"},
			})
		}
	}

	// Pagination query params.
	if route.Paginated {
		params = append(params,
			map[string]any{
				"name":        "limit",
				"in":          "query",
				"description": "Maximum number of results to return.",
				"schema":      map[string]any{"type": "integer", "minimum": 1},
			},
			map[string]any{
				"name":        "cursor",
				"in":          "query",
				"description": "Pagination cursor from a previous response.",
				"schema":      map[string]string{"type": "string"},
			},
			map[string]any{
				"name":        "order",
				"in":          "query",
				"description": "Sort order (e.g., created_at:desc).",
				"schema":      map[string]string{"type": "string"},
			},
		)
	}

	// Additional query params.
	for _, qp := range route.SpecQueryParams {
		p := map[string]any{
			"name": qp.Name,
			"in":   "query",
		}
		if qp.Description != "" {
			p["description"] = qp.Description
		}
		if qp.Required {
			p["required"] = true
		}
		s := map[string]any{}
		if qp.Schema.Type != "" {
			s["type"] = qp.Schema.Type
		} else {
			s["type"] = "string"
		}
		if qp.Schema.Format != "" {
			s["format"] = qp.Schema.Format
		}
		if len(qp.Schema.Enum) > 0 {
			s["enum"] = qp.Schema.Enum
		}
		p["schema"] = s
		params = append(params, p)
	}

	if len(params) > 0 {
		op["parameters"] = params
	}

	// Request body.
	if route.RequestBody != nil {
		schemaName := registerSchema(schemas, route.RequestBody)
		op["requestBody"] = map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]string{"$ref": "#/components/schemas/" + schemaName},
				},
			},
		}
	}

	// Responses.
	statusCode := route.StatusCode
	if statusCode == 0 {
		switch strings.ToUpper(route.Method) {
		case "POST":
			statusCode = 201
		case "DELETE":
			statusCode = 204
		default:
			statusCode = 200
		}
	}

	responses := map[string]any{}

	if statusCode == 204 {
		responses["204"] = map[string]any{"description": "No content."}
	} else if route.ResponseBody != nil {
		schemaName := registerSchema(schemas, route.ResponseBody)
		var responseSchema any

		if route.Paginated {
			responseSchema = map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/" + schemaName},
					},
					"pagination": map[string]any{"$ref": "#/components/schemas/Pagination"},
				},
			}
		} else {
			responseSchema = map[string]any{"$ref": "#/components/schemas/" + schemaName}
		}

		responses[statusCodeStr(statusCode)] = map[string]any{
			"description": "Successful response.",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": responseSchema,
				},
			},
		}
	} else {
		responses[statusCodeStr(statusCode)] = map[string]any{
			"description": "Successful response.",
		}
	}

	// Standard error responses.
	if route.Authenticated {
		responses["401"] = map[string]any{
			"description": "Authentication required.",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]string{"$ref": "#/components/schemas/Error"},
				},
			},
		}
	}

	op["responses"] = responses
	return op
}

// =============================================================================
// Schema Reflection
// =============================================================================

// registerSchema reflects on a Go struct and adds it to the schemas map.
// Returns the schema name (used for $ref).
func registerSchema(schemas map[string]map[string]any, v any) string {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	name := t.Name()
	if _, exists := schemas[name]; exists {
		return name
	}

	schema := reflectStructSchema(t)
	schemas[name] = schema
	return name
}

func reflectStructSchema(t reflect.Type) map[string]any {
	if t.Kind() != reflect.Struct {
		return map[string]any{"type": goTypeToOpenAPI(t)}
	}

	properties := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Skip unexported fields.
		if !field.IsExported() {
			continue
		}

		// Handle embedded structs — flatten their fields.
		if field.Anonymous {
			ft := field.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				embedded := reflectStructSchema(ft)
				if props, ok := embedded["properties"].(map[string]any); ok {
					for k, v := range props {
						properties[k] = v
					}
				}
				if req, ok := embedded["required"].([]string); ok {
					required = append(required, req...)
				}
			}
			continue
		}

		// Get JSON field name.
		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		jsonName := strings.Split(jsonTag, ",")[0]
		if jsonName == "" {
			jsonName = field.Name
		}

		ft := field.Type
		isPointer := ft.Kind() == reflect.Ptr
		if isPointer {
			ft = ft.Elem()
		}

		prop := goFieldToSchema(ft)
		properties[jsonName] = prop

		// Non-pointer fields are required (matches our create struct pattern).
		if !isPointer && !hasOmitempty(jsonTag) {
			required = append(required, jsonName)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	return schema
}

func goFieldToSchema(t reflect.Type) map[string]any {
	// Handle time.Time specially.
	if t == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}

	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": goFieldToSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Struct:
		return reflectStructSchema(t)
	default:
		return map[string]any{"type": "string"}
	}
}

func goTypeToOpenAPI(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	default:
		return "string"
	}
}

// =============================================================================
// Helpers
// =============================================================================

// colonToOpenAPIPath converts :param to {param} in route paths.
func colonToOpenAPIPath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") {
			parts[i] = "{" + p[1:] + "}"
		}
	}
	return strings.Join(parts, "/")
}

func statusCodeStr(code int) string {
	switch code {
	case 200:
		return "200"
	case 201:
		return "201"
	case 204:
		return "204"
	default:
		return "200"
	}
}

func hasOmitempty(tag string) bool {
	return strings.Contains(tag, "omitempty")
}

func paginationSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"has_prev":        map[string]any{"type": "boolean", "description": "Whether a previous page exists."},
			"limit":           map[string]any{"type": "integer", "description": "Number of results per page."},
			"previous_cursor": map[string]any{"type": "string", "description": "Cursor for the previous page."},
			"next_cursor":     map[string]any{"type": "string", "description": "Cursor for the next page. Empty when no more results."},
			"page_total":      map[string]any{"type": "integer", "description": "Number of results in the current page."},
		},
	}
}

func errorSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{"type": "string"},
			"code":    map[string]any{"type": "string"},
			"fields": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"field":   map[string]any{"type": "string"},
						"message": map[string]any{"type": "string"},
					},
				},
			},
		},
		"required": []string{"message"},
	}
}

package fop

import sdkfop "github.com/gopernicus/gopernicus/sdk/fop"

// RecordResponse wraps a single record in a consistent envelope.
//
// The relationship and permissions fields are optional — they are only populated
// when the handler is annotated with @with:permissions, which inlines an
// authorization check and exposes what the caller can do on that resource.
// Both fields use omitempty so plain Get handlers produce {"record": {...}}
// and permission-aware handlers produce the full envelope without any special
// casing on the frontend.
type RecordResponse[T any] struct {
	Record       T        `json:"record"`
	Relationship string   `json:"relationship,omitempty"`
	Permissions  []string `json:"permissions,omitempty"`
}

// PageResponse wraps a slice of records with pagination metadata in a typed envelope.
// Generated list handlers use this instead of map[string]any so the shape is
// self-documenting, IDE-friendly, and usable in OpenAPI spec generation.
type PageResponse[T any] struct {
	Data       []T                 `json:"data"`
	Pagination sdkfop.Pagination   `json:"pagination"`
}

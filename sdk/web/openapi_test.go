package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"
)

// Timestamps is an embedded struct used to exercise field flattening.
type Timestamps struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// createUserRequest exercises json tags, pointer-optional, and embedding.
type createUserRequest struct {
	Timestamps
	Name     string  `json:"name"`
	Email    string  `json:"email"`
	Nickname *string `json:"nickname,omitempty"`
	Age      int     `json:"age"`
	secret   string  //nolint:unused // unexported: must be skipped by reflection.
	Ignored  string  `json:"-"`
}

// userResponse is the success entity for list/get routes.
type userResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func buildTestSpec() map[string]any {
	routes := []RouteSpec{
		{
			Method:       "POST",
			Path:         "/users",
			Summary:      "Create a user",
			Tags:         []string{"users"},
			RequestBody:  createUserRequest{},
			ResponseBody: userResponse{},
		},
		{
			Method:        "GET",
			Path:          "/users/{id}",
			Summary:       "Get a user",
			Tags:          []string{"users"},
			Authenticated: true,
			ResponseBody:  userResponse{},
		},
		{
			Method:       "GET",
			Path:         "/users",
			Summary:      "List users",
			Tags:         []string{"users"},
			Paginated:    true,
			ResponseBody: userResponse{},
			SpecQueryParams: []SpecQueryParam{
				{Name: "status", Description: "Filter by status", Schema: ParamSchema{Type: "string", Enum: []string{"active", "banned"}}},
			},
		},
		{
			Method: "DELETE",
			Path:   "/users/{id}",
			Tags:   []string{"users"},
		},
	}
	return BuildOpenAPISpec(OpenAPIInfo{Title: "Test API", Version: "1.2.3"}, routes)
}

func paths(t *testing.T, spec map[string]any) map[string]any {
	t.Helper()
	p, ok := spec["paths"].(map[string]map[string]any)
	if !ok {
		t.Fatalf("paths is %T, want map[string]map[string]any", spec["paths"])
	}
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func TestBuildOpenAPISpec_Envelope(t *testing.T) {
	spec := buildTestSpec()

	if spec["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", spec["openapi"])
	}
	info := spec["info"].(map[string]any)
	if info["title"] != "Test API" || info["version"] != "1.2.3" {
		t.Errorf("info = %v, want Test API / 1.2.3", info)
	}

	comps := spec["components"].(map[string]any)
	sec := comps["securitySchemes"].(map[string]any)
	bearer := sec["bearerAuth"].(map[string]any)
	if bearer["type"] != "http" || bearer["scheme"] != "bearer" || bearer["bearerFormat"] != "JWT" {
		t.Errorf("bearerAuth scheme = %v", bearer)
	}
}

func TestBuildOpenAPISpec_PathParamsCanonicalBraces(t *testing.T) {
	spec := buildTestSpec()
	p := paths(t, spec)

	// {id} paths stay in canonical brace form.
	if _, ok := p["/users/{id}"]; !ok {
		t.Fatalf("missing /users/{id}; got paths %v", keys(p))
	}

	get := p["/users/{id}"].(map[string]any)["get"].(map[string]any)
	params := get["parameters"].([]map[string]any)
	found := false
	for _, prm := range params {
		if prm["in"] == "path" {
			found = true
			if prm["name"] != "id" {
				t.Errorf("path param name = %v, want id", prm["name"])
			}
			if prm["required"] != true {
				t.Errorf("path param required = %v, want true", prm["required"])
			}
		}
	}
	if !found {
		t.Errorf("no path parameter extracted from /users/{id}")
	}
}

func TestColonToOpenAPIPath_CompatibilityShim(t *testing.T) {
	cases := map[string]string{
		"/users/{id}":            "/users/{id}", // canonical passthrough
		"/users/:id":             "/users/{id}", // legacy colon shim
		"/orders/:oid/items/:id": "/orders/{oid}/items/{id}",
		"/health":                "/health",
	}
	for in, want := range cases {
		if got := colonToOpenAPIPath(in); got != want {
			t.Errorf("colonToOpenAPIPath(%q) = %q, want %q", in, got, want)
		}
	}

	// A colon-authored route still yields a path parameter.
	op := buildOperation(RouteSpec{Method: "GET", Path: "/users/:id"}, map[string]map[string]any{})
	params := op["parameters"].([]map[string]any)
	if len(params) != 1 || params[0]["name"] != "id" {
		t.Errorf("colon route params = %v, want one param named id", params)
	}
}

func TestBuildOpenAPISpec_DeterministicSort(t *testing.T) {
	routes := []RouteSpec{
		{Method: "POST", Path: "/b"},
		{Method: "GET", Path: "/a"},
		{Method: "DELETE", Path: "/a"},
		{Method: "GET", Path: "/b"},
	}
	first := BuildOpenAPISpec(OpenAPIInfo{Title: "x", Version: "1"}, routes)
	firstJSON, _ := json.Marshal(first)

	// Re-order the input; output JSON must be byte-identical.
	routes2 := []RouteSpec{
		{Method: "GET", Path: "/b"},
		{Method: "DELETE", Path: "/a"},
		{Method: "POST", Path: "/b"},
		{Method: "GET", Path: "/a"},
	}
	second := BuildOpenAPISpec(OpenAPIInfo{Title: "x", Version: "1"}, routes2)
	secondJSON, _ := json.Marshal(second)

	if string(firstJSON) != string(secondJSON) {
		t.Errorf("spec output not deterministic:\n%s\n---\n%s", firstJSON, secondJSON)
	}
}

func TestReflectSchema_EmbeddedTagsPointerTime(t *testing.T) {
	schema := reflectStructSchema(reflect.TypeOf(createUserRequest{}))

	props := schema["properties"].(map[string]any)

	// Embedded timestamps fields are flattened up.
	created := props["created_at"].(map[string]any)
	if created["type"] != "string" || created["format"] != "date-time" {
		t.Errorf("created_at = %v, want string/date-time", created)
	}
	if _, ok := props["updated_at"]; !ok {
		t.Errorf("embedded updated_at not flattened; props: %v", keys(props))
	}

	// json tag rename + integer mapping.
	if _, ok := props["name"]; !ok {
		t.Errorf("name property missing")
	}
	if age := props["age"].(map[string]any); age["type"] != "integer" {
		t.Errorf("age type = %v, want integer", age["type"])
	}

	// Pointer field is present but optional.
	if _, ok := props["nickname"]; !ok {
		t.Errorf("nickname property missing")
	}

	// Unexported and json:"-" fields are omitted.
	if _, ok := props["secret"]; ok {
		t.Errorf("unexported field leaked into schema")
	}
	if _, ok := props["Ignored"]; ok {
		t.Errorf("json:\"-\" field leaked into schema")
	}

	// Required = non-pointer, non-omitempty, sorted.
	req := schema["required"].([]string)
	if !sort.StringsAreSorted(req) {
		t.Errorf("required not sorted: %v", req)
	}
	if contains(req, "nickname") {
		t.Errorf("pointer/omitempty field nickname marked required: %v", req)
	}
	for _, want := range []string{"name", "email", "age", "created_at", "updated_at"} {
		if !contains(req, want) {
			t.Errorf("expected %q required; got %v", want, req)
		}
	}
}

func TestBuildOperation_AuthenticatedAddsSecurityAnd401(t *testing.T) {
	spec := buildTestSpec()
	p := paths(t, spec)
	get := p["/users/{id}"].(map[string]any)["get"].(map[string]any)

	if _, ok := get["security"]; !ok {
		t.Errorf("authenticated route missing security requirement")
	}
	resp := get["responses"].(map[string]any)
	if _, ok := resp["401"]; !ok {
		t.Errorf("authenticated route missing 401 response; got %v", keys(resp))
	}

	// Unauthenticated routes get no 401.
	post := p["/users"].(map[string]any)["post"].(map[string]any)
	if _, ok := post["responses"].(map[string]any)["401"]; ok {
		t.Errorf("unauthenticated POST should not carry a 401 response")
	}
}

func TestBuildOperation_DefaultStatusCodes(t *testing.T) {
	spec := buildTestSpec()
	p := paths(t, spec)

	post := p["/users"].(map[string]any)["post"].(map[string]any)
	if _, ok := post["responses"].(map[string]any)["201"]; !ok {
		t.Errorf("POST default status = %v, want 201", keys(post["responses"].(map[string]any)))
	}

	del := p["/users/{id}"].(map[string]any)["delete"].(map[string]any)
	delResp := del["responses"].(map[string]any)
	if _, ok := delResp["204"]; !ok {
		t.Errorf("DELETE default status = %v, want 204", keys(delResp))
	}
}

func TestBuildOperation_StatusCodeOverride(t *testing.T) {
	// A 202 override must surface as the "202" response key, not a lossy 200.
	op := buildOperation(RouteSpec{
		Method:       "POST",
		Path:         "/jobs",
		StatusCode:   http.StatusAccepted,
		ResponseBody: userResponse{},
	}, map[string]map[string]any{})
	resp := op["responses"].(map[string]any)
	if _, ok := resp["202"]; !ok {
		t.Errorf("StatusCode 202 override = %v, want a 202 key", keys(resp))
	}
}

func TestBuildOperation_PaginatedWrapperAndQueryParams(t *testing.T) {
	spec := buildTestSpec()
	p := paths(t, spec)
	list := p["/users"].(map[string]any)["get"].(map[string]any)

	// Pagination + custom query params present.
	params := list["parameters"].([]map[string]any)
	names := map[string]bool{}
	for _, prm := range params {
		names[prm["name"].(string)] = true
	}
	for _, want := range []string{"limit", "cursor", "order", "status"} {
		if !names[want] {
			t.Errorf("paginated route missing %q query param; got %v", want, names)
		}
	}

	// Response wraps entity items via allOf(Pagination) + typed items.
	resp := list["responses"].(map[string]any)["200"].(map[string]any)
	schema := resp["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	allOf, ok := schema["allOf"].([]any)
	if !ok || len(allOf) != 1 {
		t.Fatalf("paginated response missing allOf(Pagination); got %v", schema)
	}
	ref := allOf[0].(map[string]any)["$ref"]
	if ref != "#/components/schemas/Pagination" {
		t.Errorf("allOf ref = %v, want Pagination", ref)
	}
	items := schema["properties"].(map[string]any)["items"].(map[string]any)
	if items["type"] != "array" {
		t.Errorf("items type = %v, want array", items["type"])
	}
	itemRef := items["items"].(map[string]any)["$ref"]
	if itemRef != "#/components/schemas/userResponse" {
		t.Errorf("items element ref = %v, want userResponse", itemRef)
	}
}

// TestPaginationSchemaMatchesCrudPage is the load-bearing guard: the hand-
// authored Pagination component must carry exactly crud.Page's json tags. The
// keys are duplicated as a literal here on purpose (no web->crud import); if
// crud.Page's tags change this test and paginationSchema must move together.
func TestPaginationSchemaMatchesCrudPage(t *testing.T) {
	schema := paginationSchema()
	props := schema["properties"].(map[string]any)

	want := []string{"items", "next_cursor", "has_more", "has_prev", "previous_cursor"}
	got := keys(props)
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("Pagination property keys = %v, want crud.Page tags %v", got, want)
	}

	if props["items"].(map[string]any)["type"] != "array" {
		t.Errorf("items should be an array")
	}
	if props["has_more"].(map[string]any)["type"] != "boolean" {
		t.Errorf("has_more should be a boolean")
	}
	if props["next_cursor"].(map[string]any)["type"] != "string" {
		t.Errorf("next_cursor should be a string")
	}
}

func TestBuildOpenAPISpec_Tags(t *testing.T) {
	spec := buildTestSpec()
	tags, ok := spec["tags"].([]map[string]string)
	if !ok || len(tags) == 0 {
		t.Fatalf("tags missing; got %T", spec["tags"])
	}
	if tags[0]["name"] != "users" {
		t.Errorf("tag = %v, want users", tags[0])
	}
}

func TestServeOpenAPI(t *testing.T) {
	h := NewWebHandler()
	routes := []RouteSpec{
		{Method: "GET", Path: "/ping", Summary: "Ping", ResponseBody: userResponse{}},
	}
	h.ServeOpenAPI("/openapi.json", OpenAPIInfo{Title: "Served", Version: "9"}, routes)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("served body not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("served openapi = %v, want 3.1.0", doc["openapi"])
	}
	info := doc["info"].(map[string]any)
	if info["title"] != "Served" {
		t.Errorf("served info title = %v, want Served", info["title"])
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

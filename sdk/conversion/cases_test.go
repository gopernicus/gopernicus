package conversion

import "testing"

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user_id", "UserID"},
		{"api_key", "APIKey"},
		{"created_at", "CreatedAt"},
		{"first_name", "FirstName"},
		{"http_status", "HTTPStatus"},
		{"json_data", "JSONData"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := ToPascalCase(tt.input); got != tt.want {
			t.Errorf("ToPascalCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToCamelCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user_id", "userID"},
		{"api_key", "apiKey"},
		{"created_at", "createdAt"},
		{"first_name", "firstName"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := ToCamelCase(tt.input); got != tt.want {
			t.Errorf("ToCamelCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"AuthAPIKey", "auth_api_key"},
		{"UserID", "user_id"},
		{"createdAt", "created_at"},
		{"ContentTag", "content_tag"},
		{"simpleword", "simpleword"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := ToSnakeCase(tt.input); got != tt.want {
			t.Errorf("ToSnakeCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToKebabCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"auth_sessions", "auth-sessions"},
		{"api_keys", "api-keys"},
		{"single", "single"},
	}

	for _, tt := range tests {
		if got := ToKebabCase(tt.input); got != tt.want {
			t.Errorf("ToKebabCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToLowerSpaced(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ContentTag", "content tag"},
		{"UserID", "user id"},
		{"simpleword", "simpleword"},
	}

	for _, tt := range tests {
		if got := ToLowerSpaced(tt.input); got != tt.want {
			t.Errorf("ToLowerSpaced(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAddAcronym(t *testing.T) {
	AddAcronym("XML")

	if got := ToPascalCase("xml_parser"); got != "XMLParser" {
		t.Errorf("ToPascalCase after AddAcronym(XML): got %q, want %q", got, "XMLParser")
	}
}

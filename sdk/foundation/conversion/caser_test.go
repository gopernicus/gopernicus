package conversion

import "testing"

// TestCaserCustomAcronyms proves a Caser built with WithAcronyms keeps the
// custom acronyms uppercase in ToPascalCase/ToCamelCase, on top of the built-ins.
func TestCaserCustomAcronyms(t *testing.T) {
	c := NewCaser(WithAcronyms("K8S", "XML"))

	pascal := []struct {
		input string
		want  string
	}{
		{"k8s_node", "K8SNode"},
		{"xml_doc", "XMLDoc"},
		{"api_key", "APIKey"}, // built-in still applies
		{"created_at", "CreatedAt"},
	}
	for _, tt := range pascal {
		if got := c.ToPascalCase(tt.input); got != tt.want {
			t.Errorf("Caser.ToPascalCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}

	camel := []struct {
		input string
		want  string
	}{
		{"k8s_node", "k8sNode"},
		{"xml_doc", "xmlDoc"},
		{"api_key", "apiKey"},
	}
	for _, tt := range camel {
		if got := c.ToCamelCase(tt.input); got != tt.want {
			t.Errorf("Caser.ToCamelCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestCaserOptionsDoNotMutateDefault proves a custom Caser's acronyms never leak
// into the package default (the D-2 data-race regression this seam replaces).
// The package-level func must still treat "k8s" as an ordinary word.
func TestCaserOptionsDoNotMutateDefault(t *testing.T) {
	_ = NewCaser(WithAcronyms("K8S"))

	if got := ToPascalCase("k8s_node"); got != "K8sNode" {
		t.Errorf("ToPascalCase(%q) = %q, want %q (default must not learn K8S)", "k8s_node", got, "K8sNode")
	}
	if got := NewCaser().ToPascalCase("k8s_node"); got != "K8sNode" {
		t.Errorf("NewCaser().ToPascalCase(%q) = %q, want %q (built-in table must stay clean)", "k8s_node", got, "K8sNode")
	}
}

// TestCaserInstancesIsolated proves two casers built from the same base do not
// share acronym state.
func TestCaserInstancesIsolated(t *testing.T) {
	withK8s := NewCaser(WithAcronyms("K8S"))
	plain := NewCaser()

	if got := withK8s.ToPascalCase("k8s_node"); got != "K8SNode" {
		t.Errorf("withK8s.ToPascalCase(%q) = %q, want %q", "k8s_node", got, "K8SNode")
	}
	if got := plain.ToPascalCase("k8s_node"); got != "K8sNode" {
		t.Errorf("plain.ToPascalCase(%q) = %q, want %q", "k8s_node", got, "K8sNode")
	}
}

// TestDefaultCaserMatchesPackageFuncs proves NewCaser() with no options behaves
// identically to the package-level funcs across every conversion.
func TestDefaultCaserMatchesPackageFuncs(t *testing.T) {
	c := NewCaser()

	pascalCamel := []string{"user_id", "api_key", "created_at", "http_status", "json_data", ""}
	for _, in := range pascalCamel {
		if got, want := c.ToPascalCase(in), ToPascalCase(in); got != want {
			t.Errorf("Caser.ToPascalCase(%q) = %q, want %q", in, got, want)
		}
		if got, want := c.ToCamelCase(in), ToCamelCase(in); got != want {
			t.Errorf("Caser.ToCamelCase(%q) = %q, want %q", in, got, want)
		}
	}

	fromCased := []string{"AuthAPIKey", "UserID", "createdAt", "ContentTag", "simpleword", ""}
	for _, in := range fromCased {
		if got, want := c.ToSnakeCase(in), ToSnakeCase(in); got != want {
			t.Errorf("Caser.ToSnakeCase(%q) = %q, want %q", in, got, want)
		}
		if got, want := c.ToKebabCase(in), ToKebabCase(in); got != want {
			t.Errorf("Caser.ToKebabCase(%q) = %q, want %q", in, got, want)
		}
		if got, want := c.ToLowerSpaced(in), ToLowerSpaced(in); got != want {
			t.Errorf("Caser.ToLowerSpaced(%q) = %q, want %q", in, got, want)
		}
	}
}

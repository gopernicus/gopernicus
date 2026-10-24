package logging

import (
	"reflect"
	"testing"
)

// The environment package tests parsing generically. This package owns only
// the Options tag contract and tests it without coupling two pkg peers.
func TestOptions_EnvironmentTags(t *testing.T) {
	want := map[string]struct {
		env string
		def string
	}{
		"Level":  {env: "LOG_LEVEL", def: "INFO"},
		"Format": {env: "LOG_FORMAT", def: "json"},
		"Output": {env: "LOG_OUTPUT", def: "STDERR"},
		// No default tag: an env value would replace it, and RedactKeys only
		// ever adds to DefaultRedactKeys.
		"RedactKeys": {env: "LOG_REDACT_KEYS", def: ""},
	}

	typ := reflect.TypeFor[Options]()
	for name, tags := range want {
		field, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("Options.%s is missing", name)
		}
		if got := field.Tag.Get("env"); got != tags.env {
			t.Errorf("Options.%s env tag = %q, want %q", name, got, tags.env)
		}
		if got := field.Tag.Get("default"); got != tags.def {
			t.Errorf("Options.%s default tag = %q, want %q", name, got, tags.def)
		}
	}
}

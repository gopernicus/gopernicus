package web

import (
	"reflect"
	"testing"
)

// The environment package tests parsing generically. This package owns only
// the ServerConfig tag contract and tests it without coupling foundation peers.
func TestServerConfig_EnvironmentTags(t *testing.T) {
	want := map[string]struct {
		env string
		def string
	}{
		"Host":            {env: "HOST", def: "localhost"},
		"Port":            {env: "PORT", def: "8080"},
		"ReadTimeout":     {env: "READ_TIMEOUT", def: "15s"},
		"WriteTimeout":    {env: "WRITE_TIMEOUT", def: "15s"},
		"IdleTimeout":     {env: "IDLE_TIMEOUT", def: "120s"},
		"ShutdownTimeout": {env: "SHUTDOWN_TIMEOUT", def: "10s"},
		// No default: zero already means "trust no proxy" and "derive the
		// origin from the listen address".
		"TrustedProxyCount": {env: "TRUSTED_PROXY_COUNT"},
		"PublicBaseURL":     {env: "PUBLIC_BASE_URL"},
	}

	typ := reflect.TypeFor[ServerConfig]()

	// The exact set: environment's tests carry a mirror of these tags, so a
	// field added, renamed, or retagged here must be reflected there too.
	var tagged []string
	for i := range typ.NumField() {
		if typ.Field(i).Tag.Get("env") != "" {
			tagged = append(tagged, typ.Field(i).Name)
		}
	}
	if len(tagged) != len(want) {
		t.Fatalf("ServerConfig has %d env-tagged fields %v, want exactly %d", len(tagged), tagged, len(want))
	}

	for name, tags := range want {
		field, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("ServerConfig.%s is missing", name)
		}
		if got := field.Tag.Get("env"); got != tags.env {
			t.Errorf("ServerConfig.%s env tag = %q, want %q", name, got, tags.env)
		}
		if got := field.Tag.Get("default"); got != tags.def {
			t.Errorf("ServerConfig.%s default tag = %q, want %q", name, got, tags.def)
		}
	}
}

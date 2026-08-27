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
	}

	typ := reflect.TypeFor[ServerConfig]()
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

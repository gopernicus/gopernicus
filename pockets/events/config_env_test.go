package events

import (
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// TestConfigEnvTags pins the EVENTS_* keys on Config: a host tunes the gateway
// through ParseEnvTags. The collaborator fields (Bus, StreamMiddleware,
// Authorize, Projector) carry no tag and are untouched by the parse.
func TestConfigEnvTags(t *testing.T) {
	t.Setenv("EVENTS_HEARTBEAT", "10s")
	t.Setenv("EVENTS_BUFFER_SIZE", "128")
	t.Setenv("EVENTS_MAX_CONN_AGE", "30m")
	t.Setenv("EVENTS_MAX_CONNS_PER_SUBJECT", "25")

	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Heartbeat != 10*time.Second {
		t.Errorf("Heartbeat = %v, want 10s", cfg.Heartbeat)
	}
	if cfg.BufferSize != 128 {
		t.Errorf("BufferSize = %d, want 128", cfg.BufferSize)
	}
	if cfg.MaxConnAge != 30*time.Minute {
		t.Errorf("MaxConnAge = %v, want 30m", cfg.MaxConnAge)
	}
	if cfg.MaxConnsPerSubject != 25 {
		t.Errorf("MaxConnsPerSubject = %d, want 25", cfg.MaxConnsPerSubject)
	}
}

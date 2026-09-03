package jobs

import (
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// TestConfigEnvTags pins the JOBS_* keys on Config: a host reads its sizing and
// cadence through ParseEnvTags instead of hand-parsing them. Collaborator fields
// (Handlers, Cron, Logger) carry no tag and are untouched by the parse.
func TestConfigEnvTags(t *testing.T) {
	t.Setenv("JOBS_WORKERS", "7")
	t.Setenv("JOBS_POLL_INTERVAL", "250ms")
	t.Setenv("JOBS_IDLE_INTERVAL", "3s")
	t.Setenv("JOBS_MAX_ATTEMPTS", "9")
	t.Setenv("JOBS_SCHEDULE_BATCH", "40")
	t.Setenv("JOBS_HEARTBEAT_INTERVAL", "1m")

	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Workers != 7 {
		t.Errorf("Workers = %d, want 7", cfg.Workers)
	}
	if cfg.PollInterval != 250*time.Millisecond {
		t.Errorf("PollInterval = %v, want 250ms", cfg.PollInterval)
	}
	if cfg.IdleInterval != 3*time.Second {
		t.Errorf("IdleInterval = %v, want 3s", cfg.IdleInterval)
	}
	if cfg.MaxAttempts != 9 {
		t.Errorf("MaxAttempts = %d, want 9", cfg.MaxAttempts)
	}
	if cfg.ScheduleBatch != 40 {
		t.Errorf("ScheduleBatch = %d, want 40", cfg.ScheduleBatch)
	}
	if cfg.Heartbeat != time.Minute {
		t.Errorf("Heartbeat = %v, want 1m", cfg.Heartbeat)
	}
}

// TestFencedRuntimeConfigEnvTags pins the fenced runtime's keys, including the
// two it does not share with Config (JOBS_LEASE_FOR, JOBS_PROCESS_TIMEOUT).
func TestFencedRuntimeConfigEnvTags(t *testing.T) {
	t.Setenv("JOBS_WORKERS", "5")
	t.Setenv("JOBS_POLL_INTERVAL", "100ms")
	t.Setenv("JOBS_IDLE_INTERVAL", "2s")
	t.Setenv("JOBS_MAX_ATTEMPTS", "4")
	t.Setenv("JOBS_LEASE_FOR", "45s")
	t.Setenv("JOBS_PROCESS_TIMEOUT", "20s")

	var cfg FencedRuntimeConfig
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Workers != 5 {
		t.Errorf("Workers = %d, want 5", cfg.Workers)
	}
	if cfg.PollInterval != 100*time.Millisecond {
		t.Errorf("PollInterval = %v, want 100ms", cfg.PollInterval)
	}
	if cfg.IdleInterval != 2*time.Second {
		t.Errorf("IdleInterval = %v, want 2s", cfg.IdleInterval)
	}
	if cfg.MaxAttempts != 4 {
		t.Errorf("MaxAttempts = %d, want 4", cfg.MaxAttempts)
	}
	if cfg.LeaseFor != 45*time.Second {
		t.Errorf("LeaseFor = %v, want 45s", cfg.LeaseFor)
	}
	if cfg.ProcessTimeout != 20*time.Second {
		t.Errorf("ProcessTimeout = %v, want 20s", cfg.ProcessTimeout)
	}
}

// TestFencedRuntimeConfigEnvNamespace proves the documented disambiguation for a
// host running both runtimes in one process: the fenced config shares the JOBS_*
// key names with Config, so it is parsed under a namespace and reads
// FENCED_JOBS_* instead — the unnamespaced values never reach it.
func TestFencedRuntimeConfigEnvNamespace(t *testing.T) {
	t.Setenv("JOBS_WORKERS", "7")
	t.Setenv("FENCED_JOBS_WORKERS", "3")
	t.Setenv("FENCED_JOBS_LEASE_FOR", "90s")

	var cfg FencedRuntimeConfig
	if err := environment.ParseEnvTags("FENCED", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Workers != 3 {
		t.Errorf("Workers = %d, want 3 (FENCED_JOBS_WORKERS, not JOBS_WORKERS)", cfg.Workers)
	}
	if cfg.LeaseFor != 90*time.Second {
		t.Errorf("LeaseFor = %v, want 90s", cfg.LeaseFor)
	}
}

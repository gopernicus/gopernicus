package logging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"WARN", slog.LevelWarn},
		{"warn", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"invalid", slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ParseLevel(tt.input); got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNew_Output(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		level   slog.Level
		text    bool
		stdout  bool
	}{
		{name: "defaults", level: slog.LevelInfo},
		{name: "unknown values", options: Options{Level: "unknown", Format: "unknown", Output: "unknown"}, level: slog.LevelInfo},
		{name: "case insensitive", options: Options{Level: "debug", Format: "TeXt", Output: "stdout"}, level: slog.LevelDebug, text: true, stdout: true},
		{name: "warning", options: Options{Level: "WARNING", Format: "JSON", Output: "STDOUT"}, level: slog.LevelWarn, stdout: true},
		{name: "error", options: Options{Level: "ERROR", Format: "text", Output: "STDERR"}, level: slog.LevelError, text: true},
	}
	levels := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError}
	ctx := sdk.WithRequestID(context.Background(), "req-123")
	ctx = sdk.WithTraceID(ctx, "trace-456")
	ctx = sdk.WithSpanID(ctx, "span-789")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			global := slog.Default()
			stdout, stderr := captureOutput(t, func() {
				log := New(tt.options)
				for _, level := range levels {
					log.Log(ctx, level, "message", "key", "value")
				}
			})
			if slog.Default() != global {
				t.Error("New changed the global logger")
			}
			output, unused := stderr, stdout
			if tt.stdout {
				output, unused = stdout, stderr
			}
			if unused != "" {
				t.Errorf("unexpected output on the other destination: %s", unused)
			}
			var expectedLevels []slog.Level
			for _, level := range levels {
				if level >= tt.level {
					expectedLevels = append(expectedLevels, level)
				}
			}
			lines := strings.Split(strings.TrimSpace(output), "\n")
			if len(lines) != len(expectedLevels) {
				t.Fatalf("got %d log lines, want %d: %s", len(lines), len(expectedLevels), output)
			}
			for i, line := range lines {
				want := map[string]string{
					"level": expectedLevels[i].String(), "msg": "message", "key": "value",
					"request_id": "req-123", "trace_id": "trace-456", "span_id": "span-789",
				}
				if tt.text {
					for key, value := range want {
						if !strings.Contains(line, key+"="+value) {
							t.Errorf("text log missing %s=%s: %s", key, value, line)
						}
					}
					continue
				}
				entry := parseJSON(t, []byte(line))
				for key, value := range want {
					if entry[key] != value {
						t.Errorf("JSON %s = %v, want %s", key, entry[key], value)
					}
				}
			}
		})
	}
}

// These tests run sequentially because New takes its destination from os.Stdout
// or os.Stderr. Files let us inspect actual output without blocking on a pipe.
func captureOutput(t *testing.T, log func()) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	errOut, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { errOut.Close() })
	originalOut, originalErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, errOut
	defer func() { os.Stdout, os.Stderr = originalOut, originalErr }()
	log()
	stdoutBytes, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	stderrBytes, err := os.ReadFile(errOut.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(stdoutBytes), string(stderrBytes)
}

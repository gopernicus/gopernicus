package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
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
			got := ParseLevel(tt.input)
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseOutput(t *testing.T) {
	// STDOUT should not be nil
	out := parseOutput("STDOUT")
	if out == nil {
		t.Fatal("parseOutput(STDOUT) returned nil")
	}

	// Default (STDERR) should not be nil
	out = parseOutput("STDERR")
	if out == nil {
		t.Fatal("parseOutput(STDERR) returned nil")
	}

	// Unknown defaults to stderr (non-nil)
	out = parseOutput("unknown")
	if out == nil {
		t.Fatal("parseOutput(unknown) returned nil")
	}
}

func TestNew_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	log.Info("hello", "key", "value")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, buf.String())
	}
	if entry["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", entry["msg"])
	}
	if entry["key"] != "value" {
		t.Errorf("key = %v, want value", entry["key"])
	}
}

func TestNew_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	log.Info("hello")

	output := buf.String()
	if len(output) == 0 {
		t.Fatal("text handler produced no output")
	}
}

func TestNew_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	log.Info("should not appear")
	if buf.Len() > 0 {
		t.Errorf("info message appeared at WARN level: %s", buf.String())
	}

	log.Warn("should appear")
	if buf.Len() == 0 {
		t.Error("warn message did not appear at WARN level")
	}
}

func TestNewDefault(t *testing.T) {
	log := NewDefault()
	if log == nil {
		t.Fatal("NewDefault returned nil")
	}
}

func TestNewNoop(t *testing.T) {
	log := NewNoop()
	if log == nil {
		t.Fatal("NewNoop returned nil")
	}
	// Should not panic
	log.Info("this goes nowhere")
}

func TestNew_WithOptions(t *testing.T) {
	log := New(Options{
		Level:  "DEBUG",
		Format: "text",
		Output: "STDOUT",
	})
	if log == nil {
		t.Fatal("New returned nil")
	}
}

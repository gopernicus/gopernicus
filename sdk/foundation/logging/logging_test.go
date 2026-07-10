package logging

import (
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
			if got := ParseLevel(tt.input); got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseOutput(t *testing.T) {
	for _, in := range []string{"STDOUT", "STDERR", "unknown"} {
		if out := parseOutput(in); out == nil {
			t.Fatalf("parseOutput(%q) returned nil", in)
		}
	}
}

func TestNewDefault(t *testing.T) {
	if log := NewDefault(); log == nil {
		t.Fatal("NewDefault returned nil")
	}
}

func TestNewNoop(t *testing.T) {
	log := NewNoop()
	if log == nil {
		t.Fatal("NewNoop returned nil")
	}
	log.Info("this goes nowhere")
}

func TestNew_WithOptions(t *testing.T) {
	if log := New(Options{Level: "DEBUG", Format: "text", Output: "STDOUT"}); log == nil {
		t.Fatal("New returned nil")
	}
}

func TestNew_WithTracing(t *testing.T) {
	if log := New(Options{Level: "DEBUG", Format: "json", Output: "STDERR"}, WithTracing()); log == nil {
		t.Fatal("New with WithTracing returned nil")
	}
}

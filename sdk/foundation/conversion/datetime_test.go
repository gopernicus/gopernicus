package conversion

import (
	"testing"
	"time"
)

func TestParseDateTime(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"2024-01-15T10:30:00Z", false},
		{"2024-01-15T10:30:00+05:00", false},
		{"2024-01-15T10:30:00.123456789Z", false},
		{"2024-01-15", false},
		{"2024-01-15 10:30:00", false},
		{"2024-01-15T10:30:00", false},
		{"not-a-date", true},
		{"01/15/2024", true}, // US format not supported by ParseDateTime
	}

	for _, tt := range tests {
		result, err := ParseDateTime(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseDateTime(%q): err = %v, wantErr = %v", tt.input, err, tt.wantErr)
		}
		if !tt.wantErr && result.IsZero() {
			t.Errorf("ParseDateTime(%q): got zero time, want non-zero", tt.input)
		}
	}

	// Verify specific parsing.
	result, _ := ParseDateTime("2024-06-15")
	if result.Year() != 2024 || result.Month() != time.June || result.Day() != 15 {
		t.Errorf("ParseDateTime(2024-06-15) = %v, want 2024-06-15", result)
	}
}

func TestParseFlexibleDate(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"2024-01-15T10:30:00Z", false},
		{"2024-01-15", false},
		{"2024/01/15", false},
		{"01/15/2024", false}, // US format
		{"01-15-2024", false}, // US format
		{"garbage", true},
	}

	for _, tt := range tests {
		_, err := ParseFlexibleDate(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseFlexibleDate(%q): err = %v, wantErr = %v", tt.input, err, tt.wantErr)
		}
	}
}

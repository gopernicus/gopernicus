package conversion

import (
	"fmt"
	"time"
)

// ParseDateTime parses a datetime string using RFC3339/ISO8601 formats.
// Preferred for API inputs where the format is controlled.
//
// Supported formats (in order of precedence):
//   - RFC3339: "2006-01-02T15:04:05Z07:00"
//   - RFC3339Nano: "2006-01-02T15:04:05.999999999Z07:00"
//   - Date only: "2006-01-02"
//   - DateTime without timezone: "2006-01-02 15:04:05"
//   - ISO8601 without timezone: "2006-01-02T15:04:05"
func ParseDateTime(dateStr string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		time.DateOnly,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse datetime: %s (expected RFC3339 or ISO8601 format)", dateStr)
}

// ParseFlexibleDate attempts to parse a date string using multiple common formats.
//
// WARNING: This function tries both US (MM/DD/YYYY) and European (DD/MM/YYYY)
// formats, which are ambiguous. For example, "01/02/2024" could be interpreted
// as either January 2nd or February 1st. US formats are tried first.
//
// For new code, prefer ParseDateTime which only accepts unambiguous ISO8601 formats.
func ParseFlexibleDate(dateStr string) (time.Time, error) {
	formats := []string{
		// Unambiguous formats first (ISO8601 family)
		time.RFC3339,
		time.RFC3339Nano,
		time.DateOnly,
		"2006-01-02 15:04:05",
		"2006/01/02",

		// US formats (MM/DD/YYYY) — tried before European
		"01/02/2006",
		"01-02-2006",
		"01/02/06",
		"01-02-06",

		// European formats (DD/MM/YYYY)
		"02/01/2006",
		"02-01-2006",
		"02/01/06",
		"02-01-06",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse date: %s", dateStr)
}

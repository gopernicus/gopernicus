package pgxdb

import (
	"fmt"
	"regexp"
	"strings"
)

// QuoteIdentifier validates and quotes a SQL identifier (column, table, schema.table).
// Returns the properly quoted identifier or an error if the input is unsafe.
//
// Supported formats:
//   - "column_name"         → `"column_name"`
//   - "schema.table"        → `"schema"."table"`
//   - "column_name alias"   → `"column_name" "alias"`
//   - "schema.table alias"  → `"schema"."table" "alias"`
func QuoteIdentifier(name string) (string, error) {
	dangerousChars := regexp.MustCompile(`[;'"\\()]`)
	if dangerousChars.MatchString(name) {
		return "", fmt.Errorf("identifier contains dangerous characters: %s", name)
	}

	parts := strings.Split(name, " ")

	if len(parts) > 2 {
		return "", fmt.Errorf("invalid identifier format (too many parts): %s", name)
	}

	identifierPattern := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z][a-zA-Z0-9_]*)?$`)

	// Simple identifier (no alias).
	if len(parts) == 1 {
		if strings.Contains(parts[0], ".") {
			segments := strings.Split(parts[0], ".")
			if len(segments) > 2 {
				return "", fmt.Errorf("invalid identifier format (too many segments): %s", name)
			}
			for i, segment := range segments {
				if !identifierPattern.MatchString(segment) {
					return "", fmt.Errorf("invalid identifier segment at position %d: %s", i, segment)
				}
			}
			quotedSegments := make([]string, len(segments))
			for i, segment := range segments {
				quotedSegments[i] = fmt.Sprintf(`"%s"`, segment)
			}
			return strings.Join(quotedSegments, "."), nil
		}

		if identifierPattern.MatchString(parts[0]) {
			return fmt.Sprintf(`"%s"`, parts[0]), nil
		}
		return "", fmt.Errorf("invalid identifier format: %s", parts[0])
	}

	// Two parts: identifier + alias.
	quoted := ""
	if strings.Contains(parts[0], ".") {
		segments := strings.Split(parts[0], ".")
		if len(segments) > 2 {
			return "", fmt.Errorf("invalid identifier format (too many segments in first part): %s", parts[0])
		}
		for i, segment := range segments {
			if !identifierPattern.MatchString(segment) {
				return "", fmt.Errorf("invalid identifier segment at position %d: %s", i, segment)
			}
		}
		quotedSegments := make([]string, len(segments))
		for i, segment := range segments {
			quotedSegments[i] = fmt.Sprintf(`"%s"`, segment)
		}
		quoted = strings.Join(quotedSegments, ".")
	} else {
		if !identifierPattern.MatchString(parts[0]) {
			return "", fmt.Errorf("invalid identifier first part: %s", parts[0])
		}
		quoted = fmt.Sprintf(`"%s"`, parts[0])
	}

	if !identifierPattern.MatchString(parts[1]) {
		return "", fmt.Errorf("invalid identifier alias: %s", parts[1])
	}

	quoted += fmt.Sprintf(` "%s"`, parts[1])

	return quoted, nil
}

package schema

import (
	"regexp"
	"strings"
)

var (
	// quotedLiteralRe pulls single-quoted string literals out of a CHECK
	// constraint definition segment.
	quotedLiteralRe = regexp.MustCompile(`'([^']*)'`)

	// checkInListRe matches the authored `col IN (...)` form.
	checkInListRe = regexp.MustCompile(`(?i)\bIN\s*\(`)

	// checkNotInRe disqualifies `col NOT IN (...)` — an exclusion list, not
	// an allowed-value list.
	checkNotInRe = regexp.MustCompile(`(?i)\bNOT\s+IN\s*\(`)
)

// CheckConstraintAllowedValues extracts the allowed string values from a
// single-column CHECK constraint definition, or nil when the constraint does
// not pin the column to a finite set of literals. Three shapes are
// recognized, in order:
//
//   - Postgres normalized IN-list (what pg_get_constraintdef emits):
//     CHECK (((principal_type)::text = ANY ((ARRAY['user'::character varying, 'service_account'::character varying])::text[])))
//   - authored IN-list (hand-written snapshots, other dialects):
//     CHECK (principal_type IN ('user', 'service_account'))
//   - single-value equality:
//     CHECK (((identifier_type)::text = 'email'::text))
//
// Range, pattern (~ / LIKE / SIMILAR TO), NOT IN, and multi-literal
// non-list checks return nil — one literal inside those is not a closed
// domain. Value order follows the definition, so extraction is
// deterministic.
func CheckConstraintAllowedValues(definition string) []string {
	upper := strings.ToUpper(definition)

	// Normalized IN-list: `= ANY (ARRAY['a'::..., 'b'::...])`.
	if strings.Contains(upper, "= ANY") {
		if start := strings.Index(definition, "ARRAY["); start >= 0 {
			if end := strings.Index(definition[start:], "]"); end > 0 {
				if vals := quotedLiterals(definition[start : start+end+1]); len(vals) > 0 {
					return vals
				}
			}
		}
	}

	// Authored IN-list: `col IN ('a', 'b')`.
	if loc := checkInListRe.FindStringIndex(definition); loc != nil && !checkNotInRe.MatchString(definition) {
		seg := definition[loc[1]-1:]
		if end := strings.IndexByte(seg, ')'); end > 0 {
			if vals := quotedLiterals(seg[:end+1]); len(vals) > 0 {
				return vals
			}
		}
	}

	// Single-value equality with exactly one quoted literal.
	if strings.Contains(definition, "=") &&
		!strings.ContainsAny(definition, "~<>") &&
		!strings.Contains(upper, " LIKE ") &&
		!strings.Contains(upper, " SIMILAR TO ") {
		if vals := quotedLiterals(definition); len(vals) == 1 {
			return vals
		}
	}

	return nil
}

// EnrichCheckConstraintEnums folds single-column CHECK ... IN constraints
// into the column enum metadata (IsEnum / EnumValues), so every generator
// that already consults the enum machinery — fixtures, bridge validation,
// integration-test value selection — honors CHECK-pinned columns the same
// way it honors native enum types. Native enum columns keep their reflected
// values; non-string columns are left untouched.
//
// Applied at load time only (LoadJSON): the persisted reflected-schema JSON
// is never rewritten, so committed snapshots stay byte-stable. Idempotent —
// already-enum columns are skipped.
func EnrichCheckConstraintEnums(s *ReflectedSchema) {
	if s == nil {
		return
	}
	for _, table := range s.Tables {
		if table == nil {
			continue
		}
		for _, c := range table.Constraints {
			if !strings.EqualFold(c.Type, "CHECK") || len(c.Columns) != 1 {
				continue
			}
			vals := CheckConstraintAllowedValues(c.Definition)
			if len(vals) == 0 {
				continue
			}
			for i := range table.Columns {
				col := &table.Columns[i]
				if col.Name != c.Columns[0] || col.IsEnum {
					continue
				}
				if strings.TrimPrefix(col.GoType, "*") != "string" {
					continue
				}
				col.IsEnum = true
				col.EnumValues = vals
			}
		}
	}
}

func quotedLiterals(s string) []string {
	matches := quotedLiteralRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	vals := make([]string, 0, len(matches))
	for _, m := range matches {
		vals = append(vals, m[1])
	}
	return vals
}

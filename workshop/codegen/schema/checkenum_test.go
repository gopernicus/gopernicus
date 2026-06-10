package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCheckConstraintAllowedValues(t *testing.T) {
	tests := []struct {
		name string
		def  string
		want []string
	}{
		{
			name: "postgres normalized IN-list (varchar casts)",
			def:  `CHECK (((principal_type)::text = ANY ((ARRAY['user'::character varying, 'service_account'::character varying])::text[])))`,
			want: []string{"user", "service_account"},
		},
		{
			name: "postgres normalized IN-list (text casts, no column cast)",
			def:  `CHECK ((status = ANY (ARRAY['PENDING'::text, 'STAGED'::text, 'COMPLETED'::text, 'FAILED'::text, 'DEAD_LETTER'::text])))`,
			want: []string{"PENDING", "STAGED", "COMPLETED", "FAILED", "DEAD_LETTER"},
		},
		{
			name: "authored IN-list",
			def:  `CHECK (principal_type IN ('user', 'service_account'))`,
			want: []string{"user", "service_account"},
		},
		{
			name: "authored IN-list lowercase keyword",
			def:  `check (record_state in ('active','archived','deleted'))`,
			want: []string{"active", "archived", "deleted"},
		},
		{
			name: "single-value equality (normalized)",
			def:  `CHECK (((identifier_type)::text = 'email'::text))`,
			want: []string{"email"},
		},
		{
			name: "authored NOT IN is an exclusion list",
			def:  `CHECK (purpose NOT IN ('banned'))`,
			want: nil,
		},
		{
			name: "normalized NOT IN (<> ALL) is an exclusion list",
			def:  `CHECK (((purpose)::text <> ALL ((ARRAY['banned'::character varying])::text[])))`,
			want: nil,
		},
		{
			name: "pattern match is not a closed domain",
			def:  `CHECK (((slug)::text ~ '^[a-z][a-z0-9-]*$'::text))`,
			want: nil,
		},
		{
			name: "range comparison is not a closed domain",
			def:  `CHECK (created_at > '2020-01-01'::date)`,
			want: nil,
		},
		{
			name: "numeric check has no literals",
			def:  `CHECK ((attempt_count >= 0))`,
			want: nil,
		},
		{
			name: "boolean dependency check has no literals",
			def:  `CHECK (((act_as_user = false) OR ((act_as_user = true) AND (owner_user_id IS NOT NULL))))`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckConstraintAllowedValues(tt.def)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CheckConstraintAllowedValues(%q) = %v, want %v", tt.def, got, tt.want)
			}
		})
	}
}

func checkEnumTestSchema() *ReflectedSchema {
	return &ReflectedSchema{
		Tables: map[string]*TableInfo{
			"principals": {
				TableName: "principals",
				Columns: []ColumnInfo{
					{Name: "principal_id", DBType: "varchar", GoType: "string", IsPrimaryKey: true},
					{Name: "principal_type", DBType: "varchar(64)", GoType: "string"},
				},
				Constraints: []ConstraintInfo{{
					Name:       "principals_type_check",
					Type:       "CHECK",
					Columns:    []string{"principal_type"},
					Definition: `CHECK (((principal_type)::text = ANY ((ARRAY['user'::character varying, 'service_account'::character varying])::text[])))`,
				}},
			},
		},
	}
}

func TestEnrichCheckConstraintEnumsFeedsEnumMachinery(t *testing.T) {
	s := checkEnumTestSchema()
	EnrichCheckConstraintEnums(s)

	col := s.Tables["principals"].Columns[1]
	if !col.IsEnum {
		t.Fatal("expected principal_type to be marked IsEnum")
	}
	if want := []string{"user", "service_account"}; !reflect.DeepEqual(col.EnumValues, want) {
		t.Errorf("EnumValues = %v, want %v", col.EnumValues, want)
	}
	if pk := s.Tables["principals"].Columns[0]; pk.IsEnum {
		t.Error("unconstrained column must stay non-enum")
	}
}

// Tables with no CHECK-IN constraints must be untouched — committed
// pre-enrichment snapshots regenerate identically.
func TestEnrichCheckConstraintEnumsInertWithoutCheckIn(t *testing.T) {
	s := checkEnumTestSchema()
	s.Tables["principals"].Constraints = []ConstraintInfo{
		{Name: "uq", Type: "UNIQUE", Columns: []string{"principal_type"}},
		{Name: "len", Type: "CHECK", Columns: []string{"principal_type"}, Definition: "CHECK ((char_length(principal_type) > 0))"},
	}
	EnrichCheckConstraintEnums(s)

	for _, col := range s.Tables["principals"].Columns {
		if col.IsEnum || col.EnumValues != nil {
			t.Errorf("column %s gained enum metadata without a CHECK-IN constraint", col.Name)
		}
	}
}

func TestEnrichCheckConstraintEnumsGuards(t *testing.T) {
	s := checkEnumTestSchema()
	table := s.Tables["principals"]

	// Non-string columns are never marked.
	table.Columns = append(table.Columns, ColumnInfo{Name: "level", DBType: "integer", GoType: "int"})
	table.Constraints = append(table.Constraints, ConstraintInfo{
		Name: "level_check", Type: "CHECK", Columns: []string{"level"},
		Definition: `CHECK (((level)::text = ANY ((ARRAY['1'::text, '2'::text])::text[])))`,
	})

	// Multi-column CHECKs are skipped even when they contain literals.
	table.Constraints = append(table.Constraints, ConstraintInfo{
		Name: "pair_check", Type: "CHECK", Columns: []string{"principal_id", "principal_type"},
		Definition: `CHECK (principal_id IN ('a') OR principal_type IN ('b'))`,
	})

	// Native enum columns keep their reflected values.
	table.Columns = append(table.Columns, ColumnInfo{
		Name: "mood", DBType: "mood", GoType: "string",
		IsEnum: true, EnumType: "mood", EnumValues: []string{"happy", "sad"},
	})
	table.Constraints = append(table.Constraints, ConstraintInfo{
		Name: "mood_check", Type: "CHECK", Columns: []string{"mood"},
		Definition: `CHECK (mood IN ('only'))`,
	})

	EnrichCheckConstraintEnums(s)

	for _, col := range table.Columns {
		switch col.Name {
		case "principal_type":
			if !col.IsEnum {
				t.Error("principal_type should be enum")
			}
		case "level":
			if col.IsEnum {
				t.Error("non-string column must not be marked enum")
			}
		case "principal_id":
			if col.IsEnum {
				t.Error("multi-column CHECK must not mark columns enum")
			}
		case "mood":
			if want := []string{"happy", "sad"}; !reflect.DeepEqual(col.EnumValues, want) {
				t.Errorf("native enum values overwritten: %v", col.EnumValues)
			}
		}
	}
}

// LoadJSON is the enrichment choke point: pre-baked snapshots without enum
// metadata gain it on load, and the file on disk is never rewritten.
func TestLoadJSONAppliesCheckEnumEnrichment(t *testing.T) {
	s := checkEnumTestSchema()
	path := filepath.Join(t.TempDir(), "_public.json")
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	col := loaded.Tables["principals"].Columns[1]
	if !col.IsEnum || len(col.EnumValues) != 2 {
		t.Errorf("loaded principal_type not enriched: IsEnum=%v EnumValues=%v", col.IsEnum, col.EnumValues)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(raw) {
		t.Error("LoadJSON must not rewrite the persisted artifact")
	}
}

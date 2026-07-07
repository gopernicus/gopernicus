package pgxdb

import "testing"

func TestPrettyPrintSQL(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{
			name: "multi-line with tabs collapses to one line",
			sql: "SELECT id, name\n" +
				"\t\tFROM organizations\n" +
				"\t\tWHERE id = @id",
			want: "SELECT id, name FROM organizations WHERE id = @id",
		},
		{
			name: "collapses runs of spaces",
			sql:  "SELECT   id,    name   FROM   organizations",
			want: "SELECT id, name FROM organizations",
		},
		{
			name: "trims whitespace around parentheses",
			sql:  "INSERT INTO organizations ( id, slug, name )\nVALUES ( @id, @slug, @name )",
			want: "INSERT INTO organizations(id, slug, name)VALUES(@id, @slug, @name)",
		},
		{
			name: "trims leading and trailing whitespace",
			sql:  "\n\t  SELECT 1  \n\t",
			want: "SELECT 1",
		},
		{
			name: "empty string stays empty",
			sql:  "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PrettyPrintSQL(tt.sql); got != tt.want {
				t.Fatalf("PrettyPrintSQL(%q) = %q, want %q", tt.sql, got, tt.want)
			}
		})
	}
}

package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/workshop/codegen/schema"
)

// principalChildResolved builds a favorites-shaped entity: composite of a PK
// that is also a FK to principals (principal inheritance).
func principalChildResolved() *ResolvedFile {
	return &ResolvedFile{
		Table: &schema.TableInfo{
			TableName: "favorites",
			PrimaryKey: &schema.PrimaryKeyInfo{
				Column:  "principal_id",
				Columns: []string{"principal_id", "resource_id"},
			},
			ForeignKeys: []schema.ForeignKeyInfo{
				{Columns: []string{"principal_id"}, RefTable: "principals", RefColumns: []string{"principal_id"}, ColumnName: "principal_id"},
			},
		},
		EntityName:  "Favorite",
		EntityLower: "favorite",
		TableName:   "favorites",
		PackageName: "favorites",
		DomainName:  "access",
		PKColumn:    "principal_id",
		PKGoName:    "PrincipalID",
		PKGoType:    "string",
		AllColumns: []schema.ColumnInfo{
			{Name: "principal_id", DBType: "varchar", GoType: "string", IsPrimaryKey: true, IsForeignKey: true},
			{Name: "resource_id", DBType: "varchar", GoType: "string"},
		},
	}
}

// principalsFixtureResolved builds the principals feature entity with the
// CHECK ... IN constraint folded into EnumValues (as schema load does).
func principalsFixtureResolved() *ResolvedFile {
	return &ResolvedFile{
		Table:       &schema.TableInfo{TableName: "principals"},
		EntityName:  "Principal",
		EntityLower: "principal",
		TableName:   "principals",
		PackageName: "principals",
		DomainName:  "auth",
		PKColumn:    "principal_id",
		PKGoName:    "PrincipalID",
		PKGoType:    "string",
		AllColumns: []schema.ColumnInfo{
			{Name: "principal_id", DBType: "varchar", GoType: "string", IsPrimaryKey: true},
			{Name: "principal_type", DBType: "varchar(64)", GoType: "string", IsEnum: true,
				EnumValues: []string{"user", "service_account"}},
		},
	}
}

// The principal-inheritance insert must use a CHECK-valid principal_type —
// the child entity's own name (e.g. "favorite") violates
// principals_type_check.
func TestPrincipalInheritanceFixtureUsesCheckValidType(t *testing.T) {
	dir := t.TempDir()
	data := FixtureTemplateData{
		ModulePath: "example.com/app",
		Entities: []FixtureEntity{
			BuildFixtureEntity(principalsFixtureResolved(), "example.com/app"),
			BuildFixtureEntity(principalChildResolved(), "example.com/app"),
		},
	}
	if err := GenerateFixtures(data, dir, Options{}); err != nil {
		t.Fatalf("GenerateFixtures: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(out)

	if !strings.Contains(content, "INSERT INTO principals") {
		t.Fatal("expected principal-inheritance insert")
	}
	if strings.Contains(content, `"favorite")`) {
		t.Error(`principal insert must not use the child entity name "favorite"`)
	}
	if !strings.Contains(content, `principalID, "user")`) {
		t.Error(`principal insert must use the principals entity's CHECK-valid default "user"`)
	}
}

// Without a principals entity in the batch, the fallback is "user" — the
// first value the framework-shipped principals_type_check allows.
func TestPrincipalInheritanceFixtureFallsBackToUser(t *testing.T) {
	dir := t.TempDir()
	data := FixtureTemplateData{
		ModulePath: "example.com/app",
		Entities: []FixtureEntity{
			BuildFixtureEntity(principalChildResolved(), "example.com/app"),
		},
	}
	if err := GenerateFixtures(data, dir, Options{}); err != nil {
		t.Fatalf("GenerateFixtures: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"user")`) {
		t.Error(`expected "user" fallback for principal_type`)
	}
}

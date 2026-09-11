//go:build integration

package turso

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func tupleUpgradeLegacy(t *testing.T) *tursodb.DB {
	t.Helper()
	ctx := context.Background()
	url, token := requireTursoEnv(t)
	db := openAndMigrate(t, url, token)
	for _, table := range authorizationTables {
		if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+table+""); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range canonicalMigrations {
		if _, err := db.Exec(ctx, "DELETE FROM "+"schema_migrations"+" WHERE source = 'default' AND version = '"+name+"'"); err != nil {
			t.Fatal(err)
		}
	}
	before := fstest.MapFS{}
	for _, name := range canonicalMigrations {
		if name >= "0006_" {
			continue
		}
		data, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+name)
		if err != nil {
			t.Fatal(err)
		}
		before[MigrationsDir+"/"+name] = &fstest.MapFile{Data: data}
	}
	if err := tursodb.RunMigrations(ctx, db, before, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"iam_relationships", "iam_roles"} {
			if _, err := db.Exec(context.Background(), "DELETE FROM "+table); err != nil {
				t.Error(err)
			}
		}
		if err := tursodb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
			t.Error(err)
		}
	})

	return db
}

func TestTupleIdentityUpgrade(t *testing.T) {
	ctx := context.Background()
	db := tupleUpgradeLegacy(t)
	for _, statement := range []string{
		`INSERT INTO iam_relationships (relationship_id, resource_type, resource_id, relation, subject_type, subject_id, subject_relation, created_at) VALUES
          ('old-3', 'doc', 'd1', 'viewer', 'group', 'g', 'member', '2026-01-01T00:00:00Z'),
          ('old-2', 'doc', 'd1', 'viewer', 'group', 'g', 'admin', '2026-01-02T00:00:00Z'),
          ('old-1', 'doc', 'd1', 'viewer', 'group', 'g', '', '2026-01-03T00:00:00Z'),
          ('old-4', 'doc', 'd2', 'editor', 'group', 'g', 'member', '2026-01-04T00:00:00Z')`,
		`INSERT INTO iam_roles (subject_type, subject_id, role, resource_type, resource_id, created_at) VALUES
          ('user', 'alice', 'reader', 'doc', 'd1', '2026-01-01T00:00:00Z'),
          ('user', 'alice', 'reader', '', '', '2026-01-02T00:00:00Z')`,
	} {
		if _, err := db.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}

	var metadata int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM pragma_table_info('iam_relationships') WHERE name IN ('relationship_id','created_at')) + (SELECT count(*) FROM pragma_table_info('iam_roles') WHERE name='created_at')`).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata != 0 {
		t.Fatalf("removed metadata columns still present: %d", metadata)
	}
	rows, err := db.Query(ctx, `SELECT name FROM pragma_table_info('iam_relationships') WHERE pk > 0 ORDER BY pk`)
	if err != nil {
		t.Fatal(err)
	}
	var primaryKey []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		primaryKey = append(primaryKey, column)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"resource_type", "resource_id", "relation", "subject_type", "subject_id", "subject_relation"}; !slices.Equal(primaryKey, want) {
		t.Fatalf("primary key=%v, want %v", primaryKey, want)
	}

	repos, err := Repositories(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	req := list.Request{Limit: 1}
	var resources []relationships.SubjectRelationship
	for i := 0; i < 5; i++ {
		page, err := repos.Relationships.ListRelationshipsBySubject(ctx, "group", "g", relationships.SubjectRelationshipFilter{}, req)
		if err != nil {
			t.Fatal(err)
		}
		resources = append(resources, page.Items...)
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == req.Cursor {
			t.Fatal("tuple cursor did not advance")
		}
		req.Cursor = page.NextCursor
	}
	wantResources := []relationships.SubjectRelationship{
		{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectRelation: ""},
		{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectRelation: "admin"},
		{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectRelation: "member"},
		{ResourceType: "doc", ResourceID: "d2", Relation: "editor", SubjectRelation: "member"},
	}
	if !slices.Equal(resources, wantResources) {
		t.Fatalf("upgraded tuple walk=%+v, want %+v", resources, wantResources)
	}
	req = list.Request{Limit: 1}
	var subjects []relationships.ResourceRelationship
	for i := 0; i < 4; i++ {
		page, err := repos.Relationships.ListRelationshipsByResource(ctx, "doc", "d1", relationships.ResourceRelationshipFilter{}, req)
		if err != nil {
			t.Fatal(err)
		}
		subjects = append(subjects, page.Items...)
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == req.Cursor {
			t.Fatal("subject tuple cursor did not advance")
		}
		req.Cursor = page.NextCursor
	}
	wantSubjects := []relationships.ResourceRelationship{
		{SubjectType: "group", SubjectID: "g", Relation: "viewer", SubjectRelation: ""},
		{SubjectType: "group", SubjectID: "g", Relation: "viewer", SubjectRelation: "admin"},
		{SubjectType: "group", SubjectID: "g", Relation: "viewer", SubjectRelation: "member"},
	}
	if !slices.Equal(subjects, wantSubjects) {
		t.Fatalf("upgraded subject walk=%+v, want %+v", subjects, wantSubjects)
	}
	req = list.Request{Limit: 1}
	var assignments []roles.Assignment
	for i := 0; i < 3; i++ {
		page, err := repos.Roles.ListBySubject(ctx, "user", "alice", req)
		if err != nil {
			t.Fatal(err)
		}
		assignments = append(assignments, page.Items...)
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == req.Cursor {
			t.Fatal("role cursor did not advance")
		}
		req.Cursor = page.NextCursor
	}
	wantRoles := []roles.Assignment{
		{SubjectType: "user", SubjectID: "alice", Role: "reader"},
		{SubjectType: "user", SubjectID: "alice", Role: "reader", ResourceType: "doc", ResourceID: "d1"},
	}
	if !slices.Equal(assignments, wantRoles) {
		t.Fatalf("upgraded role walk=%+v, want %+v", assignments, wantRoles)
	}
	// The independent exclusive-subject rule still rejects a competing relation.
	statement := `INSERT INTO iam_relationships (resource_type, resource_id, relation, subject_type, subject_id, subject_relation) VALUES ('doc','d1','owner','group','g','member')`
	if _, err := db.Exec(ctx, statement); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("exclusive subject constraint: %v", err)
	}
}

func TestTupleIdentityUpgradeRejectsSeparators(t *testing.T) {
	for table, fields := range map[string][]string{
		"iam_relationships": {"resource_type", "resource_id", "relation", "subject_type", "subject_id", "subject_relation"},
		"iam_roles":         {"subject_type", "subject_id", "role", "resource_type", "resource_id"},
	} {
		for _, field := range fields {
			for _, invalid := range []string{"bad\x01value", "bad\x00value\x01suffix"} {
				label := "separator"
				if strings.ContainsRune(invalid, 0) {
					label = "nul_before_separator"
				}
				t.Run(table+"/"+field+"/"+label, func(t *testing.T) {
					ctx := context.Background()
					db := tupleUpgradeLegacy(t)
					for _, statement := range []string{
						`INSERT INTO iam_relationships (relationship_id, resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('legacy','doc','d1','viewer','group','g','member','2026-01-01T00:00:00Z')`,
						`INSERT INTO iam_roles (subject_type,subject_id,role,resource_type,resource_id,created_at) VALUES ('user','alice','reader','doc','d1','2026-01-01T00:00:00Z')`,
					} {
						if _, err := db.Exec(ctx, statement); err != nil {
							t.Fatal(err)
						}
					}
					before := invalid
					if _, err := db.Exec(ctx, "UPDATE "+table+" SET "+field+"=?", before); err != nil {
						t.Fatal(err)
					}
					err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir)
					if !errors.Is(err, sdk.ErrInvalidInput) {
						t.Fatalf("upgrade must reject reserved separator, got %v", err)
					}
					var id, created, after string
					if err := db.QueryRow(ctx, "SELECT relationship_id FROM "+"iam_relationships").Scan(&id); err != nil || id != "legacy" {
						t.Fatalf("old relationship metadata changed: id=%q err=%v", id, err)
					}
					if err := db.QueryRow(ctx, "SELECT CAST(created_at AS TEXT) FROM "+"iam_roles").Scan(&created); err != nil || created == "" {
						t.Fatalf("old role metadata changed: created=%q err=%v", created, err)
					}
					if err := db.QueryRow(ctx, "SELECT "+field+" FROM "+table).Scan(&after); err != nil || after != before {
						t.Fatalf("legacy value changed: got=%q err=%v", after, err)
					}
					var applied int
					if err := db.QueryRow(ctx, "SELECT count(*) FROM "+"schema_migrations"+" WHERE source='default' AND version='0006_iam_tuple_identity.sql'").Scan(&applied); err != nil || applied != 0 {
						t.Fatalf("failed migration recorded: count=%d err=%v", applied, err)
					}
				})
			}
		}
	}
}

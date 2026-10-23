//go:build integration

package turso

import (
	"context"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

func TestSchemaProbe(t *testing.T) {
	ctx := context.Background()
	db, _ := liveRepos(t)
	if err := probeTable(ctx, db, "iam_audit"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`INSERT INTO iam_tuples VALUES(2,'doc','d','','user','u','')`, `INSERT INTO iam_tuples VALUES(2,'doc','','editor','user','u','')`} {
		if _, err := db.Exec(ctx, statement); err == nil {
			t.Fatalf("constraint accepted invalid fact: %s", statement)
		}
	}
}

// TestSchemaProbeRequiresMigration0005 returns the live database's audit and
// operation tables to their 0004 shape: the store refuses them until 0005 runs.
func TestSchemaProbeRequiresMigration0005(t *testing.T) {
	url, token := requireTursoEnv(t)
	db := openAndMigrate(t, url, token)
	primary, err := MigrationsFS.ReadFile(MigrationsDir + "/0001_iam_tuples.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(primary), "CREATE TABLE main.iam_audit (")
	if start < 0 {
		t.Fatal("0001 no longer creates iam_audit")
	}
	if _, err := db.Exec(t.Context(), `DROP TABLE main.iam_audit; DROP TABLE main.iam_operations;
DELETE FROM main.schema_migrations WHERE source='default' AND version IN ('0003_iam_operations.sql', '0004_iam_operations_fenced.sql', '0005_iam_audit_integrity.sql', '0006_iam_audit_immutable.sql', '0007_iam_operations_expiry.sql');
`+string(primary[start:])); err != nil {
		t.Fatal(err)
	}
	if err := tursodb.RunMigrations(t.Context(), db, migrationsThrough(t, "0004_iam_operations_fenced.sql"), MigrationsDir); err != nil {
		t.Fatal(err)
	}
	assertMigration0005Required(t, db)
}

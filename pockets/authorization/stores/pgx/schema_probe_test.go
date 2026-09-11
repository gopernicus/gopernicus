package pgx

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

func TestSchemaProbe(t *testing.T) {
	ctx := context.Background()
	db, _ := liveRepos(t)
	if err := pgxdb.ProbeTable(ctx, db, qualify(t, "iam_audit")); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"iam_scopes", "iam_mutations"} {
		if err := pgxdb.ProbeTable(ctx, db, qualify(t, table)); err == nil {
			t.Fatalf("retired table remains: %s", table)
		}
	}
	for _, statement := range []string{`INSERT INTO iam_relationships(resource_type,resource_id,relation,subject_type,subject_id) VALUES('doc','d','','user','u')`, `INSERT INTO iam_roles(subject_type,subject_id,role,resource_type,resource_id) VALUES('user','u','editor','doc','')`} {
		if _, err := db.Exec(ctx, qualifySQL(t, statement)); err == nil {
			t.Fatalf("constraint accepted invalid fact: %s", statement)
		}
	}
}

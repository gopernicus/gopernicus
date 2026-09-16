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
	for _, statement := range []string{`INSERT INTO iam_tuples VALUES(2,'doc','d','','user','u','')`, `INSERT INTO iam_tuples VALUES(2,'doc','','editor','user','u','')`} {
		if _, err := db.Exec(ctx, qualifySQL(t, statement)); err == nil {
			t.Fatalf("constraint accepted invalid fact: %s", statement)
		}
	}
}

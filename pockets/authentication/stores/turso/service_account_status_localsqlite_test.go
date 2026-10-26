package turso

import (
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
)

// TestServiceAccountStatusSchema_LocalSQLite pins the owner ruling on 0027,
// untagged so `make check` runs it: without the table Repositories still
// constructs (0027 is never probed in every mode), and the status store's
// ProbeSchema — the pocket's check under AdministrationConfig.
// ServiceAccountStatus — refuses with sdk.ErrNotFound, naming the file.
func TestServiceAccountStatusSchema_LocalSQLite(t *testing.T) {
	db, _, _ := oauthSQLite(t)
	if _, err := db.Exec(t.Context(), "DROP TABLE "+serviceAccountStatusTable); err != nil {
		t.Fatalf("drop %s: %v", serviceAccountStatusTable, err)
	}
	repos, err := Repositories(t.Context(), db)
	if err != nil {
		t.Fatalf("Repositories without 0027 = %v, want it to construct (the status is opt-in)", err)
	}
	storetest.RunServiceAccountStatusSchemaProbe(t, func(*testing.T) serviceaccount.StatusRepository { return repos.ServiceAccountStatus })
	if err := repos.ServiceAccountStatus.ProbeSchema(t.Context()); err == nil || !strings.Contains(err.Error(), serviceAccountStatusMigration) {
		t.Fatalf("ProbeSchema = %v, want an error naming %s", err, serviceAccountStatusMigration)
	}
}

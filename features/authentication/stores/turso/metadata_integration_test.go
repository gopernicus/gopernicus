//go:build integration

// Integration tests for the invitation-metadata column that need raw SQL access
// the dialect-agnostic storetest suite cannot express: the populated-table
// migration upgrade (a row with no metadata reads back as the '{}' default) and
// the malformed-stored-JSON path (a corrupt column fails the read, never a silent
// empty). Run with -tags=integration and TURSO_DATABASE_URL/TURSO_AUTH_TOKEN.
package turso

import (
	"context"
	"os"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

func TestInvitationMetadataUpgradeAndMalformed_Turso(t *testing.T) {
	url := os.Getenv("TURSO_DATABASE_URL")
	token := os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" || token == "" {
		t.Skip("TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso metadata upgrade NOT verified")
	}
	ctx := context.Background()

	db, err := tursodb.Open(tursodb.Config{URL: url, AuthToken: token})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db)
	t.Cleanup(func() { truncate(t, db) })

	repos, err := Repositories(db)
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	repo := repos.Invitations

	// Populated-table upgrade: insert a row the OLD way — every column EXCEPT
	// metadata — so the ALTER's DEFAULT '{}' is what fills it, exactly as a
	// pre-migration row upgrades. It must read back as a non-nil empty map.
	now := tursodb.FormatTime(time.Now().UTC())
	if _, err := db.Exec(ctx, `INSERT INTO invitations
		(id, resource_type, resource_id, relation, identifier, identifier_kind, resolved_subject_id,
		 invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at)
		VALUES ('leg-1','project','p1','member','legacy@x.com','email','','inviter','hash-leg-1',0,'pending',?,NULL,?,?)`,
		now, now, now); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	got, err := repo.Get(ctx, "leg-1")
	if err != nil {
		t.Fatalf("Get legacy row: %v", err)
	}
	if got.Metadata == nil || len(got.Metadata) != 0 {
		t.Errorf("legacy-row metadata = %#v, want non-nil empty map", got.Metadata)
	}

	// Malformed stored JSON must fail the read, never surface as a silent empty map.
	if _, err := db.Exec(ctx, `UPDATE invitations SET metadata = ? WHERE id = ?`, "not json", "leg-1"); err != nil {
		t.Fatalf("corrupt metadata: %v", err)
	}
	if _, err := repo.Get(ctx, "leg-1"); err == nil {
		t.Error("Get with malformed stored metadata succeeded, want a decode error")
	}
}

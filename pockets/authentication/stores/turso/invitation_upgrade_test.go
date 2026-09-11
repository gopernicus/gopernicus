//go:build integration

package turso

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
)

// Exercise the actual upgrade of a populated pre-0018 database, not an insert
// after the new column already exists. The host applies migrations before boot.
func TestInvitationAcceptanceUpgrade(t *testing.T) {
	ctx := context.Background()
	db := probeDial(t)

	probeDropAll(t, db)
	t.Cleanup(func() { probeResetSchema(t, db) })
	prior := fstest.MapFS{}
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() > "0016_invitation_metadata.sql" {
			continue
		}
		name := MigrationsDir + "/" + entry.Name()
		data, err := fs.ReadFile(MigrationsFS, name)
		if err != nil {
			t.Fatal(err)
		}
		prior[name] = &fstest.MapFile{Data: data}
	}
	if err := tursodb.RunMigrations(ctx, db, prior, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Exec(ctx, `INSERT INTO invitations
 (id,resource_type,resource_id,relation,identifier,identifier_kind,resolved_subject_id,invited_by,token_hash,auto_accept,status,expires_at,created_at,updated_at,metadata)
 VALUES ('existing-invitation','project','legacy','member','legacy@example.com','email','','inviter','preserved-token',1,'pending',?,?,?,'{"source":"legacy"}')`, tursodb.FormatTime(now.Add(time.Hour)), tursodb.FormatTime(now), tursodb.FormatTime(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(context.Background(), db); !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), "0018_invitation_acceptance.sql") {
		t.Fatalf("boot before 0018: %v", err)
	}
	if err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(context.Background(), db)
	if err != nil {
		t.Fatalf("boot after 0018: %v", err)
	}
	got, err := repos.Invitations.GetByTokenHash(ctx, "preserved-token")
	if err != nil || got.ID != "existing-invitation" || got.Status != invitations.StatusPending || got.Identifier != "legacy@example.com" || got.Metadata["source"] != "legacy" || got.ResolvedSubjectType != "" || !got.CreatedAt.Equal(now) {
		t.Fatalf("upgrade changed pending invitation: %+v %v", got, err)
	}
	claim := invitations.Acceptance{TokenHash: got.TokenHash, SubjectType: "user", SubjectID: "acceptor", Now: now.Add(time.Minute)}
	claimed, err := repos.Invitations.ClaimAcceptance(ctx, got.ID, claim)
	if err != nil || claimed.Status != invitations.StatusAccepting || claimed.ResolvedSubjectType != "user" || claimed.ResolvedSubjectID != "acceptor" {
		t.Fatalf("upgraded row claim: %+v %v", claimed, err)
	}
	accepted, err := repos.Invitations.CompleteAcceptance(ctx, got.ID, claim)
	if err != nil || accepted.Status != invitations.StatusAccepted {
		t.Fatalf("upgraded row finalization: %+v %v", accepted, err)
	}
}

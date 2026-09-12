//go:build integration && !live

package firestore

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The two service-account properties the conformance suite does not assert
// because they are STORE decisions rather than port contracts: the constraint
// this store deliberately does NOT enforce, and the columns its Update
// deliberately does not touch.

// TestServiceAccountNamesAreNotUnique is the audited non-constraint (SCHEMA.md
// §5.9). Migration 0006 puts no unique index on `name`, so two accounts may
// share one — and a Firestore adapter that "helpfully" claimed the name would be
// STRICTER than both SQL siblings, which is a divergence no port case would
// catch (the suite never creates two accounts with the same name).
func TestServiceAccountNamesAreNotUnique(t *testing.T) {
	r, _ := openRepos(t)
	ctx := context.Background()

	first, err := serviceaccount.New(dbGenerated, "deployer", "", "admin", false, "", testBase)
	if err != nil {
		t.Fatalf("serviceaccount.New: %v", err)
	}
	createdFirst, err := r.ServiceAccounts.Create(ctx, first)
	if err != nil {
		t.Fatalf("Create(first): %v", err)
	}

	second, err := serviceaccount.New(dbGenerated, "deployer", "", "admin", false, "", testBase.Add(time.Minute))
	if err != nil {
		t.Fatalf("serviceaccount.New: %v", err)
	}
	createdSecond, err := r.ServiceAccounts.Create(ctx, second)
	if err != nil {
		t.Fatalf("Create(second) with a duplicate name: %v — name carries no unique index", err)
	}
	if createdFirst.ID == createdSecond.ID {
		t.Fatalf("the two accounts share an id: %s", createdFirst.ID)
	}

	page, err := r.ServiceAccounts.List(ctx, list.Request{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("the directory holds %d accounts, want both same-named rows", len(page.Items))
	}
}

// TestServiceAccountUpdateLeavesCreatedAtAlone pins the column list. Update is a
// FIELD update precisely so id and created_at survive it — the SQL adapters'
// `UPDATE … SET name=?, …` names neither. A whole-document Set built from the
// caller's value would rewrite created_at from whatever the caller happened to
// carry, silently reordering the account in every page that follows.
func TestServiceAccountUpdateLeavesCreatedAtAlone(t *testing.T) {
	r, _ := openRepos(t)
	ctx := context.Background()

	sa, err := serviceaccount.New(dbGenerated, "bot", "before", "admin", false, "", testBase)
	if err != nil {
		t.Fatalf("serviceaccount.New: %v", err)
	}
	created, err := r.ServiceAccounts.Create(ctx, sa)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A caller handing back a DIFFERENT created_at must not move the row.
	changed := created
	changed.Description = "after"
	changed.CreatedAt = testBase.Add(72 * time.Hour)
	changed.UpdatedAt = testBase.Add(time.Hour)
	if _, err := r.ServiceAccounts.Update(ctx, created.ID, changed); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := r.ServiceAccounts.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.Description != "after" {
		t.Errorf("Description = %q, want the update to have landed", got.Description)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreatedAt = %v, want the original %v — Update must not touch it", got.CreatedAt, created.CreatedAt)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	if !got.UpdatedAt.Equal(testBase.Add(time.Hour)) {
		t.Errorf("UpdatedAt = %v, want the value the caller supplied", got.UpdatedAt)
	}
}

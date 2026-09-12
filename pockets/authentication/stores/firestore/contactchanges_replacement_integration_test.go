//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

// The contact-change cases the conformance suite cannot reach: the DOCUMENT the
// replacement is keyed by. This collection carries no claim, so its whole
// uniqueness argument is "the id IS the (user, kind) pair" (SCHEMA.md §3.12,
// §5.10) — a store that keyed it by the surrogate id would pass every port case
// on a fresh database and accumulate two pending values per pair in production.

// TestReplacementIsTheDocumentIdentity proves the keying directly: two Creates
// for one (user, kind) write ONE document, the second value wins, and the row's
// surrogate id is a FIELD that moves with it rather than the key.
func TestReplacementIsTheDocumentIdentity(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	const userID = "u-key"
	first, err := r.ContactChanges.Create(ctx, contactchange.New(userID, identifier.KindEmail, "first@example.com",
		identifier.Uses{Login: true, Recovery: true}, true, "", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Create #1: %v", err)
	}
	if first.ID == "" {
		t.Fatal("Create returned an empty id — the store did not assign one")
	}

	stored, found, err := readContactChange(ctx, db, db.ReaderFrom(ctx), userID, identifier.KindEmail)
	if err != nil || !found {
		t.Fatalf("readContactChange after #1: found=%v err=%v", found, err)
	}
	if stored.ID != first.ID {
		t.Errorf("stored id = %q, want the returned %q (the surrogate id is a field, and it round-trips)", stored.ID, first.ID)
	}

	second, err := r.ContactChanges.Create(ctx, contactchange.New(userID, identifier.KindEmail, "second@example.com",
		identifier.Uses{Login: true}, false, "old-ident", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Create #2: %v", err)
	}
	if second.ID == first.ID {
		t.Error("the replacement reused the displaced row's id — each Create mints its own")
	}

	stored, found, err = readContactChange(ctx, db, db.ReaderFrom(ctx), userID, identifier.KindEmail)
	if err != nil || !found {
		t.Fatalf("readContactChange after #2: found=%v err=%v", found, err)
	}
	if stored.ID != second.ID || stored.NewValue != "second@example.com" {
		t.Errorf("stored row = {id:%q value:%q}, want the second change", stored.ID, stored.NewValue)
	}
	// The whole row was replaced, not merged: the first change's intent is gone.
	if stored.MakePrimary || stored.ReplacesIdentifierID != "old-ident" || stored.RecoveryEnabled {
		t.Errorf("stored row kept fields from the displaced change: %+v", stored)
	}

	// A different kind for the same user is a DIFFERENT document and is untouched.
	if _, err := r.ContactChanges.Create(ctx, contactchange.New(userID, identifier.KindPhone, "+15551112222",
		identifier.Uses{Notification: true}, false, "", time.Hour, time.Now())); err != nil {
		t.Fatalf("Create(phone): %v", err)
	}
	if _, found, err := readContactChange(ctx, db, db.ReaderFrom(ctx), userID, identifier.KindEmail); err != nil || !found {
		t.Errorf("the email change was displaced by a phone change: found=%v err=%v", found, err)
	}
}

// TestExpiredConsumeCommitsItsDeletion is the committed-outcome contract at the
// document level (N-D2). An expired pending change is DELETED and sdk.ErrExpired
// is reported afterwards — returning the sentinel from inside the transaction
// would roll the deletion back and leave the stale value consumable forever. The
// second Consume proving sdk.ErrNotFound is the port's view of it; the absent
// document is the store's.
func TestExpiredConsumeCommitsItsDeletion(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	const userID = "u-expired-consume"
	created, err := r.ContactChanges.Create(ctx, contactchange.New(userID, identifier.KindEmail, "stale@example.com",
		identifier.Uses{Login: true}, false, "", -time.Minute, time.Now()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := r.ContactChanges.Consume(ctx, userID, identifier.KindEmail, created.ID); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("Consume(expired): err=%v, want ErrExpired", err)
	}
	if _, found, err := readContactChange(ctx, db, db.ReaderFrom(ctx), userID, identifier.KindEmail); err != nil || found {
		t.Errorf("the expired row survived its own consume: found=%v err=%v", found, err)
	}
	if _, err := r.ContactChanges.Consume(ctx, userID, identifier.KindEmail, created.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Consume after the expiry deletion: err=%v, want ErrNotFound", err)
	}
}

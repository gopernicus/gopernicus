// Package storetest is the executable form of the vault.Repository contract.
// Every store runs RunRepository: the memory reference here and each driver
// module. It imports the stdlib, sdk and this pocket only.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/sdk"
)

// base is a fixed instant the cases stamp from.
var base = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

var (
	globex = vault.Owner{Type: "tenant", ID: "globex"}
	acme   = vault.Owner{Type: "tenant", ID: "acme"}
	system = vault.Source{System: "storetest"}
)

// RunRepository exercises a vault.Repository. newRepo MUST return a clean,
// isolated repository per call.
func RunRepository(t *testing.T, newRepo func(t *testing.T) vault.Repository) {
	t.Helper()
	t.Run("OwnerKeyLifecycle", func(t *testing.T) { testOwnerKeyLifecycle(t, newRepo(t)) })
	t.Run("ReplaceOwnerKeyReseals", func(t *testing.T) { testReplaceReseals(t, newRepo(t)) })
	t.Run("ListOwnerKeysPages", func(t *testing.T) { testListOwnerKeys(t, newRepo(t)) })
	t.Run("SecretVersions", func(t *testing.T) { testSecretVersions(t, newRepo(t)) })
	t.Run("SecretNeedsOwnerKey", func(t *testing.T) { testSecretNeedsOwnerKey(t, newRepo(t)) })
	t.Run("GetMissingRecordsNothing", func(t *testing.T) { testGetMissing(t, newRepo(t)) })
	t.Run("DeleteSecretAndOwner", func(t *testing.T) { testDelete(t, newRepo(t)) })
	t.Run("AuditIsAtomic", func(t *testing.T) { testAuditAtomic(t, newRepo(t)) })
	t.Run("AuditOrderFilterPage", func(t *testing.T) { testAuditListing(t, newRepo(t)) })
	t.Run("TimesAreUTCMicroseconds", func(t *testing.T) { testTimes(t, newRepo(t)) })
	t.Run("AppendAuditAtomic", func(t *testing.T) { testAppendAuditAtomic(t, newRepo(t)) })
	t.Run("GetRecordsOutcomeAndVersion", func(t *testing.T) { testGetRecordsOutcomeAndVersion(t, newRepo(t)) })
	t.Run("DeleteOwnerConflictsOnShrink", func(t *testing.T) { testDeleteOwnerConflictsOnShrink(t, newRepo(t)) })
	t.Run("DeleteOwnerConflictsOnGrowth", func(t *testing.T) { testDeleteOwnerConflictsOnGrowth(t, newRepo(t)) })
	t.Run("DeleteOwnerRecordsReturnedVersion", func(t *testing.T) { testDeleteOwnerRecordsReturnedVersion(t, newRepo(t)) })
	t.Run("CorrelationRoundTrip", func(t *testing.T) { testCorrelationRoundTrip(t, newRepo(t)) })
}

func key(owner vault.Owner, id string) vault.OwnerKey {
	return vault.OwnerKey{Owner: owner, KeyID: id, WrappedKey: "wrapped-" + id, WrapperKeyID: "m1", CreatedAt: base, RotatedAt: base}
}

func record(t *testing.T, at time.Time, action vault.Action, owner vault.Owner, name string) vault.AuditRecord {
	t.Helper()
	r, err := vault.NewAuditRecord(at, action, owner, name, 0, "", system)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func sealed(owner vault.Owner, name, keyID, ciphertext string, at time.Time) vault.SealedSecret {
	return vault.SealedSecret{Ref: vault.Ref{Owner: owner, Name: name}, KeyID: keyID, Ciphertext: ciphertext, CreatedAt: at, UpdatedAt: at}
}

func allAudit(t *testing.T, repo vault.Repository, filter vault.AuditFilter) []vault.AuditRecord {
	t.Helper()
	var out []vault.AuditRecord
	for after := ""; ; {
		page, next, err := repo.ListAudit(context.Background(), filter, after, 3)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, page...)
		if next == "" {
			return out
		}
		after = next
	}
}

func testOwnerKeyLifecycle(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	if _, err := repo.GetOwnerKey(ctx, globex); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("absent key: want ErrNotFound, got %v", err)
	}
	k1 := key(globex, "k1")
	if err := repo.CreateOwnerKey(ctx, k1, []vault.AuditRecord{record(t, base, vault.ActionOwnerKeyCreated, globex, "")}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateOwnerKey(ctx, key(globex, "k9"), []vault.AuditRecord{record(t, base, vault.ActionOwnerKeyCreated, globex, "")}); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("second key: want ErrAlreadyExists, got %v", err)
	}
	got, err := repo.GetOwnerKey(ctx, globex)
	if err != nil || got != k1 {
		t.Fatalf("get: %+v %v", got, err)
	}
	k2 := key(globex, "k2")
	k2.RotatedAt = base.Add(time.Hour)
	if err := repo.ReplaceOwnerKey(ctx, k2, "wrong", nil, []vault.AuditRecord{record(t, base, vault.ActionOwnerKeyRewrapped, globex, "")}); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale expectation: want ErrConflict, got %v", err)
	}
	if err := repo.ReplaceOwnerKey(ctx, key(acme, "a1"), "a0", nil, []vault.AuditRecord{record(t, base, vault.ActionOwnerKeyRewrapped, acme, "")}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("replace absent: want ErrNotFound, got %v", err)
	}
	if err := repo.ReplaceOwnerKey(ctx, k2, "k1", nil, []vault.AuditRecord{record(t, base, vault.ActionOwnerKeyRotated, globex, "")}); err != nil {
		t.Fatal(err)
	}
	if got, err = repo.GetOwnerKey(ctx, globex); err != nil || got != k2 {
		t.Fatalf("after replace: %+v %v", got, err)
	}
	if n := len(allAudit(t, repo, vault.AuditFilter{Owner: &globex})); n != 2 {
		t.Fatalf("want 2 audit records (created, rotated), got %d", n)
	}
}

func testReplaceReseals(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	if err := repo.CreateOwnerKey(ctx, key(globex, "k1"), nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := repo.PutSecret(ctx, sealed(globex, name, "k1", "ct-"+name, base), record(t, base, vault.ActionSecretPut, globex, name)); err != nil {
			t.Fatal(err)
		}
	}
	later := base.Add(time.Minute)
	resealed := []vault.SealedSecret{sealed(globex, "a", "k2", "ct2-a", later), sealed(globex, "b", "k2", "ct2-b", later)}
	ghost := append(resealed, sealed(globex, "zz", "k2", "ct2-zz", later))
	if err := repo.ReplaceOwnerKey(ctx, key(globex, "k2"), "k1", ghost, nil); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("resealing an absent secret: want ErrNotFound, got %v", err)
	}
	if got, _ := repo.GetOwnerKey(ctx, globex); got.KeyID != "k1" {
		t.Fatalf("failed replace must change nothing, key is %q", got.KeyID)
	}
	if err := repo.ReplaceOwnerKey(ctx, key(globex, "k2"), "k1", resealed, nil); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListSealedSecrets(ctx, globex)
	if err != nil || len(got) != 2 {
		t.Fatalf("list sealed: %+v %v", got, err)
	}
	for i, want := range resealed {
		g := got[i]
		if g.Ref != want.Ref || g.KeyID != "k2" || g.Ciphertext != want.Ciphertext || g.Version != 1 || !g.UpdatedAt.Equal(later) || !g.CreatedAt.Equal(base) {
			t.Fatalf("resealed %d: %+v", i, g)
		}
	}
}

func testListOwnerKeys(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	var want []string
	for _, id := range []string{"c", "a", "b", "d", "e"} {
		o := vault.Owner{Type: "tenant", ID: id}
		if err := repo.CreateOwnerKey(ctx, key(o, "k"), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CreateOwnerKey(ctx, key(vault.Owner{Type: "site", ID: "z"}, "k"), nil); err != nil {
		t.Fatal(err)
	}
	want = []string{"site:z", "tenant:a", "tenant:b", "tenant:c", "tenant:d", "tenant:e"}
	var got []string
	pages := 0
	for after := ""; ; {
		keys, next, err := repo.ListOwnerKeys(ctx, after, 4)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, k := range keys {
			got = append(got, k.Owner.String())
		}
		if next == "" {
			break
		}
		after = next
	}
	if fmt.Sprint(got) != fmt.Sprint(want) || pages != 2 {
		t.Fatalf("owners %v in %d pages, want %v in 2", got, pages, want)
	}
	if _, _, err := repo.ListOwnerKeys(ctx, "", 0); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("zero limit: %v", err)
	}
}

func testSecretVersions(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	if err := repo.CreateOwnerKey(ctx, key(globex, "k1"), nil); err != nil {
		t.Fatal(err)
	}
	ref := vault.Ref{Owner: globex, Name: "cloudflare.api_token"}
	first, err := repo.PutSecret(ctx, sealed(globex, ref.Name, "k1", "ct1", base), record(t, base, vault.ActionSecretPut, globex, ref.Name))
	if err != nil || first.Version != 1 || first.Ciphertext != "ct1" || !first.CreatedAt.Equal(base) {
		t.Fatalf("first put: %+v %v", first, err)
	}
	later := base.Add(time.Minute)
	second, err := repo.PutSecret(ctx, sealed(globex, ref.Name, "k1", "ct2", later), record(t, later, vault.ActionSecretPut, globex, ref.Name))
	if err != nil || second.Version != 2 || second.Ciphertext != "ct2" || !second.CreatedAt.Equal(base) || !second.UpdatedAt.Equal(later) {
		t.Fatalf("second put: %+v %v", second, err)
	}
	got, err := repo.GetSecret(ctx, ref, []vault.AuditRecord{record(t, later, vault.ActionSecretRead, globex, ref.Name)})
	if err != nil || got != second {
		t.Fatalf("get: %+v %v", got, err)
	}
	meta, err := repo.ListSecrets(ctx, globex)
	if err != nil || len(meta) != 1 || meta[0].Version != 2 || meta[0].KeyID != "k1" || meta[0].Ref != ref {
		t.Fatalf("list: %+v %v", meta, err)
	}
	puts := allAudit(t, repo, vault.AuditFilter{Action: vault.ActionSecretPut})
	if len(puts) != 2 || puts[0].Version != 1 || puts[1].Version != 2 || puts[1].Name != ref.Name {
		t.Fatalf("put audit must carry the assigned version: %+v", puts)
	}
	if reads := allAudit(t, repo, vault.AuditFilter{Action: vault.ActionSecretRead}); len(reads) != 1 {
		t.Fatalf("read audit: %+v", reads)
	}
}

func testSecretNeedsOwnerKey(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	_, err := repo.PutSecret(ctx, sealed(globex, "x", "k1", "ct", base), record(t, base, vault.ActionSecretPut, globex, "x"))
	if !errors.Is(err, sdk.ErrInvalidReference) {
		t.Fatalf("put without owner key: want ErrInvalidReference, got %v", err)
	}
	if n := len(allAudit(t, repo, vault.AuditFilter{})); n != 0 {
		t.Fatalf("failed put recorded %d audit rows", n)
	}
}

func testGetMissing(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	if err := repo.CreateOwnerKey(ctx, key(globex, "k1"), nil); err != nil {
		t.Fatal(err)
	}
	ref := vault.Ref{Owner: globex, Name: "missing"}
	if _, err := repo.GetSecret(ctx, ref, []vault.AuditRecord{record(t, base, vault.ActionSecretRead, globex, ref.Name)}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if n := len(allAudit(t, repo, vault.AuditFilter{})); n != 0 {
		t.Fatalf("missing read recorded %d audit rows", n)
	}
	if err := repo.DeleteSecret(ctx, ref, []vault.AuditRecord{record(t, base, vault.ActionSecretDeleted, globex, ref.Name)}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("delete missing: want ErrNotFound, got %v", err)
	}
	if err := repo.DeleteOwner(ctx, acme, nil, []vault.AuditRecord{record(t, base, vault.ActionOwnerKeyDestroyed, acme, "")}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("delete owner without key: want ErrNotFound, got %v", err)
	}
}

func testDelete(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	for _, o := range []vault.Owner{globex, acme} {
		if err := repo.CreateOwnerKey(ctx, key(o, "k1"), nil); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"a", "b"} {
			if _, err := repo.PutSecret(ctx, sealed(o, name, "k1", "ct", base), record(t, base, vault.ActionSecretPut, o, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := repo.DeleteSecret(ctx, vault.Ref{Owner: globex, Name: "a"}, []vault.AuditRecord{record(t, base, vault.ActionSecretDeleted, globex, "a")}); err != nil {
		t.Fatal(err)
	}
	if left, _ := repo.ListSecrets(ctx, globex); len(left) != 1 || left[0].Ref.Name != "b" {
		t.Fatalf("after delete: %+v", left)
	}
	if got := allAudit(t, repo, vault.AuditFilter{Owner: &globex, Action: vault.ActionSecretDeleted}); len(got) != 1 || got[0].Version != 1 || got[0].KeyID != "k1" {
		t.Fatalf("delete must record the version and key it removed: %+v", got)
	}
	if err := repo.DeleteOwner(ctx, globex, []string{"b"}, []vault.AuditRecord{record(t, base, vault.ActionSecretDeleted, globex, "b"), record(t, base, vault.ActionOwnerKeyDestroyed, globex, "")}); err != nil {
		t.Fatal(err)
	}
	if left, _ := repo.ListSecrets(ctx, globex); len(left) != 0 {
		t.Fatalf("owner secrets survive: %+v", left)
	}
	if _, err := repo.GetOwnerKey(ctx, globex); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("owner key survives: %v", err)
	}
	if left, _ := repo.ListSecrets(ctx, acme); len(left) != 2 {
		t.Fatalf("other owner touched: %+v", left)
	}
	if n := len(allAudit(t, repo, vault.AuditFilter{Owner: &globex})); n != 5 {
		t.Fatalf("globex audit: want 2 puts + 2 deletes + destroyed = 5, got %d", n)
	}
}

// testAuditAtomic makes the audit append fail (a duplicate record id) and
// checks that the change it was to accompany did not happen.
func testAuditAtomic(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	created := record(t, base, vault.ActionOwnerKeyCreated, globex, "")
	if err := repo.CreateOwnerKey(ctx, key(globex, "k1"), []vault.AuditRecord{created}); err != nil {
		t.Fatal(err)
	}
	dup := created
	dup.Action = vault.ActionSecretPut
	if _, err := repo.PutSecret(ctx, sealed(globex, "a", "k1", "ct", base), dup); err == nil {
		t.Fatal("duplicate audit id accepted")
	}
	if left, _ := repo.ListSecrets(ctx, globex); len(left) != 0 {
		t.Fatalf("secret written although its audit failed: %+v", left)
	}
	if err := repo.ReplaceOwnerKey(ctx, key(globex, "k2"), "k1", nil, []vault.AuditRecord{dup}); err == nil {
		t.Fatal("duplicate audit id accepted on replace")
	}
	if got, _ := repo.GetOwnerKey(ctx, globex); got.KeyID != "k1" {
		t.Fatalf("key replaced although its audit failed: %q", got.KeyID)
	}
	if _, err := repo.PutSecret(ctx, sealed(globex, "a", "k1", "ct", base), record(t, base, vault.ActionSecretPut, globex, "a")); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteSecret(ctx, vault.Ref{Owner: globex, Name: "a"}, []vault.AuditRecord{dup}); err == nil {
		t.Fatal("duplicate audit id accepted on delete")
	}
	if left, _ := repo.ListSecrets(ctx, globex); len(left) != 1 {
		t.Fatalf("secret deleted although its audit failed: %+v", left)
	}
	if err := repo.DeleteOwner(ctx, globex, []string{"a"}, []vault.AuditRecord{dup}); err == nil {
		t.Fatal("duplicate audit id accepted on delete owner")
	}
	if _, err := repo.GetOwnerKey(ctx, globex); err != nil {
		t.Fatalf("owner destroyed although its audit failed: %v", err)
	}
	if n := len(allAudit(t, repo, vault.AuditFilter{})); n != 2 {
		t.Fatalf("want exactly the two successful records, got %d", n)
	}
}

func testAuditListing(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	if err := repo.CreateOwnerKey(ctx, key(globex, "k1"), nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateOwnerKey(ctx, key(acme, "k1"), nil); err != nil {
		t.Fatal(err)
	}
	user := vault.Source{ActorType: "user", ActorID: "u1", Reason: "verify token"}
	var want []string
	// Written newest first to prove ordering is by time, not insertion.
	for i := 6; i >= 0; i-- {
		owner, src := globex, system
		if i%2 == 1 {
			owner, src = acme, user
		}
		r, err := vault.NewAuditRecord(base.Add(time.Duration(i)*time.Second), vault.ActionSecretPut, owner, "n", 0, "k1", src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.PutSecret(ctx, sealed(owner, "n", "k1", "ct", base), r); err != nil {
			t.Fatal(err)
		}
		want = append([]string{r.ID}, want...)
	}
	all := allAudit(t, repo, vault.AuditFilter{})
	if len(all) != 7 {
		t.Fatalf("want 7, got %d", len(all))
	}
	for i, r := range all {
		if r.ID != want[i] {
			t.Fatalf("order at %d: %s, want %s", i, r.ID, want[i])
		}
		if r.Source.Reason != "" && r.Source != user {
			t.Fatalf("source round trip: %+v", r.Source)
		}
	}
	cases := map[string]struct {
		filter vault.AuditFilter
		want   int
	}{
		"owner":      {vault.AuditFilter{Owner: &acme}, 3},
		"actor":      {vault.AuditFilter{ActorType: "user", ActorID: "u1"}, 3},
		"system":     {vault.AuditFilter{System: "storetest"}, 4},
		"name":       {vault.AuditFilter{Name: "n"}, 7},
		"other name": {vault.AuditFilter{Name: "zz"}, 0},
		"action":     {vault.AuditFilter{Action: vault.ActionOwnerKeyCreated}, 0},
		"window":     {vault.AuditFilter{Since: base.Add(2 * time.Second), Until: base.Add(5 * time.Second)}, 3},
	}
	for name, c := range cases {
		if got := len(allAudit(t, repo, c.filter)); got != c.want {
			t.Errorf("%s: got %d, want %d", name, got, c.want)
		}
	}
	if _, _, err := repo.ListAudit(ctx, vault.AuditFilter{Action: "bogus"}, "", 10); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("bad filter: %v", err)
	}
}

func testTimes(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	zone := time.FixedZone("test", 3600)
	odd := time.Date(2026, 10, 16, 13, 0, 0, 123456789, zone)
	k := key(globex, "k1")
	k.CreatedAt, k.RotatedAt = odd, odd
	r := record(t, odd, vault.ActionOwnerKeyCreated, globex, "")
	if err := repo.CreateOwnerKey(ctx, k, []vault.AuditRecord{r}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetOwnerKey(ctx, globex)
	if err != nil {
		t.Fatal(err)
	}
	wantTime := odd.UTC().Truncate(time.Microsecond)
	if !got.CreatedAt.Equal(wantTime) || got.CreatedAt.Location() != time.UTC || got.RotatedAt.Location() != time.UTC {
		t.Fatalf("owner key times: %v %v", got.CreatedAt, got.RotatedAt)
	}
	sec, err := repo.PutSecret(ctx, sealed(globex, "a", "k1", "ct", odd), record(t, odd, vault.ActionSecretPut, globex, "a"))
	if err != nil || !sec.CreatedAt.Equal(wantTime) || sec.CreatedAt.Location() != time.UTC || sec.UpdatedAt.Location() != time.UTC {
		t.Fatalf("secret times: %+v %v", sec, err)
	}
	audit := allAudit(t, repo, vault.AuditFilter{})
	if len(audit) != 2 || !audit[0].OccurredAt.Equal(wantTime) || audit[0].OccurredAt.Location() != time.UTC {
		t.Fatalf("audit times: %+v", audit)
	}
}

// testAppendAuditAtomic appends batches holding one refused record and checks
// that none of the batch was written.
func testAppendAuditAtomic(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	if err := repo.AppendAudit(ctx, nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	good := record(t, base, vault.ActionSecretRead, globex, "a")
	for name, spoil := range map[string]func(r *vault.AuditRecord){
		"no id":           func(r *vault.AuditRecord) { r.ID = "" },
		"no action":       func(r *vault.AuditRecord) { r.Action = "" },
		"unknown action":  func(r *vault.AuditRecord) { r.Action = "secret_read_denied" },
		"no outcome":      func(r *vault.AuditRecord) { r.Outcome = "" },
		"unknown outcome": func(r *vault.AuditRecord) { r.Outcome = "refused" },
	} {
		bad := record(t, base, vault.ActionSecretRead, globex, "a")
		spoil(&bad)
		if err := repo.AppendAudit(ctx, []vault.AuditRecord{good, bad}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("%s: want ErrInvalidInput, got %v", name, err)
		}
		if n := len(allAudit(t, repo, vault.AuditFilter{})); n != 0 {
			t.Fatalf("%s: a refused batch wrote %d rows", name, n)
		}
	}
	if err := repo.AppendAudit(ctx, []vault.AuditRecord{good, good}); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("duplicate in batch: want ErrAlreadyExists, got %v", err)
	}
	second := record(t, base, vault.ActionSecretRead, globex, "b")
	if err := repo.AppendAudit(ctx, []vault.AuditRecord{good, second}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendAudit(ctx, []vault.AuditRecord{record(t, base, vault.ActionSecretRead, globex, "c"), good}); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("recorded id in batch: want ErrAlreadyExists, got %v", err)
	}
	if got := allAudit(t, repo, vault.AuditFilter{}); len(got) != 2 {
		t.Fatalf("want exactly the accepted batch, got %+v", got)
	}
}

// testGetRecordsOutcomeAndVersion pins what a read records: the version read
// filled in by GetSecret, the outcome as given, and the outcome filter.
func testGetRecordsOutcomeAndVersion(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	if err := repo.CreateOwnerKey(ctx, key(globex, "k1"), nil); err != nil {
		t.Fatal(err)
	}
	ref := vault.Ref{Owner: globex, Name: "a"}
	for i := range 2 {
		if _, err := repo.PutSecret(ctx, sealed(globex, ref.Name, "k1", "ct", base), record(t, base.Add(time.Duration(i)*time.Second), vault.ActionSecretPut, globex, ref.Name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.GetSecret(ctx, ref, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(allAudit(t, repo, vault.AuditFilter{Action: vault.ActionSecretRead})); n != 0 {
		t.Fatalf("a read without records recorded %d rows", n)
	}
	read := record(t, base.Add(2*time.Second), vault.ActionSecretRead, globex, ref.Name)
	got, err := repo.GetSecret(ctx, ref, []vault.AuditRecord{read})
	if err != nil || got.Version != 2 {
		t.Fatalf("get: %+v %v", got, err)
	}
	denied := record(t, base.Add(3*time.Second), vault.ActionSecretRead, globex, ref.Name)
	denied.Outcome, denied.Version, denied.KeyID = vault.OutcomeDenied, 2, "k1"
	if err := repo.AppendAudit(ctx, []vault.AuditRecord{denied}); err != nil {
		t.Fatal(err)
	}
	reads := allAudit(t, repo, vault.AuditFilter{Action: vault.ActionSecretRead})
	if len(reads) != 2 || reads[0].ID != read.ID || reads[0].Version != 2 || reads[0].KeyID != "k1" || reads[0].Outcome != vault.OutcomeOK ||
		reads[1].ID != denied.ID || reads[1].Version != 2 || reads[1].Outcome != vault.OutcomeDenied {
		t.Fatalf("reads must carry the version read and their outcome: %+v", reads)
	}
	if got := allAudit(t, repo, vault.AuditFilter{Outcome: vault.OutcomeDenied}); len(got) != 1 || got[0].ID != denied.ID {
		t.Fatalf("denied filter: %+v", got)
	}
	if got := allAudit(t, repo, vault.AuditFilter{Outcome: vault.OutcomeOK}); len(got) != 3 {
		t.Fatalf("ok filter: want 2 puts + 1 read, got %+v", got)
	}
	if _, _, err := repo.ListAudit(ctx, vault.AuditFilter{Outcome: "refused"}, "", 10); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("bad outcome filter: %v", err)
	}
}

// deleteOwnerFixture gives globex a key and the named secrets, and returns the
// records a DeleteOwner of them would carry.
func deleteOwnerFixture(t *testing.T, repo vault.Repository, names ...string) []vault.AuditRecord {
	t.Helper()
	ctx := context.Background()
	if err := repo.CreateOwnerKey(ctx, key(globex, "k1"), nil); err != nil {
		t.Fatal(err)
	}
	var records []vault.AuditRecord
	for _, name := range names {
		if _, err := repo.PutSecret(ctx, sealed(globex, name, "k1", "ct", base), record(t, base, vault.ActionSecretPut, globex, name)); err != nil {
			t.Fatal(err)
		}
		records = append(records, record(t, base, vault.ActionSecretDeleted, globex, name))
	}
	return append(records, record(t, base, vault.ActionOwnerKeyDestroyed, globex, ""))
}

// requireOwnerIntact checks a refused DeleteOwner removed and recorded nothing.
func requireOwnerIntact(t *testing.T, repo vault.Repository, secrets int) {
	t.Helper()
	ctx := context.Background()
	if left, _ := repo.ListSecrets(ctx, globex); len(left) != secrets {
		t.Fatalf("a conflicting delete removed secrets: %+v", left)
	}
	if _, err := repo.GetOwnerKey(ctx, globex); err != nil {
		t.Fatalf("a conflicting delete removed the owner key: %v", err)
	}
	for _, action := range []vault.Action{vault.ActionSecretDeleted, vault.ActionOwnerKeyDestroyed} {
		if got := allAudit(t, repo, vault.AuditFilter{Action: action}); len(got) != 0 {
			t.Fatalf("a conflicting delete recorded %+v", got)
		}
	}
}

// testDeleteOwnerConflictsOnShrink expects a secret the store no longer holds,
// as when it was deleted after the caller listed it.
func testDeleteOwnerConflictsOnShrink(t *testing.T, repo vault.Repository) {
	records := deleteOwnerFixture(t, repo, "a", "b")
	gone := record(t, base, vault.ActionSecretDeleted, globex, "c")
	err := repo.DeleteOwner(context.Background(), globex, []string{"a", "b", "c"}, append(records, gone))
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	requireOwnerIntact(t, repo, 2)
}

// testDeleteOwnerConflictsOnGrowth leaves out a secret the store holds, as
// when it was written after the caller listed.
func testDeleteOwnerConflictsOnGrowth(t *testing.T, repo vault.Repository) {
	records := deleteOwnerFixture(t, repo, "a", "b")
	err := repo.DeleteOwner(context.Background(), globex, []string{"a"}, []vault.AuditRecord{records[0], records[2]})
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	requireOwnerIntact(t, repo, 2)
}

// testDeleteOwnerRecordsReturnedVersion passes records with stale versions and
// key ids and checks the store recorded the rows it removed.
func testDeleteOwnerRecordsReturnedVersion(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	records := deleteOwnerFixture(t, repo, "a", "b")
	if _, err := repo.PutSecret(ctx, sealed(globex, "a", "k1", "ct", base), record(t, base, vault.ActionSecretPut, globex, "a")); err != nil {
		t.Fatal(err)
	}
	later := base.Add(time.Minute)
	resealed := []vault.SealedSecret{sealed(globex, "a", "k2", "ct2-a", later), sealed(globex, "b", "k2", "ct2-b", later)}
	if err := repo.ReplaceOwnerKey(ctx, key(globex, "k2"), "k1", resealed, nil); err != nil {
		t.Fatal(err)
	}
	records[0].Version, records[0].KeyID = 1, "k1"
	if err := repo.DeleteOwner(ctx, globex, []string{"b", "a"}, records); err != nil {
		t.Fatal(err)
	}
	deleted := allAudit(t, repo, vault.AuditFilter{Owner: &globex, Action: vault.ActionSecretDeleted})
	want := map[string]int{"a": 2, "b": 1}
	if len(deleted) != 2 {
		t.Fatalf("want two deletion records, got %+v", deleted)
	}
	for _, r := range deleted {
		if r.Version != want[r.Name] || r.KeyID != "k2" {
			t.Fatalf("deletion of %s recorded version %d key %q, want version %d key k2", r.Name, r.Version, r.KeyID, want[r.Name])
		}
	}
	destroyed := allAudit(t, repo, vault.AuditFilter{Owner: &globex, Action: vault.ActionOwnerKeyDestroyed})
	if len(destroyed) != 1 || destroyed[0].Version != 0 || destroyed[0].KeyID != "" {
		t.Fatalf("the destruction record must be stored as given: %+v", destroyed)
	}
}

// testCorrelationRoundTrip stores request and trace ids as given, through an
// append and through a mutation. Blanking an invalid id is vault.Correlate's
// job, so a 129-byte id reaching the store is kept whole.
func testCorrelationRoundTrip(t *testing.T, repo vault.Repository) {
	ctx := context.Background()
	created := record(t, base, vault.ActionOwnerKeyCreated, globex, "")
	created.RequestID, created.TraceID = "req-01J9ZK3Q7M8N", "4bf92f3577b34da6a3ce929d0e0e4736"
	if err := repo.CreateOwnerKey(ctx, key(globex, "k1"), []vault.AuditRecord{created}); err != nil {
		t.Fatal(err)
	}
	long := record(t, base.Add(time.Second), vault.ActionSecretRead, globex, "a")
	long.RequestID = strings.Repeat("r", 129)
	plain := record(t, base.Add(2*time.Second), vault.ActionSecretRead, globex, "a")
	if err := repo.AppendAudit(ctx, []vault.AuditRecord{long, plain}); err != nil {
		t.Fatal(err)
	}
	got := allAudit(t, repo, vault.AuditFilter{})
	if len(got) != 3 {
		t.Fatalf("want 3 records, got %+v", got)
	}
	for i, want := range []vault.AuditRecord{created, long, plain} {
		if got[i].ID != want.ID || got[i].RequestID != want.RequestID || got[i].TraceID != want.TraceID {
			t.Fatalf("record %d: request %q trace %q, want %q %q", i, got[i].RequestID, got[i].TraceID, want.RequestID, want.TraceID)
		}
	}
}

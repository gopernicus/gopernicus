package vault_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/pockets/secrets/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

var (
	gps    = vault.Owner{Type: "tenant", ID: "gps"}
	token  = vault.Ref{Owner: gps, Name: "cloudflare.api_token"}
	staff  = vault.Source{ActorType: "user", ActorID: "u1", Reason: "paste token"}
	worker = vault.Source{System: "deploy"}
)

// sentinel is a value whose appearance anywhere but Get's return is a leak.
const sentinel = "LEAK-cfat-9f8e7d6c5b4a"

func ring(t *testing.T, active string, ids ...string) cryptids.KeyedEncrypter {
	t.Helper()
	keys := map[string][]byte{}
	for _, id := range ids {
		keys[id] = bytes.Repeat([]byte{id[len(id)-1]}, 32)
	}
	enc, err := cryptids.NewAESGCMRing(cryptids.KeyRing{Active: active, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

func newVault(t *testing.T, repo vault.Repository, enc cryptids.KeyedEncrypter) *vault.Service {
	t.Helper()
	w, err := vault.NewRingWrapper(enc)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := vault.NewService(repo, w)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestPutGetDeleteAreAudited(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	svc := newVault(t, repo, ring(t, "m1", "m1"))

	meta, err := svc.Put(ctx, token, []byte(sentinel), staff)
	if err != nil || meta.Version != 1 || meta.Ref != token || meta.KeyID == "" {
		t.Fatalf("put: %+v %v", meta, err)
	}
	if _, err := svc.Get(ctx, token, vault.Source{}); !errors.Is(err, vault.ErrSourceRequired) {
		t.Fatalf("unattributed read must be refused: %v", err)
	}
	got, err := svc.Get(ctx, token, worker)
	if err != nil || string(got) != sentinel {
		t.Fatalf("get: %q %v", got, err)
	}
	again, err := svc.Put(ctx, token, []byte("second"), staff)
	if err != nil || again.Version != 2 || again.KeyID != meta.KeyID {
		t.Fatalf("second put: %+v %v", again, err)
	}
	if err := svc.Delete(ctx, token, staff); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, token, worker); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}

	audit, _, err := svc.Audit(ctx, vault.AuditFilter{Owner: &gps}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, r := range audit {
		actions = append(actions, string(r.Action))
		if strings.Contains(r.ID+r.Name+r.KeyID+r.Source.Reason, sentinel) {
			t.Fatalf("audit carries the value: %+v", r)
		}
	}
	want := "owner_key_created secret_put secret_read secret_put secret_deleted"
	if strings.Join(actions, " ") != want {
		t.Fatalf("audit actions %q, want %q", strings.Join(actions, " "), want)
	}
	if audit[1].Version != 1 || audit[3].Version != 2 || audit[1].Source != staff || audit[2].Source != worker {
		t.Fatalf("audit detail: %+v", audit)
	}
	// Nothing in the store holds the plaintext.
	for _, sec := range mustSealed(t, repo) {
		if strings.Contains(sec.Ciphertext, sentinel) {
			t.Fatal("ciphertext contains plaintext")
		}
	}
}

func TestValuesAreBoundToTheirReference(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	svc := newVault(t, repo, ring(t, "m1", "m1"))
	other := vault.Ref{Owner: gps, Name: "other"}
	if _, err := svc.Put(ctx, token, []byte("one"), staff); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Put(ctx, other, []byte("two"), staff); err != nil {
		t.Fatal(err)
	}
	// Move token's ciphertext onto other's row, as a DBA with write access could.
	sealed := mustSealed(t, repo) // sorted by name: token first, then other
	moved := sealed[0]
	moved.Ref = other
	if err := repo.ReplaceOwnerKey(ctx, mustKey(t, repo), mustKey(t, repo).KeyID, []vault.SealedSecret{moved}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, other, worker); !errors.Is(err, vault.ErrSealedValueCorrupt) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("moved ciphertext must not open: %v", err)
	}
}

func TestDeleteOwnerShredsEverything(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	svc := newVault(t, repo, ring(t, "m1", "m1"))
	for _, name := range []string{"a", "b"} {
		if _, err := svc.Put(ctx, vault.Ref{Owner: gps, Name: name}, []byte(name), staff); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.DeleteOwner(ctx, gps, staff); err != nil {
		t.Fatal(err)
	}
	if left, _ := svc.List(ctx, gps); len(left) != 0 {
		t.Fatalf("secrets survive: %+v", left)
	}
	if err := svc.DeleteOwner(ctx, gps, staff); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	audit, _, _ := svc.Audit(ctx, vault.AuditFilter{Owner: &gps}, "", 0)
	var deleted, destroyed int
	for _, r := range audit {
		switch r.Action {
		case vault.ActionSecretDeleted:
			deleted++
		case vault.ActionOwnerKeyDestroyed:
			destroyed++
		}
	}
	if deleted != 2 || destroyed != 1 {
		t.Fatalf("audit: %d deleted, %d destroyed", deleted, destroyed)
	}
	// A new secret for the same owner gets a new key; the old ciphertext is gone for good.
	meta, err := svc.Put(ctx, token, []byte("fresh"), staff)
	if err != nil || meta.KeyID == "" {
		t.Fatalf("put after shred: %+v %v", meta, err)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	svc := newVault(t, memory.New(), ring(t, "m1", "m1"))
	for name, call := range map[string]func() error{
		"bad owner type": func() error {
			_, e := svc.Put(ctx, vault.Ref{Owner: vault.Owner{Type: "Tenant", ID: "gps"}, Name: "a"}, []byte("v"), staff)
			return e
		},
		"bad name": func() error {
			_, e := svc.Put(ctx, vault.Ref{Owner: gps, Name: "has space"}, []byte("v"), staff)
			return e
		},
		"empty value": func() error { _, e := svc.Put(ctx, token, nil, staff); return e },
		"huge value":  func() error { _, e := svc.Put(ctx, token, make([]byte, vault.MaxValueBytes+1), staff); return e },
		"actor+system": func() error {
			_, e := svc.Put(ctx, token, []byte("v"), vault.Source{ActorType: "user", ActorID: "u", System: "x"})
			return e
		},
		"half actor": func() error { _, e := svc.Put(ctx, token, []byte("v"), vault.Source{ActorType: "user"}); return e },
		"long reason": func() error {
			_, e := svc.Put(ctx, token, []byte("v"), vault.Source{System: "x", Reason: strings.Repeat("r", vault.MaxReasonLen+1)})
			return e
		},
		"nul reason": func() error {
			_, e := svc.Put(ctx, token, []byte("v"), vault.Source{System: "x", Reason: "a\x00b"})
			return e
		},
		"bad filter": func() error {
			_, _, e := svc.Audit(ctx, vault.AuditFilter{Since: time.Unix(10, 0), Until: time.Unix(5, 0)}, "", 0)
			return e
		},
	} {
		if err := call(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: want ErrInvalidInput, got %v", name, err)
		}
	}
	if _, err := vault.NewService(nil, nil); !errors.Is(err, vault.ErrRepositoryRequired) {
		t.Fatalf("nil repo: %v", err)
	}
	if _, err := vault.NewService(memory.New(), nil); !errors.Is(err, vault.ErrWrapperRequired) {
		t.Fatalf("nil wrapper: %v", err)
	}
	if _, err := vault.NewRingWrapper(nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil encrypter: %v", err)
	}
}

func TestMasterKeyDroppedIsReported(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	if _, err := newVault(t, repo, ring(t, "m1", "m1")).Put(ctx, token, []byte("v"), staff); err != nil {
		t.Fatal(err)
	}
	// m1 retired without rewrapping first.
	_, err := newVault(t, repo, ring(t, "m2", "m2")).Get(ctx, token, worker)
	if !errors.Is(err, cryptids.ErrUnknownKeyID) {
		t.Fatalf("want ErrUnknownKeyID, got %v", err)
	}
}

func TestNothingLogsPlaintext(t *testing.T) {
	// The pocket takes no logger; this pins that a host logger wrapped around
	// the calls would see nothing either, by checking every error string.
	ctx := context.Background()
	repo := memory.New()
	svc := newVault(t, repo, ring(t, "m1", "m1"))
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	_, err := svc.Put(ctx, token, []byte(sentinel), staff)
	log.Info("put", "err", err)
	_, err = svc.Get(ctx, token, vault.Source{})
	log.Info("get", "err", err)
	_, err = svc.Put(ctx, vault.Ref{Owner: gps, Name: "bad name"}, []byte(sentinel), staff)
	log.Info("put", "err", err)
	if strings.Contains(buf.String(), sentinel) {
		t.Fatalf("plaintext in log: %s", buf.String())
	}
}

func mustSealed(t *testing.T, repo vault.Repository) []vault.SealedSecret {
	t.Helper()
	sealed, err := repo.ListSealedSecrets(context.Background(), gps)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func mustKey(t *testing.T, repo vault.Repository) vault.OwnerKey {
	t.Helper()
	key, err := repo.GetOwnerKey(context.Background(), gps)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

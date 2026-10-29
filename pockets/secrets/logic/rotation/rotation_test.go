package rotation_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/secrets/logic/rotation"
	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/pockets/secrets/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

var (
	ops   = vault.Source{System: "rotate"}
	staff = vault.Source{ActorType: "user", ActorID: "u1"}
)

func wrapper(t *testing.T, active string, ids ...string) vault.Wrapper {
	t.Helper()
	keys := map[string][]byte{}
	for _, id := range ids {
		keys[id] = bytes.Repeat([]byte{id[len(id)-1]}, 32)
	}
	enc, err := cryptids.NewAESGCMRing(cryptids.KeyRing{Active: active, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	w, err := vault.NewRingWrapper(enc)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func services(t *testing.T, repo vault.Repository, w vault.Wrapper) (*vault.Service, *rotation.Service) {
	t.Helper()
	v, err := vault.NewService(repo, w)
	if err != nil {
		t.Fatal(err)
	}
	r, err := rotation.NewService(repo, w)
	if err != nil {
		t.Fatal(err)
	}
	return v, r
}

func owner(id string) vault.Owner { return vault.Owner{Type: "tenant", ID: id} }

// TestMasterRotation walks the documented procedure: m1 only; add m2 active;
// rewrap; drop m1; everything still reads and no secret row was touched.
func TestMasterRotation(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	v1, _ := services(t, repo, wrapper(t, "m1", "m1"))
	for i := range 5 {
		o := owner(string(rune('a' + i)))
		if _, err := v1.Put(ctx, vault.Ref{Owner: o, Name: "token"}, []byte("value-"+o.ID), staff); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := repo.ListSealedSecrets(ctx, owner("c"))

	// Step 2: m2 active, m1 still present. Old owner keys still unwrap.
	v2, r2 := services(t, repo, wrapper(t, "m2", "m1", "m2"))
	if got, err := v2.Get(ctx, vault.Ref{Owner: owner("a"), Name: "token"}, ops); err != nil || string(got) != "value-a" {
		t.Fatalf("read during rotation: %q %v", got, err)
	}
	dry, err := r2.RewrapOwnerKeys(ctx, ops, rotation.RewrapOptions{Page: 2, DryRun: true})
	if err != nil || dry.Scanned != 5 || dry.Rewrapped != 5 || dry.Skipped != 0 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if keys, _, _ := repo.ListOwnerKeys(ctx, "", 10); keys[0].WrapperKeyID != "m1" {
		t.Fatal("dry run wrote")
	}
	// Step 3: rewrap, in small pages.
	report, err := r2.RewrapOwnerKeys(ctx, ops, rotation.RewrapOptions{Page: 2})
	if err != nil || report.Scanned != 5 || report.Rewrapped != 5 || len(report.Failed) != 0 {
		t.Fatalf("rewrap: %+v %v", report, err)
	}
	again, err := r2.RewrapOwnerKeys(ctx, ops, rotation.RewrapOptions{})
	if err != nil || again.Rewrapped != 0 || again.Skipped != 5 {
		t.Fatalf("rerun must be a no-op: %+v %v", again, err)
	}
	after, _ := repo.ListSealedSecrets(ctx, owner("c"))
	if len(before) != 1 || before[0] != after[0] {
		t.Fatalf("rewrap touched a secret row: %+v -> %+v", before, after)
	}
	// Step 4: m1 dropped.
	v3, _ := services(t, repo, wrapper(t, "m2", "m2"))
	for i := range 5 {
		o := owner(string(rune('a' + i)))
		if got, err := v3.Get(ctx, vault.Ref{Owner: o, Name: "token"}, ops); err != nil || string(got) != "value-"+o.ID {
			t.Fatalf("after drop %s: %q %v", o, got, err)
		}
	}
	audit, _, _ := v3.Audit(ctx, vault.AuditFilter{Action: vault.ActionOwnerKeyRewrapped}, "", 0)
	if len(audit) != 5 || audit[0].Source != ops {
		t.Fatalf("rewrap audit: %+v", audit)
	}
}

func TestRewrapReportsUnknownWrapperKey(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	v1, _ := services(t, repo, wrapper(t, "m1", "m1"))
	if _, err := v1.Put(ctx, vault.Ref{Owner: owner("lost"), Name: "t"}, []byte("v"), staff); err != nil {
		t.Fatal(err)
	}
	v9, _ := services(t, repo, wrapper(t, "m9", "m9"))
	if _, err := v9.Put(ctx, vault.Ref{Owner: owner("fine"), Name: "t"}, []byte("v"), staff); err != nil {
		t.Fatal(err)
	}
	// Operator skipped step 1 and dropped m1 before rewrapping.
	_, r := services(t, repo, wrapper(t, "m2", "m9", "m2"))
	report, err := r.RewrapOwnerKeys(ctx, ops, rotation.RewrapOptions{})
	if err != nil || report.Scanned != 2 || report.Rewrapped != 1 || len(report.Failed) != 1 || report.Failed[0] != owner("lost") {
		t.Fatalf("report: %+v %v", report, err)
	}
}

func TestOwnerKeyRotation(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	v, r := services(t, repo, wrapper(t, "m1", "m1"))
	o := owner("globex")
	for _, name := range []string{"a", "b", "c"} {
		if _, err := v.Put(ctx, vault.Ref{Owner: o, Name: name}, []byte("value-"+name), staff); err != nil {
			t.Fatal(err)
		}
	}
	oldKey, _ := repo.GetOwnerKey(ctx, o)
	oldSealed, _ := repo.ListSealedSecrets(ctx, o)

	if err := r.RotateOwnerKey(ctx, o, ops); err != nil {
		t.Fatal(err)
	}
	newKey, _ := repo.GetOwnerKey(ctx, o)
	if newKey.KeyID == oldKey.KeyID || newKey.WrappedKey == oldKey.WrappedKey || !newKey.CreatedAt.Equal(oldKey.CreatedAt) || !newKey.RotatedAt.After(oldKey.RotatedAt) {
		t.Fatalf("key not rotated: %+v -> %+v", oldKey, newKey)
	}
	newSealed, _ := repo.ListSealedSecrets(ctx, o)
	for i, sec := range newSealed {
		if sec.KeyID != newKey.KeyID || sec.Ciphertext == oldSealed[i].Ciphertext || sec.Version != 1 {
			t.Fatalf("secret %s not resealed: %+v", sec.Ref, sec)
		}
		got, err := v.Get(ctx, sec.Ref, ops)
		if err != nil || string(got) != "value-"+sec.Ref.Name {
			t.Fatalf("read after rotation: %q %v", got, err)
		}
	}
	if err := r.RotateOwnerKey(ctx, owner("nobody"), ops); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("rotate absent: %v", err)
	}
	if err := r.RotateOwnerKey(ctx, o, vault.Source{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("rotate without source: %v", err)
	}
	audit, _, _ := v.Audit(ctx, vault.AuditFilter{Owner: &o, Action: vault.ActionOwnerKeyRotated}, "", 0)
	if len(audit) != 1 || audit[0].KeyID != newKey.KeyID {
		t.Fatalf("rotation audit: %+v", audit)
	}
}

// TestRotationIsAtomic corrupts one sealed value so the reseal fails midway
// and checks that neither the key nor any secret changed.
func TestRotationIsAtomic(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	v, r := services(t, repo, wrapper(t, "m1", "m1"))
	o := owner("globex")
	for _, name := range []string{"a", "b"} {
		if _, err := v.Put(ctx, vault.Ref{Owner: o, Name: name}, []byte("v"), staff); err != nil {
			t.Fatal(err)
		}
	}
	key, _ := repo.GetOwnerKey(ctx, o)
	sealed, _ := repo.ListSealedSecrets(ctx, o)
	broken := sealed[1]
	broken.Ciphertext = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if err := repo.ReplaceOwnerKey(ctx, key, key.KeyID, []vault.SealedSecret{broken}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.RotateOwnerKey(ctx, o, ops); !errors.Is(err, vault.ErrSealedValueCorrupt) {
		t.Fatalf("want corrupt value error, got %v", err)
	}
	after, _ := repo.GetOwnerKey(ctx, o)
	if after != key {
		t.Fatal("key changed although rotation failed")
	}
	if got, err := v.Get(ctx, sealed[0].Ref, ops); err != nil || string(got) != "v" {
		t.Fatalf("healthy secret changed: %q %v", got, err)
	}
}

// TestRotationStampsCorrelation: both rotation records carry the context's
// request and trace ids, through vault.Correlate.
func TestRotationStampsCorrelation(t *testing.T) {
	ctx := sdk.WithTraceID(sdk.WithRequestID(context.Background(), "req-rotate"), "trace-rotate")
	repo := memory.New()
	v, _ := services(t, repo, wrapper(t, "m1", "m1"))
	o := owner("globex")
	if _, err := v.Put(context.Background(), vault.Ref{Owner: o, Name: "a"}, []byte("v"), staff); err != nil {
		t.Fatal(err)
	}
	_, r := services(t, repo, wrapper(t, "m2", "m1", "m2"))
	if _, err := r.RewrapOwnerKeys(ctx, ops, rotation.RewrapOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.RotateOwnerKey(ctx, o, ops); err != nil {
		t.Fatal(err)
	}
	for _, action := range []vault.Action{vault.ActionOwnerKeyRewrapped, vault.ActionOwnerKeyRotated} {
		audit, _, err := v.Audit(context.Background(), vault.AuditFilter{Action: action}, "", 0)
		if err != nil || len(audit) != 1 || audit[0].RequestID != "req-rotate" || audit[0].TraceID != "trace-rotate" {
			t.Fatalf("%s: %+v %v", action, audit, err)
		}
	}
}

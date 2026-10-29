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
	globex = vault.Owner{Type: "tenant", ID: "globex"}
	token  = vault.Ref{Owner: globex, Name: "cloudflare.api_token"}
	staff  = vault.Source{ActorType: "user", ActorID: "u1", Reason: "paste token"}
	worker = vault.Source{System: "deploy"}
)

// sentinel is a value whose appearance anywhere but Get's return is a leak.
const sentinel = "LEAK-cfat-9f8e7d6c5b4a"

// errKeyService is unavailableWrapper's Unwrap error.
var errKeyService = errors.New("key service unavailable")

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

	audit, _, err := svc.Audit(ctx, vault.AuditFilter{Owner: &globex}, "", 0)
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
	other := vault.Ref{Owner: globex, Name: "other"}
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
		if _, err := svc.Put(ctx, vault.Ref{Owner: globex, Name: name}, []byte(name), staff); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.DeleteOwner(ctx, globex, staff); err != nil {
		t.Fatal(err)
	}
	if left, _ := svc.List(ctx, globex); len(left) != 0 {
		t.Fatalf("secrets survive: %+v", left)
	}
	if err := svc.DeleteOwner(ctx, globex, staff); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	audit, _, _ := svc.Audit(ctx, vault.AuditFilter{Owner: &globex}, "", 0)
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

func TestActorIDsTakeHostShapes(t *testing.T) {
	ctx := context.Background()
	svc := newVault(t, memory.New(), ring(t, "m1", "m1"))
	for _, id := range []string{"HGVcNGX5wbQd8PfR42Gtz", "01J9ZK3Q7M8N2P4R6T8V0W2X4Y", "6f1a2b3c-4d5e-4f60-8a9b-0c1d2e3f4a5b", "svc:deploy", "ops@example.com"} {
		if _, err := svc.Put(ctx, token, []byte("v"), vault.Source{ActorType: "user", ActorID: id}); err != nil {
			t.Errorf("actor id %q refused: %v", id, err)
		}
	}
	audit, _, err := svc.Audit(ctx, vault.AuditFilter{ActorID: "svc:deploy"}, "", 0)
	if err != nil || len(audit) != 1 {
		t.Fatalf("filter by host-shaped actor id: %d %v", len(audit), err)
	}
	for _, bad := range []string{"", " u1", "u 1", "a/b", "é"} {
		if _, err := svc.Put(ctx, token, []byte("v"), vault.Source{ActorType: "user", ActorID: bad}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("actor id %q accepted: %v", bad, err)
		}
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	svc := newVault(t, memory.New(), ring(t, "m1", "m1"))
	for name, call := range map[string]func() error{
		"bad owner type": func() error {
			_, e := svc.Put(ctx, vault.Ref{Owner: vault.Owner{Type: "Tenant", ID: "globex"}, Name: "a"}, []byte("v"), staff)
			return e
		},
		"bad name": func() error {
			_, e := svc.Put(ctx, vault.Ref{Owner: globex, Name: "has space"}, []byte("v"), staff)
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
	_, err = svc.Put(ctx, vault.Ref{Owner: globex, Name: "bad name"}, []byte(sentinel), staff)
	log.Info("put", "err", err)
	if strings.Contains(buf.String(), sentinel) {
		t.Fatalf("plaintext in log: %s", buf.String())
	}
}

func mustSealed(t *testing.T, repo vault.Repository) []vault.SealedSecret {
	t.Helper()
	sealed, err := repo.ListSealedSecrets(context.Background(), globex)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func mustKey(t *testing.T, repo vault.Repository) vault.OwnerKey {
	t.Helper()
	key, err := repo.GetOwnerKey(context.Background(), globex)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// failingAppend is a repository whose AppendAudit fails, as a store whose
// audit table refuses the insert would.
type failingAppend struct{ *memory.Store }

func (failingAppend) AppendAudit(context.Context, []vault.AuditRecord) error {
	return errors.New("audit table unavailable")
}

// conflictingDeleteOwner reports sdk.ErrConflict on its first conflicts calls
// to DeleteOwner, as a store whose owner gained a secret after the listing.
type conflictingDeleteOwner struct {
	*memory.Store
	conflicts, calls int
}

func (r *conflictingDeleteOwner) DeleteOwner(ctx context.Context, owner vault.Owner, expected []string, audit []vault.AuditRecord) error {
	r.calls++
	if r.calls <= r.conflicts {
		return sdk.ErrConflict
	}
	return r.Store.DeleteOwner(ctx, owner, expected, audit)
}

// unavailableWrapper fails every Unwrap the way an unreachable key service
// would: an error that is not a refusal.
type unavailableWrapper struct{ vault.Wrapper }

func (unavailableWrapper) Unwrap(context.Context, string) ([]byte, error) {
	return nil, errKeyService
}

// readFixture stores token twice under m1, so a read returns version 2.
func readFixture(t *testing.T) (*memory.Store, *vault.Service) {
	t.Helper()
	repo := memory.New()
	svc := newVault(t, repo, ring(t, "m1", "m1"))
	for _, v := range []string{"first", sentinel} {
		if _, err := svc.Put(context.Background(), token, []byte(v), staff); err != nil {
			t.Fatal(err)
		}
	}
	return repo, svc
}

func reads(t *testing.T, svc *vault.Service) []vault.AuditRecord {
	t.Helper()
	got, _, err := svc.Audit(context.Background(), vault.AuditFilter{Action: vault.ActionSecretRead}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestReadOutcomes pins the one secret_read record per Get: the version read,
// OutcomeDenied when the vault cannot open the value for the caller, and no
// record for a missing owner key or a wrapper that is unavailable.
func TestReadOutcomes(t *testing.T) {
	ctx := context.Background()
	reseal := func(t *testing.T, repo *memory.Store, change func(*vault.SealedSecret)) {
		t.Helper()
		key := mustKey(t, repo)
		sec := mustSealed(t, repo)[0]
		change(&sec)
		if err := repo.ReplaceOwnerKey(ctx, key, key.KeyID, []vault.SealedSecret{sec}, nil); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]struct {
		read    func(t *testing.T, repo *memory.Store, svc *vault.Service) ([]byte, error)
		want    error
		outcome vault.Outcome // "" expects no record
	}{
		"ok": {
			read: func(t *testing.T, _ *memory.Store, svc *vault.Service) ([]byte, error) {
				return svc.Get(ctx, token, worker)
			},
			outcome: vault.OutcomeOK,
		},
		"retired wrapper key": {
			read: func(t *testing.T, repo *memory.Store, _ *vault.Service) ([]byte, error) {
				return newVault(t, repo, ring(t, "m2", "m2")).Get(ctx, token, worker)
			},
			want: cryptids.ErrUnknownKeyID, outcome: vault.OutcomeDenied,
		},
		"sealed under another key": {
			read: func(t *testing.T, repo *memory.Store, svc *vault.Service) ([]byte, error) {
				reseal(t, repo, func(s *vault.SealedSecret) { s.KeyID = "k_0000000000000000" })
				return svc.Get(ctx, token, worker)
			},
			want: sdk.ErrConflict, outcome: vault.OutcomeDenied,
		},
		"corrupted ciphertext": {
			read: func(t *testing.T, repo *memory.Store, svc *vault.Service) ([]byte, error) {
				reseal(t, repo, func(s *vault.SealedSecret) { s.Ciphertext = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" })
				return svc.Get(ctx, token, worker)
			},
			want: vault.ErrSealedValueCorrupt, outcome: vault.OutcomeDenied,
		},
		"missing owner key": {
			read: func(t *testing.T, _ *memory.Store, svc *vault.Service) ([]byte, error) {
				return svc.Get(ctx, vault.Ref{Owner: vault.Owner{Type: "tenant", ID: "nobody"}, Name: token.Name}, worker)
			},
			want: sdk.ErrNotFound,
		},
		"wrapper unavailable": {
			read: func(t *testing.T, repo *memory.Store, _ *vault.Service) ([]byte, error) {
				w, err := vault.NewRingWrapper(ring(t, "m1", "m1"))
				if err != nil {
					t.Fatal(err)
				}
				svc, err := vault.NewService(repo, unavailableWrapper{w})
				if err != nil {
					t.Fatal(err)
				}
				return svc.Get(ctx, token, worker)
			},
			want: errKeyService,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			repo, svc := readFixture(t)
			value, err := c.read(t, repo, svc)
			switch {
			case c.want == nil && (err != nil || string(value) != sentinel):
				t.Fatalf("read: %q %v", value, err)
			case c.want != nil && (err == nil || value != nil):
				t.Fatalf("read: want an error and no value, got %q %v", value, err)
			case c.want != nil && !errors.Is(err, c.want):
				t.Fatalf("read: want %v, got %v", c.want, err)
			}
			got := reads(t, svc)
			if c.outcome == "" {
				if len(got) != 0 {
					t.Fatalf("want no read record, got %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Outcome != c.outcome || got[0].Version != 2 || got[0].Source != worker || got[0].Name != token.Name {
				t.Fatalf("want one %s read of version 2, got %+v", c.outcome, got)
			}
			if c.outcome == vault.OutcomeOK && got[0].KeyID != mustKey(t, repo).KeyID {
				t.Fatalf("read key id %q, want the owner key", got[0].KeyID)
			}
		})
	}
}

func TestAuditFilterListsOnlyDenials(t *testing.T) {
	ctx := context.Background()
	repo, svc := readFixture(t)
	if _, err := svc.Get(ctx, token, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := newVault(t, repo, ring(t, "m2", "m2")).Get(ctx, token, worker); err == nil {
		t.Fatal("read under a retired wrapper key succeeded")
	}
	denied, _, err := svc.Audit(ctx, vault.AuditFilter{Outcome: vault.OutcomeDenied}, "", 0)
	if err != nil || len(denied) != 1 || denied[0].Outcome != vault.OutcomeDenied || denied[0].Action != vault.ActionSecretRead {
		t.Fatalf("denied filter: %+v %v", denied, err)
	}
	if _, _, err := svc.Audit(ctx, vault.AuditFilter{Outcome: "refused"}, "", 0); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unknown outcome filter: %v", err)
	}
}

// TestReadFailsWithoutItsRecord: when the read record cannot be appended the
// read fails and no value is returned.
func TestReadFailsWithoutItsRecord(t *testing.T) {
	ctx := context.Background()
	svc := newVault(t, failingAppend{memory.New()}, ring(t, "m1", "m1"))
	if _, err := svc.Put(ctx, token, []byte(sentinel), staff); err != nil {
		t.Fatal(err)
	}
	value, err := svc.Get(ctx, token, worker)
	if err == nil || value != nil {
		t.Fatalf("read without its record: %q %v", value, err)
	}
}

func TestDeleteRecordsRemovedVersion(t *testing.T) {
	ctx := context.Background()
	_, svc := readFixture(t)
	if err := svc.Delete(ctx, token, staff); err != nil {
		t.Fatal(err)
	}
	got, _, err := svc.Audit(ctx, vault.AuditFilter{Action: vault.ActionSecretDeleted}, "", 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("delete audit: %+v %v", got, err)
	}
	if got[0].Version != 2 || got[0].KeyID == "" {
		t.Fatalf("delete recorded version %d key %q, want the removed version 2 and its key", got[0].Version, got[0].KeyID)
	}
}

func TestDeleteOwnerRetriesConflicts(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		conflicts, calls int
		want             error
	}{
		{conflicts: 3, calls: 4},
		{conflicts: 4, calls: 4, want: sdk.ErrConflict},
	} {
		repo := &conflictingDeleteOwner{Store: memory.New(), conflicts: c.conflicts}
		svc := newVault(t, repo, ring(t, "m1", "m1"))
		if _, err := svc.Put(ctx, token, []byte("v"), staff); err != nil {
			t.Fatal(err)
		}
		err := svc.DeleteOwner(ctx, globex, staff)
		if (c.want == nil && err != nil) || (c.want != nil && !errors.Is(err, c.want)) || repo.calls != c.calls {
			t.Fatalf("%d conflicts: err %v after %d calls, want %v after %d", c.conflicts, err, repo.calls, c.want, c.calls)
		}
	}
}

func TestCorrelate(t *testing.T) {
	r, err := vault.NewAuditRecord(time.Now(), vault.ActionSecretRead, globex, token.Name, 0, "", worker)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != vault.OutcomeOK {
		t.Fatalf("NewAuditRecord outcome %q, want ok", r.Outcome)
	}
	if got := vault.Correlate(context.Background(), r); len(got) != 1 || got[0].RequestID != "" || got[0].TraceID != "" {
		t.Fatalf("no ids in context: %+v", got)
	}
	for id, want := range map[string]string{
		"req-01J9ZK3Q7M8N":       "req-01J9ZK3Q7M8N",
		strings.Repeat("a", 128): strings.Repeat("a", 128),
		strings.Repeat("a", 129): "",
		strings.Repeat("a", 200): "",
		"a\nb":                   "",
		"a\x7fb":                 "",
		"é":                      "",
	} {
		ctx := sdk.WithTraceID(sdk.WithRequestID(context.Background(), id), id)
		got := vault.Correlate(ctx, r, r)
		if len(got) != 2 || got[0].RequestID != want || got[0].TraceID != want || got[1].RequestID != want {
			t.Errorf("id %q: stamped %+v, want %q", id, got, want)
		}
	}
	if r.RequestID != "" {
		t.Fatal("Correlate modified its argument")
	}
}

// TestServiceStampsCorrelation: every record the service writes carries the
// context's ids, and a 200-byte request id is blanked, never refused.
func TestServiceStampsCorrelation(t *testing.T) {
	repo := memory.New()
	svc := newVault(t, repo, ring(t, "m1", "m1"))
	ctx := sdk.WithTraceID(sdk.WithRequestID(context.Background(), "req-1"), "trace-1")
	if _, err := svc.Put(ctx, token, []byte("v"), staff); err != nil {
		t.Fatal(err)
	}
	long := sdk.WithTraceID(sdk.WithRequestID(context.Background(), strings.Repeat("x", 200)), "trace-2")
	if _, err := svc.Get(long, token, worker); err != nil {
		t.Fatalf("a long request id must not fail the read: %v", err)
	}
	if err := svc.Delete(ctx, token, staff); err != nil {
		t.Fatal(err)
	}
	audit, _, err := svc.Audit(context.Background(), vault.AuditFilter{}, "", 0)
	if err != nil || len(audit) != 4 {
		t.Fatalf("audit: %+v %v", audit, err)
	}
	for _, r := range audit {
		want := [2]string{"req-1", "trace-1"}
		if r.Action == vault.ActionSecretRead {
			want = [2]string{"", "trace-2"}
		}
		if [2]string{r.RequestID, r.TraceID} != want {
			t.Errorf("%s: request %q trace %q, want %q", r.Action, r.RequestID, r.TraceID, want)
		}
	}
}

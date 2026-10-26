package authentication

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
)

// recordingSnapshots records which Snapshot the service asked for; the
// unscoped one fails with unscopedErr, as a store without migration 0025 would.
type recordingSnapshots struct {
	credential.MutationRepository
	set          credential.MethodSet
	full, scoped int
	scope        credential.SnapshotScope
	unscopedErr  error
}

func (f *recordingSnapshots) Snapshot(context.Context, string) (credential.MethodSet, error) {
	f.full++
	return f.set, f.unscopedErr
}

// scopedSnapshots adds credential.ScopedSnapshotter to recordingSnapshots.
type scopedSnapshots struct{ *recordingSnapshots }

func (f scopedSnapshots) SnapshotScoped(_ context.Context, _ string, scope credential.SnapshotScope) (credential.MethodSet, error) {
	f.scoped++
	f.scope = scope
	set := f.set
	if !scope.Passkeys {
		set.Passkeys = nil
	}
	return set, nil
}

// TestSnapshotNeverReadsPasskeysWhenUnwired pins the owner ruling on 0025:
// with passkeys unwired the credential rail is read through SnapshotScoped
// without passkeys, or — for a repository without it — through Snapshot with
// the passkeys dropped; wired, it is the plain Snapshot.
func TestSnapshotNeverReadsPasskeysWhenUnwired(t *testing.T) {
	ctx := context.Background()
	stored := credential.MethodSet{AuthRevision: 3, HasPassword: true, Passkeys: []credential.PasskeyMethod{{ID: "p1", UserVerified: true}}}

	rec := &recordingSnapshots{set: stored, unscopedErr: errors.New("relation passkey_credentials does not exist")}
	svc := newService(constructorConfig{CredentialMutations: scopedSnapshots{rec}})
	got, err := svc.snapshotCredentials(ctx, "u1")
	if err != nil || rec.full != 0 || rec.scoped != 1 || rec.scope.Passkeys || len(got.Passkeys) != 0 || !got.HasPassword || got.AuthRevision != 3 {
		t.Fatalf("unwired scoped: %+v, %v (full %d, scoped %d, scope %+v)", got, err, rec.full, rec.scoped, rec.scope)
	}

	plain := &recordingSnapshots{set: stored}
	svc = newService(constructorConfig{CredentialMutations: plain})
	if got, err := svc.snapshotCredentials(ctx, "u1"); err != nil || len(got.Passkeys) != 0 || !got.HasPassword {
		t.Fatalf("unwired unscoped repository: %+v, %v; want its passkeys dropped", got, err)
	}

	wired := &recordingSnapshots{set: stored}
	svc = newService(constructorConfig{CredentialMutations: scopedSnapshots{wired}})
	svc.relyingParty = struct{ webauthn.RelyingParty }{}
	if got, err := svc.snapshotCredentials(ctx, "u1"); err != nil || wired.full != 1 || wired.scoped != 0 || len(got.Passkeys) != 1 {
		t.Fatalf("wired: %+v, %v (full %d, scoped %d)", got, err, wired.full, wired.scoped)
	}
}

// TestEraseUserScopesPasskeysToTheWiring pins EraseInput.Passkeys: false
// unless passkeys are wired, so an unwired store never names
// passkey_credentials.
func TestEraseUserScopesPasskeysToTheWiring(t *testing.T) {
	store := &eraseStore{}
	svc := newEraseService(store, &eraseAdmin{}, &eraseEvents{})
	if _, _, err := svc.EraseUser(context.Background(), eraseAdminActor, "target"); err != nil {
		t.Fatal(err)
	}
	svc.relyingParty = struct{ webauthn.RelyingParty }{}
	if _, _, err := svc.EraseUser(context.Background(), eraseAdminActor, "target"); err != nil {
		t.Fatal(err)
	}
	if len(store.inputs) != 2 || store.inputs[0].Passkeys || !store.inputs[1].Passkeys {
		t.Fatalf("EraseInput.Passkeys = %+v, want false unwired then true wired", store.inputs)
	}
}

// TestTOTPConfirmSkipsThePasskeyClosureWhenUnwired: with passkeys unwired a
// bootstrap-tier Confirm does not ask the store to read passkeys (a passkey
// that can no longer be used closes nothing).
func TestTOTPConfirmSkipsThePasskeyClosureWhenUnwired(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "unwired-closure@example.com")
	e := h.begin(t, sessionID, u.ID)
	h.enroll(t, newPasskeyAuthenticator(true), sessionID, u.ID, "")
	h.svc.relyingParty = nil
	if _, err := h.svc.ConfirmTOTPEnrollment(ctx, TOTPConfirmInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, e, 0)}); err != nil {
		t.Fatalf("a bootstrap confirm with passkeys unwired = %v, want it confirmed", err)
	}
}

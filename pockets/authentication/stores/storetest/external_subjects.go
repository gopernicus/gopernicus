package storetest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// RunExternalSubjects exercises the external identity mirror and the
// Users.Provision half that writes it. The Provision cases are the load-bearing
// ones: the external engine re-reads the mirror after provisioning and fails
// closed when a store dropped InitialCredentials.External, so a store that
// ignores the field fails here instead of in production. Supply fresh
// disposable repositories for each invocation of newRepos.
func RunExternalSubjects(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("ExternalSubjects", func(t *testing.T) {
		if newRepos(t).ExternalSubjects == nil {
			t.Skip("ExternalSubjects not wired — external identity mirror conformance NOT verified")
		}
		t.Run("GetUnknownNotFound", func(t *testing.T) { testExternalGetUnknown(t, newRepos(t)) })
		t.Run("ProvisionPersistsExternal", func(t *testing.T) { testExternalProvisionPersists(t, newRepos(t)) })
		t.Run("ProvisionSubjectCollisionIsAtomic", func(t *testing.T) { testExternalProvisionSubjectCollision(t, newRepos(t)) })
		t.Run("ProvisionIdentifierCollisionWritesNoMirror", func(t *testing.T) { testExternalProvisionIdentifierCollision(t, newRepos(t)) })
		t.Run("ConcurrentProvisionSingleWinner", func(t *testing.T) { testExternalConcurrentProvision(t, newRepos(t)) })
		t.Run("LinkActiveUser", func(t *testing.T) { testExternalLink(t, newRepos(t)) })
		t.Run("LinkTakenAlreadyExists", func(t *testing.T) { testExternalLinkTaken(t, newRepos(t)) })
		t.Run("LinkMissingUserFails", func(t *testing.T) { testExternalLinkMissingUser(t, newRepos(t)) })
		t.Run("LinkInactiveUserFails", func(t *testing.T) { testExternalLinkInactiveUser(t, newRepos(t)) })
		t.Run("ListByUserScopedAndEmpty", func(t *testing.T) { testExternalListByUser(t, newRepos(t)) })
		t.Run("UnlinkAndUnknownNotFound", func(t *testing.T) { testExternalUnlink(t, newRepos(t)) })
	})
}

// externalProvisionInput builds a JIT-shaped Provision: an explicit user id, a
// notification-only identifier and a mirror row with no user id, as the engine
// supplies them.
func externalProvisionInput(t *testing.T, email, provider, subject string, uses identifier.Uses) (user.User, identifier.Identifier, externalsubject.ExternalSubject) {
	t.Helper()
	u := user.NewUser(ids, "External "+subject, suiteBase)
	ident, err := identifier.New(ids, idNorm, "", identifier.KindEmail, email, uses, true, suiteBase, suiteBase)
	if err != nil {
		t.Fatalf("identifier.New(%q): %v", email, err)
	}
	mirror, err := externalsubject.New(provider, subject, "", "https://issuer.example/"+provider, email, suiteBase)
	if err != nil {
		t.Fatalf("externalsubject.New: %v", err)
	}
	return u, ident, mirror
}

func linkInput(t *testing.T, provider, subject, userID string) externalsubject.ExternalSubject {
	t.Helper()
	s, err := externalsubject.New(provider, subject, userID, "https://issuer.example/"+provider, "", suiteBase)
	if err != nil {
		t.Fatalf("externalsubject.New: %v", err)
	}
	return s
}

func requireExternalAbsent(t *testing.T, repos auth.Repositories, provider, subject string) {
	t.Helper()
	if got, err := repos.ExternalSubjects.Get(context.Background(), provider, subject); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Get(%s, %s) = %+v, %v; want sdk.ErrNotFound", provider, subject, got, err)
	}
}

func testExternalGetUnknown(t *testing.T, repos auth.Repositories) {
	requireExternalAbsent(t, repos, "firebase", "nobody")
}

func testExternalProvisionPersists(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, ident, mirror := externalProvisionInput(t, "jit@example.com", "firebase", "sub-1", notifyOnlyUses)
	created, createdIdent, err := repos.Users.Provision(ctx, u, ident, user.InitialCredentials{External: &mirror})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if created.ID == "" || createdIdent.UserID != created.ID {
		t.Fatalf("Provision returned user %q, identifier owner %q", created.ID, createdIdent.UserID)
	}
	got, err := repos.ExternalSubjects.Get(ctx, "firebase", "sub-1")
	if err != nil {
		t.Fatalf("Get after Provision: %v — the store dropped InitialCredentials.External", err)
	}
	want := mirror
	want.UserID = created.ID
	if got.Provider != want.Provider || got.Subject != want.Subject || got.UserID != want.UserID ||
		got.Issuer != want.Issuer || got.Email != want.Email || !got.FirstSeenAt.Equal(want.FirstSeenAt) {
		t.Fatalf("Get = %+v, want %+v", got, want)
	}
	list, err := repos.ExternalSubjects.ListByUser(ctx, created.ID)
	if err != nil || len(list) != 1 || list[0].Subject != "sub-1" {
		t.Fatalf("ListByUser = %+v, %v; want the provisioned row", list, err)
	}
}

func testExternalProvisionSubjectCollision(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, ident, mirror := externalProvisionInput(t, "first@example.com", "firebase", "dup", notifyOnlyUses)
	first, _, err := repos.Users.Provision(ctx, u, ident, user.InitialCredentials{External: &mirror})
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}

	u2, ident2, mirror2 := externalProvisionInput(t, "second@example.com", "firebase", "dup", notifyOnlyUses)
	if _, _, err := repos.Users.Provision(ctx, u2, ident2, user.InitialCredentials{External: &mirror2}); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("colliding Provision: err=%v, want sdk.ErrAlreadyExists", err)
	}
	if _, err := repos.Users.Get(ctx, u2.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("losing user persisted: err=%v, want sdk.ErrNotFound", err)
	}
	if _, err := repos.Identifiers.Get(ctx, ident2.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("losing identifier persisted: err=%v, want sdk.ErrNotFound", err)
	}
	got, err := repos.ExternalSubjects.Get(ctx, "firebase", "dup")
	if err != nil || got.UserID != first.ID {
		t.Fatalf("mirror after collision = %+v, %v; want owner %s", got, err, first.ID)
	}

	// The key is (provider, subject): the same subject under another provider
	// name is a different identity.
	u3, ident3, mirror3 := externalProvisionInput(t, "third@example.com", "auth0", "dup", notifyOnlyUses)
	if _, _, err := repos.Users.Provision(ctx, u3, ident3, user.InitialCredentials{External: &mirror3}); err != nil {
		t.Fatalf("same subject, other provider: %v", err)
	}
}

func testExternalProvisionIdentifierCollision(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	seedDirectoryUser(t, repos, "taken@example.com", suiteBase, suiteBase)

	u, ident, mirror := externalProvisionInput(t, "taken@example.com", "firebase", "loser", loginRecoveryUses)
	if _, _, err := repos.Users.Provision(ctx, u, ident, user.InitialCredentials{External: &mirror}); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("Provision over a taken login claim: err=%v, want sdk.ErrAlreadyExists", err)
	}
	requireExternalAbsent(t, repos, "firebase", "loser")
	if _, err := repos.Users.Get(ctx, u.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("losing user persisted: err=%v, want sdk.ErrNotFound", err)
	}
}

func testExternalConcurrentProvision(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const racers = 4
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		winners   []string
		otherErrs []error
	)
	for i := range racers {
		u, ident, mirror := externalProvisionInput(t, "racer"+string(rune('a'+i))+"@example.com", "firebase", "race", notifyOnlyUses)
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, _, err := repos.Users.Provision(ctx, u, ident, user.InitialCredentials{External: &mirror})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, created.ID)
			case !errors.Is(err, sdk.ErrAlreadyExists):
				otherErrs = append(otherErrs, err)
			}
		}()
	}
	wg.Wait()
	if len(otherErrs) > 0 {
		t.Fatalf("racing Provision returned a non-collision error: %v", otherErrs)
	}
	if len(winners) != 1 {
		t.Fatalf("racing Provision winners = %d, want exactly 1", len(winners))
	}
	got, err := repos.ExternalSubjects.Get(ctx, "firebase", "race")
	if err != nil || got.UserID != winners[0] {
		t.Fatalf("mirror = %+v, %v; want owner %s", got, err, winners[0])
	}
}

func testExternalLink(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	owner := seedDirectoryUser(t, repos, "linked@example.com", suiteBase, suiteBase)
	in := linkInput(t, "firebase", "admin-linked", owner.ID)
	linked, err := repos.ExternalSubjects.Link(ctx, in)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if linked.UserID != owner.ID || linked.Provider != "firebase" || linked.Subject != "admin-linked" {
		t.Fatalf("Link returned %+v", linked)
	}
	got, err := repos.ExternalSubjects.Get(ctx, "firebase", "admin-linked")
	if err != nil || got.UserID != owner.ID || got.Issuer != in.Issuer || !got.FirstSeenAt.Equal(in.FirstSeenAt) {
		t.Fatalf("Get after Link = %+v, %v", got, err)
	}
}

func testExternalLinkTaken(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	first := seedDirectoryUser(t, repos, "owner@example.com", suiteBase, suiteBase)
	second := seedDirectoryUser(t, repos, "thief@example.com", suiteBase, suiteBase)
	if _, err := repos.ExternalSubjects.Link(ctx, linkInput(t, "firebase", "taken", first.ID)); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if _, err := repos.ExternalSubjects.Link(ctx, linkInput(t, "firebase", "taken", second.ID)); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("Link to a taken subject: err=%v, want sdk.ErrAlreadyExists", err)
	}
	got, err := repos.ExternalSubjects.Get(ctx, "firebase", "taken")
	if err != nil || got.UserID != first.ID {
		t.Fatalf("mapping after refused Link = %+v, %v; want owner %s", got, err, first.ID)
	}
}

func testExternalLinkMissingUser(t *testing.T, repos auth.Repositories) {
	if _, err := repos.ExternalSubjects.Link(context.Background(), linkInput(t, "firebase", "orphan", "no-such-user")); err == nil {
		t.Fatal("Link to a missing user succeeded")
	}
	requireExternalAbsent(t, repos, "firebase", "orphan")
}

func testExternalLinkInactiveUser(t *testing.T, repos auth.Repositories) {
	if repos.UserAdmin == nil {
		t.Skip("UserAdmin not wired — Link to an inactive user NOT verified for this Repositories")
	}
	ctx := context.Background()
	u := seedDirectoryUser(t, repos, "gone@example.com", suiteBase, suiteBase)
	if _, err := repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("SetStatus(deactivated): %v", err)
	}
	if _, err := repos.ExternalSubjects.Link(ctx, linkInput(t, "firebase", "inactive", u.ID)); err == nil {
		t.Fatal("Link to a deactivated user succeeded")
	}
	requireExternalAbsent(t, repos, "firebase", "inactive")
}

func testExternalListByUser(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if list, err := repos.ExternalSubjects.ListByUser(ctx, "no-such-user"); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("ListByUser(unknown) = %#v, %v; want an empty non-nil slice", list, err)
	}

	alice := seedDirectoryUser(t, repos, "alice-ext@example.com", suiteBase, suiteBase)
	bob := seedDirectoryUser(t, repos, "bob-ext@example.com", suiteBase, suiteBase)
	for _, link := range []externalsubject.ExternalSubject{
		linkInput(t, "firebase", "alice-1", alice.ID),
		linkInput(t, "auth0", "alice-1", alice.ID),
		linkInput(t, "firebase", "bob-1", bob.ID),
	} {
		if _, err := repos.ExternalSubjects.Link(ctx, link); err != nil {
			t.Fatalf("Link(%s, %s): %v", link.Provider, link.Subject, err)
		}
	}

	list, err := repos.ExternalSubjects.ListByUser(ctx, alice.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	var keys []string
	for _, s := range list {
		if s.UserID != alice.ID {
			t.Fatalf("ListByUser(alice) returned %+v", s)
		}
		keys = append(keys, s.Provider+"/"+s.Subject)
	}
	slices.Sort(keys)
	if got, want := strings.Join(keys, ","), "auth0/alice-1,firebase/alice-1"; got != want {
		t.Fatalf("ListByUser(alice) = %s, want %s", got, want)
	}
}

func testExternalUnlink(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if err := repos.ExternalSubjects.Unlink(ctx, "firebase", "never"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Unlink(unknown): err=%v, want sdk.ErrNotFound", err)
	}
	owner := seedDirectoryUser(t, repos, "unlink@example.com", suiteBase, suiteBase)
	if _, err := repos.ExternalSubjects.Link(ctx, linkInput(t, "firebase", "bye", owner.ID)); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := repos.ExternalSubjects.Unlink(ctx, "firebase", "bye"); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	requireExternalAbsent(t, repos, "firebase", "bye")
	if err := repos.ExternalSubjects.Unlink(ctx, "firebase", "bye"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second Unlink: err=%v, want sdk.ErrNotFound", err)
	}
	if _, err := repos.Users.Get(ctx, owner.ID); err != nil {
		t.Fatalf("Unlink touched the user: %v", err)
	}
}

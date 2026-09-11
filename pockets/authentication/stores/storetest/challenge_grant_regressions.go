package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

func testChallengeConcurrentReplace(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			purpose := fmt.Sprintf("replace-%v", existing)
			if existing {
				if _, err := repos.Challenges.Replace(ctx, newChallenge("owner", purpose, "k", "original", nil, 0, time.Hour, time.Now())); err != nil {
					t.Fatal(err)
				}
			}
			const n = 8
			results := make([]challenge.Challenge, n)
			errs := make([]error, n)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range n {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					results[i], errs[i] = repos.Challenges.Replace(ctx, newChallenge("owner", purpose, "k", fmt.Sprintf("digest-%d", i), nil, 0, time.Hour, time.Now()))
				}(i)
			}
			close(start)
			wg.Wait()
			candidates := make([]challenge.DigestCandidate, 0, n)
			for i, err := range errs {
				if err != nil {
					t.Fatalf("Replace %d: %v", i, err)
				}
				if results[i].ID == "" {
					t.Fatal("missing ID")
				}
				candidates = append(candidates, challenge.DigestCandidate{KeyID: "k", Digest: fmt.Sprintf("digest-%d", i)})
			}
			got, out, err := repos.Challenges.ConsumeCode(ctx, "owner", purpose, candidates, "", 10, time.Now())
			if err != nil || out != challenge.OutcomeRedeemed || got.UserID != "owner" {
				t.Fatalf("current generation: %+v %v %v", got, out, err)
			}
			if _, out, err := repos.Challenges.ConsumeCode(ctx, "owner", purpose, candidates, "", 10, time.Now()); err != nil || out != challenge.OutcomeNotFound {
				t.Fatalf("multiple current generations: %v %v", out, err)
			}
		})
	}
}

func testChallengeSubjectAndFailedReplacement(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	now := time.Now()
	c := newChallenge("owner", challenge.PurposeStepUp, "k", "current", []byte("binding"), 0, time.Hour, now)
	c.SubjectKey = "session-operation"
	original, err := repos.Challenges.Replace(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	other := c
	other.SubjectKey = "other-session"
	other.SecretDigest = "occupied"
	if _, err := repos.Challenges.Replace(ctx, other); err != nil {
		t.Fatal(err)
	}
	c.SecretDigest = "occupied"
	if _, err := repos.Challenges.Replace(ctx, c); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("collision: %v", err)
	}
	got, out, err := repos.Challenges.ConsumeCode(ctx, "session-operation", c.Purpose, codeCandidates("k", "current"), "binding", 5, now)
	if err != nil || out != challenge.OutcomeRedeemed || got.ID != original.ID || got.UserID != "owner" || got.SubjectKey != "session-operation" {
		t.Fatalf("failed replacement lost original or owner: %+v %v %v", got, out, err)
	}
}

func testGrantAdmissionAndPolicy(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fixture := grantFixture{repos}
	g := newGrant("s1", "u1", "operation", "binding", time.Hour, now)
	g.AuthenticatedAt = now.Add(-2 * time.Minute)
	stored, err := fixture.Create(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := repos.Users.Get(ctx, g.UserID)
	if err != nil {
		t.Fatal(err)
	}
	candidate := g
	candidate.ID = "stale-proof"
	if _, err := repos.AuthenticationGrants.Create(ctx, candidate, owner.AuthRevision+1, now); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale proof: %v", err)
	}
	req := authgrant.Requirement{SessionID: g.SessionID, UserID: g.UserID, Purpose: g.Purpose, ContextDigest: g.ContextDigest, AuthenticatedAfter: now.Add(-time.Minute), MinAssurance: session.AssuranceAAL1}
	if _, err := repos.AuthenticationGrants.Consume(ctx, req, now); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("old proof: %v", err)
	}
	req.AuthenticatedAfter = now.Add(-5 * time.Minute)
	req.MinAssurance = session.AssuranceAAL2
	if _, err := repos.AuthenticationGrants.Consume(ctx, req, now); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("weak proof: %v", err)
	}
	strong := g
	strong.ID = "strong-proof"
	strong.Assurance = session.AssuranceAAL2
	strong.CreatedAt = now.Add(time.Second)
	if _, err := repos.AuthenticationGrants.Create(ctx, strong, owner.AuthRevision, now); err != nil {
		t.Fatal(err)
	}
	got, err := repos.AuthenticationGrants.Consume(ctx, req, now)
	if err != nil || got.ID != strong.ID {
		t.Fatalf("suitable proof behind weak one: %+v %v", got, err)
	}
	req.MinAssurance = session.AssuranceAAL1
	got, err = repos.AuthenticationGrants.Consume(ctx, req, now)
	if err != nil || got.ID != stored.ID {
		t.Fatalf("unsuitable requests spent grant: %+v %v", got, err)
	}
	if _, err := repos.AuthenticationGrants.Consume(ctx, req, now); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second use: %v", err)
	}
	candidate.ID = "revoked-session-proof"
	if _, err := repos.AuthenticationGrants.Create(ctx, candidate, owner.AuthRevision, now); err != nil {
		t.Fatal(err)
	}
	if err := repos.Sessions.Delete(ctx, g.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.AuthenticationGrants.Consume(ctx, req, now); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
	if _, err := repos.AuthenticationGrants.Create(ctx, candidate, owner.AuthRevision, now); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("admission after deletion: %v", err)
	}
}

// contactFixture gives the original expiry/round-trip cases their current ID.
// The generation-race case below passes captured IDs directly.
type contactFixture struct{ contactchange.Repository }

func (f contactFixture) Consume(ctx context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	p, err := f.Get(ctx, userID, kind)
	if err != nil {
		return contactchange.PendingChange{}, err
	}
	return f.Repository.Consume(ctx, userID, kind, p.ID)
}

func testContactChangeGeneration(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ContactChanges
	original, err := repo.Create(ctx, newEmailChange("owner", "old@example.com", loginRecoveryUses, true, "", time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = repo.Create(ctx, newEmailChange("owner", fmt.Sprintf("%d@example.com", i), loginRecoveryUses, true, "", time.Hour))
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Create %d: %v", i, err)
		}
	}
	current, err := repo.Get(ctx, "owner", identifier.KindEmail)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Consume(ctx, "owner", identifier.KindEmail, original.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("stale generation: %v", err)
	}
	got, err := repo.Consume(ctx, "owner", identifier.KindEmail, current.ID)
	if err != nil || got.ID != current.ID {
		t.Fatalf("newer pending change lost: %+v %v", got, err)
	}
}

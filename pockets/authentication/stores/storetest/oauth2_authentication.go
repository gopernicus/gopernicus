package storetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// codeAuthenticationDeadline bounds the concurrent approve-and-rotate case: a
// store that deadlocks fails here instead of hanging the suite.
const codeAuthenticationDeadline = 10 * time.Second

// RunOAuth2CodeAuthentication exercises the OPTIONAL delegated-assurance
// capability (oauth2.CodeAuthenticationRepository, migration 0030) over a
// MIGRATED store: the approving session's Authentication is copied into the
// code and read back, a row recording nothing reads back zero, the plain
// methods still work on the same rows, the redeemed delegated session persists
// the code's Authentication, the RunOAuth2 cases pass over the
// WithAuthentication methods, and a concurrent rotation of the approving
// session during approval completes without deadlock. It runs outside Run and
// skips when the store does not implement the interface. The stores' own tests
// prove ProbeSchema reports sdk.ErrNotFound on an unmigrated schema.
func RunOAuth2CodeAuthentication(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("OAuth2CodeAuthentication", func(t *testing.T) {
		r := newRepos(t)
		if r.OAuth2 == nil || r.SessionManagement == nil {
			t.Skip("OAuth2/SessionManagement not wired — delegated assurance NOT verified")
		}
		if _, ok := r.OAuth2.(oauth2.CodeAuthenticationRepository); !ok {
			t.Skip("OAuth2 does not implement oauth2.CodeAuthenticationRepository — delegated assurance NOT verified")
		}
		for name, test := range map[string]func(*testing.T, auth.Repositories){
			"ProbeSchemaMigrated":       testCodeAuthenticationProbe,
			"CopiesApprovingSession":    testCodeAuthenticationCopies,
			"RecordsNothingAsZero":      testCodeAuthenticationZero,
			"PlainMethodsShareTheRow":   testCodeAuthenticationPlain,
			"RedeemPersistsTheCopy":     testCodeAuthenticationRedeem,
			"ConcurrentRotateOnApprove": testCodeAuthenticationConcurrentRotate,
		} {
			t.Run(name, func(t *testing.T) { test(t, newRepos(t)) })
		}
		RunOAuth2(t, func(t *testing.T) auth.Repositories {
			r := newRepos(t)
			r.OAuth2 = viaCodeAuthentication{r.OAuth2, r.OAuth2.(oauth2.CodeAuthenticationRepository)}
			return r
		})
	})
}

// viaCodeAuthentication routes CreateCode and GetCode through the
// WithAuthentication methods, so RunOAuth2 proves they keep the plain
// contract.
type viaCodeAuthentication struct {
	oauth2.Repository
	with oauth2.CodeAuthenticationRepository
}

func (v viaCodeAuthentication) CreateCode(ctx context.Context, c oauth2.Code, approvingSessionID string) error {
	return v.with.CreateCodeWithAuthentication(ctx, c, approvingSessionID)
}

func (v viaCodeAuthentication) GetCode(ctx context.Context, hash string) (oauth2.Code, error) {
	return v.with.GetCodeWithAuthentication(ctx, hash)
}

// codeAuthentication returns r.OAuth2's optional capability.
func codeAuthentication(r auth.Repositories) oauth2.CodeAuthenticationRepository {
	return r.OAuth2.(oauth2.CodeAuthenticationRepository)
}

// aal2Authentication is an AAL2 approval: a password then a user-verified
// passkey, at a microsecond-truncated instant every dialect round-trips.
func aal2Authentication(now time.Time) session.AuthenticationMetadata {
	password, _ := session.DescribeMethod(session.MethodPassword)
	passkey, _ := session.DescribeMethod(session.MethodPasskey)
	return session.AuthenticationMetadata{AuthenticatedAt: now.Add(-time.Minute), Methods: []session.AuthenticationMethod{password, passkey}, Assurance: session.AssuranceAAL2}
}

// approvingFixture is newOAuthFixture whose approving browser session records
// recorded (zero for a session that recorded nothing).
func approvingFixture(t *testing.T, r auth.Repositories, recorded session.AuthenticationMetadata) oauthFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	u := seedDirectoryUser(t, r, "oauth-assurance@example.test", now, now)
	w, _ := session.NewSession(u.ID, time.Hour, now)
	w.RefreshTokenHash = "web-hash-" + w.ID
	w.Authentication = recorded
	if _, err := r.Sessions.Create(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	return oauthFixture{r, u, w, now}
}

// unrecordedCode is f.code's binding without creating it, carrying a bogus
// Authentication every create must ignore.
func (f oauthFixture) unrecordedCode() oauth2.Code {
	c := oauth2.Code{Hash: "code-hash-assurance-" + (sdk.IDGenerator{}).MustGenerate(), UserID: f.u.ID, AuthRevision: f.u.AuthRevision,
		Delegation:  session.Delegation{AuthRevision: f.u.AuthRevision, Issuer: "https://auth.example.test", ClientID: "https://client.example.test/metadata.json", Resource: "https://mcp.example.test", ExchangeResource: "https://api.example.test", ExchangeClientID: "mcp-server"},
		RedirectURI: "https://client.example.test/callback", CodeChallenge: "01234567890123456789012345678901234567890123456", CreatedAt: f.now, ExpiresAt: f.now.Add(time.Minute),
	}
	c.Authentication = session.AuthenticationMetadata{AuthenticatedAt: f.now.Add(-time.Hour), Assurance: session.AssuranceAAL3}
	return c
}

// sameBinding compares two codes' binding fields — everything but the carried
// Authentication — with instants by Equal.
func sameBinding(a, b oauth2.Code) bool {
	return a.Hash == b.Hash && a.UserID == b.UserID && a.AuthRevision == b.AuthRevision && a.Delegation == b.Delegation &&
		a.RedirectURI == b.RedirectURI && a.CodeChallenge == b.CodeChallenge && a.CreatedAt.Equal(b.CreatedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
}

// sameAuthentication compares two records the way a dialect round-trips them:
// the instant by Equal, the methods and level exactly.
func sameAuthentication(a, b session.AuthenticationMetadata) bool {
	return a.AuthenticatedAt.Equal(b.AuthenticatedAt) && a.Assurance == b.Assurance && reflect.DeepEqual(a.Methods, b.Methods)
}

func testCodeAuthenticationProbe(t *testing.T, r auth.Repositories) {
	if err := codeAuthentication(r).ProbeSchema(context.Background()); err != nil {
		t.Fatalf("ProbeSchema on a migrated store = %v, want nil", err)
	}
}

func testCodeAuthenticationCopies(t *testing.T, r auth.Repositories) {
	want := aal2Authentication(time.Now().UTC().Truncate(time.Microsecond))
	f := approvingFixture(t, r, want)
	c := f.unrecordedCode()
	if err := codeAuthentication(r).CreateCodeWithAuthentication(context.Background(), c, f.w.ID); err != nil {
		t.Fatal(err)
	}
	got, err := codeAuthentication(r).GetCodeWithAuthentication(context.Background(), c.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !sameAuthentication(got.Authentication, want) {
		t.Fatalf("code Authentication = %+v, want the approving session's %+v (code.Authentication is ignored)", got.Authentication, want)
	}
	if !sameBinding(got, c) {
		t.Fatalf("binding lost through WithAuthentication: %+v", got)
	}
}

func testCodeAuthenticationZero(t *testing.T, r auth.Repositories) {
	f := approvingFixture(t, r, session.AuthenticationMetadata{})
	c := f.unrecordedCode()
	if err := codeAuthentication(r).CreateCodeWithAuthentication(context.Background(), c, f.w.ID); err != nil {
		t.Fatal(err)
	}
	got, err := codeAuthentication(r).GetCodeWithAuthentication(context.Background(), c.Hash)
	if err != nil || !reflect.DeepEqual(got.Authentication, session.AuthenticationMetadata{}) {
		t.Fatalf("a session recording nothing copied %+v (%v), want zero", got.Authentication, err)
	}
}

func testCodeAuthenticationPlain(t *testing.T, r auth.Repositories) {
	f := approvingFixture(t, r, aal2Authentication(time.Now().UTC().Truncate(time.Microsecond)))
	recorded := f.unrecordedCode()
	if err := codeAuthentication(r).CreateCodeWithAuthentication(context.Background(), recorded, f.w.ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.OAuth2.GetCode(context.Background(), recorded.Hash)
	if err != nil || !sameBinding(got, recorded) || !reflect.DeepEqual(got.Authentication, session.AuthenticationMetadata{}) {
		t.Fatalf("plain GetCode on a recorded row = %+v %v, want the binding and no Authentication", got, err)
	}

	plain := f.unrecordedCode()
	plain.Hash += "-plain"
	if err := r.OAuth2.CreateCode(context.Background(), plain, f.w.ID); err != nil {
		t.Fatal(err)
	}
	got, err = codeAuthentication(r).GetCodeWithAuthentication(context.Background(), plain.Hash)
	if err != nil || !reflect.DeepEqual(got.Authentication, session.AuthenticationMetadata{}) {
		t.Fatalf("plain CreateCode read WithAuthentication = %+v %v, want zero (NULL)", got.Authentication, err)
	}
}

func testCodeAuthenticationRedeem(t *testing.T, r auth.Repositories) {
	want := aal2Authentication(time.Now().UTC().Truncate(time.Microsecond))
	f := approvingFixture(t, r, want)
	c := f.unrecordedCode()
	if err := codeAuthentication(r).CreateCodeWithAuthentication(context.Background(), c, f.w.ID); err != nil {
		t.Fatal(err)
	}
	code, err := codeAuthentication(r).GetCodeWithAuthentication(context.Background(), c.Hash)
	if err != nil {
		t.Fatal(err)
	}
	proposed := f.proposed(code)
	proposed.Authentication = code.Authentication
	if _, err := r.OAuth2.RedeemCode(context.Background(), code, proposed, f.now); err != nil {
		t.Fatalf("RedeemCode with the read-back Authentication: %v", err)
	}
	stored, err := r.Sessions.Get(context.Background(), proposed.ID)
	if err != nil || stored.Profile != session.ProfileDelegated || !sameAuthentication(stored.Authentication, want) {
		t.Fatalf("delegated session Authentication = %+v (%v), want the code's %+v", stored.Authentication, err, want)
	}
	if _, err := codeAuthentication(r).GetCodeWithAuthentication(context.Background(), c.Hash); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("redeemed code still readable WithAuthentication: %v", err)
	}
}

// testCodeAuthenticationConcurrentRotate races an approval against a refresh
// rotation of the same approving session: the copy reads the row the approval
// already locks, so both must complete (SRE-6).
func testCodeAuthenticationConcurrentRotate(t *testing.T, r auth.Repositories) {
	f := approvingFixture(t, r, aal2Authentication(time.Now().UTC().Truncate(time.Microsecond)))
	ctx, cancel := context.WithTimeout(context.Background(), codeAuthenticationDeadline)
	defer cancel()
	const rounds = 8
	errs := make(chan error, 2*rounds)
	var wg sync.WaitGroup
	current := f.w.RefreshTokenHash
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range rounds {
			c := f.unrecordedCode()
			c.Hash += fmt.Sprintf("-race-%d", i)
			errs <- codeAuthentication(r).CreateCodeWithAuthentication(ctx, c, f.w.ID)
		}
	}()
	go func() {
		defer wg.Done()
		hash := current
		for i := range rounds {
			next := fmt.Sprintf("web-hash-rotated-%d-%s", i, f.w.ID)
			if a, ok := r.Sessions.(session.ActivityRepository); ok {
				errs <- a.RotateAt(ctx, f.w.ID, hash, next, time.Now().UTC())
			} else {
				errs <- r.Sessions.Rotate(ctx, f.w.ID, hash, next)
			}
			hash = next
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("approval and rotation of the approving session did not both complete: deadlock or stall")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent approve/rotate: %v", err)
		}
	}
}

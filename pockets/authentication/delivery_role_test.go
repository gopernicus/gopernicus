package authentication

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	deliverycmd "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery/command"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// roleConfig is the jobs-mode production wiring every role case starts from: a
// recording dispatcher, the encrypter, the runtime acknowledgment and a valid
// From address.
func roleConfig(disp delivery.Dispatcher, role delivery.Role) constructorConfig {
	c := deliveryProdConfig()
	c.DeliveryMode = delivery.ModeJobs
	c.DeliveryRole = role
	c.DeliveryEncrypter = stubEncrypter{}
	c.DeliveryDispatcher = disp
	c.DeliveryJobsAcknowledged = true
	c.MailFrom = "no-reply@example.com"
	return c
}

// newRole calls New directly, so the case controls MailFrom (newFixture fills
// an empty one in).
func newRole(repos Repositories, cfg constructorConfig) (*Components, error) {
	return New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
		WithPassword(PasswordConfig{Hasher: cfg.Hasher, PasswordFlowsDisabled: cfg.PasswordFlowsDisabled}),
		WithIdentity(IdentityConfig{ChallengeProtector: cfg.ChallengeProtector, IdentifierKeyer: cfg.IdentifierKeyer, SingleFactorAcknowledged: cfg.SingleFactorAcknowledged}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: cfg.RateLimiter}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer, MailFrom: cfg.MailFrom, DeliveryEncrypter: cfg.DeliveryEncrypter, DeliveryDispatcher: cfg.DeliveryDispatcher,
			DeliveryJobsAcknowledged: cfg.DeliveryJobsAcknowledged, DeliveryEphemeralAcknowledged: cfg.DeliveryEphemeralAcknowledged,
			InProcessDelivery: cfg.InProcessDelivery, Role: cfg.DeliveryRole, JobsDelivery: cfg.JobsDelivery}),
		WithLinks(LinksConfig{PasswordResetURL: cfg.PasswordResetURL}),
		WithBrowser(BrowserConfig{SessionCookie: cfg.SessionCookie}),
		WithAuditDisabledAcknowledged(),
		WithLogger(cfg.Logger))
}

// TestDeliveryRoleBothNeedsMailerAndSigner pins the zero role to the
// pre-role matrix: a delivering instance needs a Mailer and a signer.
func TestDeliveryRoleBothNeedsMailerAndSigner(t *testing.T) {
	for _, role := range []delivery.Role{"", delivery.RoleBoth} {
		cfg := roleConfig(&recordingDispatcher{}, role)
		cfg.Mailer = nil
		if _, err := newRole(testRepositories(Repositories{}), cfg); !errors.Is(err, ErrMailerRequired) {
			t.Errorf("role %q, nil Mailer: err = %v, want ErrMailerRequired", role, err)
		}
		cfg = roleConfig(&recordingDispatcher{}, role)
		cfg.TokenSigner = nil
		if _, err := newRole(testRepositories(Repositories{}), cfg); !errors.Is(err, ErrTokenSignerRequired) {
			t.Errorf("role %q, nil signer: err = %v, want ErrTokenSignerRequired", role, err)
		}
	}
}

// TestDeliveryRoleEnqueueBootsWithoutMailer proves an enqueue-only production
// instance boots with no Mailer and no MailFrom, builds no processor, and
// still seals what it admits onto the dispatcher.
func TestDeliveryRoleEnqueueBootsWithoutMailer(t *testing.T) {
	disp := &recordingDispatcher{}
	log, buf := bufferLog()
	cfg := roleConfig(disp, delivery.RoleEnqueue)
	cfg.Mailer = nil
	cfg.MailFrom = ""
	cfg.Logger = log
	cfg.PasswordFlowsDisabled = false
	cfg.SingleFactorAcknowledged = true
	cfg.ChallengeProtector = passwordlessProtector(t)
	cfg.PasswordResetURL = "https://app.example.com/reset"
	svc, err := newRole(testRepositories(Repositories{Challenges: stubChallenges{}, PasswordResets: configPasswordResets{}}), cfg)
	if err != nil {
		t.Fatalf("enqueue role, nil Mailer, production: %v", err)
	}
	if _, ok := svc.Delivery.JobRuntime(); ok {
		t.Fatal("an enqueue-only instance exposed the delivery job runtime")
	}
	if !strings.Contains(buf.String(), "role=enqueue mailer=absent") {
		t.Fatalf("boot log lacks the enqueue delivery line:\n%s", buf.String())
	}

	if err := svc.Authentication.ForgotPassword(context.Background(), "user@example.com"); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	calls := disp.calls()
	if len(calls) != 1 {
		t.Fatalf("dispatcher submits = %d, want 1", len(calls))
	}
	if payload := string(calls[0].payload); !strings.Contains(payload, `"stage":"opaque"`) || strings.Contains(payload, `"secret"`) {
		t.Fatalf("enqueue-only admission is not a sealed opaque command: %s", payload)
	}
}

// TestDeliveryRoleProcessInstallsRefusingSigner proves a process-only instance
// boots with no signer and runs on one that refuses.
func TestDeliveryRoleProcessInstallsRefusingSigner(t *testing.T) {
	log, buf := bufferLog()
	cfg := roleConfig(&recordingDispatcher{}, delivery.RoleProcess)
	cfg.TokenSigner = nil
	cfg.Logger = log
	svc, err := newRole(testRepositories(Repositories{}), cfg)
	if err != nil {
		t.Fatalf("process role, nil signer: %v", err)
	}
	if _, ok := svc.Delivery.JobRuntime(); !ok {
		t.Fatal("a process instance did not expose the delivery job runtime")
	}
	if !strings.Contains(buf.String(), "jwt_signer=refusing") {
		t.Fatalf("boot log does not report the refusing signer:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "role=process mailer=wired jobs_max_attempts=10") {
		t.Fatalf("boot log lacks the process delivery line:\n%s", buf.String())
	}
	var signer RefusingSigner
	if _, err := signer.Sign(map[string]any{"sub": "u"}, time.Now().Add(time.Minute)); !errors.Is(err, ErrSignerRefused) {
		t.Errorf("Sign: err = %v, want ErrSignerRefused", err)
	}
	if _, err := signer.Verify("token"); !errors.Is(err, ErrSignerRefused) {
		t.Errorf("Verify: err = %v, want ErrSignerRefused", err)
	}
}

// TestDeliveryRoleRequiresJobs refuses a split role outside DeliveryMode jobs,
// and an unknown role in any mode.
func TestDeliveryRoleRequiresJobs(t *testing.T) {
	for _, mode := range []delivery.Mode{delivery.ModeOff, delivery.ModeInProcess} {
		for _, role := range []delivery.Role{delivery.RoleEnqueue, delivery.RoleProcess} {
			cfg := roleConfig(nil, role)
			cfg.DeliveryMode = mode
			cfg.DeliveryDispatcher = nil
			cfg.DeliveryEphemeralAcknowledged = true
			if _, err := newRole(testRepositories(Repositories{}), cfg); !errors.Is(err, ErrDeliveryRoleRequiresJobs) {
				t.Errorf("mode %q role %q: err = %v, want ErrDeliveryRoleRequiresJobs", mode, role, err)
			}
		}
	}
	cfg := roleConfig(&recordingDispatcher{}, "worker")
	if _, err := newRole(testRepositories(Repositories{}), cfg); !errors.Is(err, ErrDeliveryRoleInvalid) {
		t.Errorf("unknown role: err = %v, want ErrDeliveryRoleInvalid", err)
	}
}

// TestDeliveryJobsMaxAttemptsNegative refuses a negative jobs attempt cap.
func TestDeliveryJobsMaxAttemptsNegative(t *testing.T) {
	cfg := roleConfig(&recordingDispatcher{}, delivery.RoleBoth)
	cfg.JobsDelivery = delivery.JobsConfig{MaxAttempts: -1}
	if _, err := newRole(testRepositories(Repositories{}), cfg); !errors.Is(err, delivery.ErrJobsMaxAttemptsInvalid) {
		t.Errorf("negative MaxAttempts: err = %v, want ErrJobsMaxAttemptsInvalid", err)
	}
}

// TestDeliveryMailFrom covers MailFrom across role and mode: empty is refused
// only where the instance sends; a malformed value is refused everywhere.
func TestDeliveryMailFrom(t *testing.T) {
	cases := []struct {
		name     string
		mode     delivery.Mode
		role     delivery.Role
		mailFrom string
		want     error
	}{
		{"off empty", delivery.ModeOff, "", "", nil},
		{"in_process empty", delivery.ModeInProcess, "", "", ErrMailFromRequired},
		{"jobs both empty", delivery.ModeJobs, delivery.RoleBoth, "", ErrMailFromRequired},
		{"jobs process empty", delivery.ModeJobs, delivery.RoleProcess, "", ErrMailFromRequired},
		{"jobs enqueue empty", delivery.ModeJobs, delivery.RoleEnqueue, "", nil},
		{"off malformed", delivery.ModeOff, "", "no-reply", ErrMailFromInvalid},
		{"jobs enqueue malformed", delivery.ModeJobs, delivery.RoleEnqueue, "no-reply", ErrMailFromInvalid},
		{"jobs both malformed", delivery.ModeJobs, delivery.RoleBoth, "no-reply", ErrMailFromInvalid},
		{"jobs both display name", delivery.ModeJobs, delivery.RoleBoth, "Example Co <no-reply@example.com>", nil},
		{"in_process bare address", delivery.ModeInProcess, "", "no-reply@example.com", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := roleConfig(&recordingDispatcher{}, tc.role)
			cfg.DeliveryMode = tc.mode
			cfg.MailFrom = tc.mailFrom
			switch tc.mode {
			case delivery.ModeOff:
				cfg.DeliveryDispatcher = nil
			case delivery.ModeInProcess:
				cfg.DeliveryDispatcher = nil
				cfg.DeliveryEphemeralAcknowledged = true
			}
			_, err := newRole(testRepositories(Repositories{}), cfg)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestDeliveryBootLineEveryMode proves the auth: delivery line is logged in
// every mode.
func TestDeliveryBootLineEveryMode(t *testing.T) {
	for _, mode := range []delivery.Mode{delivery.ModeOff, delivery.ModeInProcess, delivery.ModeJobs} {
		log, buf := bufferLog()
		cfg := roleConfig(&recordingDispatcher{}, "")
		cfg.DeliveryMode = mode
		cfg.Logger = log
		cfg.RuntimeMode = environment.ModeDevelopment
		if mode != delivery.ModeJobs {
			cfg.DeliveryDispatcher = nil
		}
		if _, err := newRole(testRepositories(Repositories{}), cfg); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if want := "msg=\"auth: delivery\" mode=" + string(mode) + " role=both mailer=wired"; !strings.Contains(buf.String(), want) {
			t.Errorf("mode %q: boot log lacks %q:\n%s", mode, want, buf.String())
		}
	}
}

// downMailer is a production-capable Mailer whose provider is down.
type downMailer struct{ prodMailer }

func (downMailer) Send(context.Context, email.Message) error {
	return errors.New("provider unavailable")
}

// TestJobsDeliveryMaxAttemptsReachesProcessor proves New hands the jobs-mode
// processor the configured attempt cap — ten by default — rather than leaving
// the engine on its own five.
func TestJobsDeliveryMaxAttemptsReachesProcessor(t *testing.T) {
	env, err := deliverycmd.NewRendered(sdk.AddressKindEmail, delivery.PurposeLoginCode, "cap", "user@example.com", "Your sign-in code", "code 123456", "", "123456")
	if err != nil {
		t.Fatalf("NewRendered: %v", err)
	}
	payload, err := deliverycmd.Seal(stubEncrypter{}, env)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	checkpoint := func(context.Context, []byte) error { return nil }
	for _, tc := range []struct {
		name string
		cfg  delivery.JobsConfig
		want int
	}{
		{"default", delivery.JobsConfig{}, defaultJobsMaxAttempts},
		{"configured", delivery.JobsConfig{MaxAttempts: 7}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := roleConfig(&recordingDispatcher{}, delivery.RoleProcess)
			cfg.Mailer = downMailer{}
			cfg.JobsDelivery = tc.cfg
			svc, err := newRole(testRepositories(Repositories{}), cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			rt, ok := svc.Delivery.JobRuntime()
			if !ok {
				t.Fatal("jobs runtime not exposed")
			}
			for attempt := 1; attempt <= tc.want; attempt++ {
				err := rt.Handle(context.Background(), delivery.Claim{ExecutionID: "exec-cap", Payload: payload, Attempt: attempt, Checkpoint: checkpoint})
				if err == nil {
					t.Fatalf("attempt %d delivered through a down provider", attempt)
				}
				if got, want := delivery.HandleErrorPermanent(err), attempt == tc.want; got != want {
					t.Fatalf("attempt %d: permanent = %v, want %v", attempt, got, want)
				}
			}
		})
	}
}

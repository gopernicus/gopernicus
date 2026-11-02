package authorization

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestAuditExpiryWiring(t *testing.T) {
	store := memory.New(memory.WithAudit())
	for _, tc := range []struct {
		name  string
		repos Repositories
		opts  []Option
		want  error
	}{
		{"expiry without Mutations", Repositories{Tuples: store.Tuples(), AuditExpiry: store.AuditExpiry()}, nil, sdk.ErrInvalidInput},
		{"typed-nil expiry", Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), AuditExpiry: (*memory.AuditExpiry)(nil)}, nil, sdk.ErrInvalidInput},
		{"retention without expiry", Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}, []Option{WithAuditRetention(MinAuditRetention)}, ErrAuditRetentionWithoutExpirer},
		{"retention without Mutations", Repositories{Tuples: store.Tuples()}, []Option{WithAuditRetention(MinAuditRetention)}, ErrAuditRetentionWithoutExpirer},
		{"retention at the store floor", Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), AuditExpiry: store.AuditExpiry()}, []Option{WithAuditRetention(365 * 24 * time.Hour)}, ErrAuditRetentionTooShort},
		{"explicit zero retention", Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), AuditExpiry: store.AuditExpiry()}, []Option{WithAuditRetention(0)}, ErrAuditRetentionTooShort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.repos, tc.opts...); !errors.Is(err, tc.want) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("New = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := New(Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), AuditExpiry: store.AuditExpiry()}, WithAuditRetention(MinAuditRetention)); err != nil {
		t.Fatalf("366-day retention refused: %v", err)
	}
}

func TestExpireAuditThroughComponents(t *testing.T) {
	store := memory.New(memory.WithAudit())
	repos := Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), AuditExpiry: store.AuditExpiry(), Audit: store.Audit()}
	unbounded := mustComponents(t, repos)
	if n, err := unbounded.Mutations.ExpireAudit(t.Context(), 10); n != 0 || !errors.Is(err, ErrAuditRetentionNotConfigured) || !errors.Is(err, mutations.ErrAuditRetentionNotConfigured) {
		t.Fatalf("ExpireAudit without a retention: %d %v", n, err)
	}
	unwired := mustComponents(t, Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()})
	if _, err := unwired.Mutations.ExpireAudit(t.Context(), 10); !errors.Is(err, mutations.ErrAuditExpiryNotConfigured) {
		t.Fatalf("ExpireAudit without AuditExpiry: %v", err)
	}
	comps := mustComponents(t, repos, WithAuditRetention(MinAuditRetention))
	if _, err := comps.Mutations.Apply(audit.WithSource(t.Context(), audit.Source{System: "retention"}), validGrantCommand(t)); err != nil {
		t.Fatal(err)
	}
	if n, err := comps.Mutations.ExpireAudit(t.Context(), 10); n != 0 || err != nil {
		t.Fatalf("ExpireAudit kept nothing inside retention: %d %v", n, err)
	}
	page, err := comps.Audit.List(t.Context(), audit.Filter{}, list.Request{Limit: 10})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("history inside retention was purged: %+v %v", page, err)
	}
}

func TestRegistrationLineReportsAuditRetention(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
		want string
	}{
		{"unset", nil, "audit_retention=unbounded"},
		{"set", []Option{WithAuditRetention(400 * 24 * time.Hour)}, "audit_retention=9600h0m0s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger, logs := postureLogger()
			store := memory.New(memory.WithAudit())
			repos := Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), OperationExpiry: store.OperationExpiry(), AuditExpiry: store.AuditExpiry(), Audit: store.Audit()}
			comps, err := New(repos, append([]Option{WithRuntimeMode(environment.ModeProduction), WithLogger(logger), WithDecisionObserver(ignoreDecisions)}, tc.opts...)...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "level=WARN") {
				t.Fatalf("an audit retention posture warned: %s", logs.String())
			}
			if line := registrationLine(t, comps, logs); !strings.Contains(line, tc.want) {
				t.Errorf("registration line lacks %s: %s", tc.want, line)
			}
		})
	}
}

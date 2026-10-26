package authorization

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	absentRecordingText = "does not report AuditRecording() (stores pgx < v0.19.0 / turso < v0.18.0, or a host wrapper hides it — forward the method)"
	falseRecordingText  = "reports AuditRecording() false (construct the store WithAudit())"
	nilReaderText       = "Repositories.Audit is nil"
	waiverHintText      = "WithAuditDisabledAcknowledged()"
	waiverLogText       = "posture_waiver=AuditDisabledAcknowledged"
	observerWarnText    = "denials are not durably recorded; wire WithDecisionObserver"
	expiryWarnText      = "operation records are never expired; wire Repositories.OperationExpiry"
)

func ignoreDecisions(context.Context, decisions.DecisionEvent) {}

// Host wrappers that do not forward AuditRecording.
type hiddenRecordingTuples struct{ tuples.Storer }
type hiddenRecordingMutations struct{ mutations.MutationRepository }

func postureRepos(recording, reader bool) Repositories {
	var opts []memory.Option
	if recording {
		opts = append(opts, memory.WithAudit())
	}
	store := memory.New(opts...)
	repos := Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), OperationExpiry: store.OperationExpiry()}
	if reader {
		repos.Audit = store.Audit()
	}
	return repos
}

func postureLogger() (*slog.Logger, *bytes.Buffer) {
	var logs bytes.Buffer
	return slog.New(slog.NewTextHandler(&logs, nil)), &logs
}

func registrationLine(t *testing.T, comps Components, logs *bytes.Buffer) string {
	t.Helper()
	logs.Reset()
	if err := comps.Register(pockets.Mount{}); err != nil {
		t.Fatal(err)
	}
	return logs.String()
}

func TestProductionRefusesAuditWiringThatCannotRecordAndRead(t *testing.T) {
	hideTuples := postureRepos(true, true)
	hideTuples.Tuples = hiddenRecordingTuples{hideTuples.Tuples}
	hideMutations := postureRepos(true, true)
	hideMutations.Mutations = hiddenRecordingMutations{hideMutations.Mutations}
	for _, tc := range []struct {
		name     string
		repos    Repositories
		want     []string
		notWant  []string
		waivable bool
	}{
		{"no reader", postureRepos(true, false), []string{nilReaderText}, []string{"AuditRecording()"}, false},
		{"store without WithAudit", postureRepos(false, true), []string{"Repositories.Tuples " + falseRecordingText, "Repositories.Mutations " + falseRecordingText}, []string{absentRecordingText, nilReaderText}, true},
		{"tuple wrapper hides the method", hideTuples, []string{"Repositories.Tuples " + absentRecordingText}, []string{falseRecordingText, "Repositories.Mutations", nilReaderText}, true},
		{"mutation wrapper hides the method", hideMutations, []string{"Repositories.Mutations " + absentRecordingText}, []string{falseRecordingText, "Repositories.Tuples", nilReaderText}, true},
		{"every condition", postureRepos(false, false), []string{"Repositories.Tuples " + falseRecordingText, nilReaderText}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger, logs := postureLogger()
			comps, err := New(tc.repos, WithRuntimeMode(environment.ModeProduction), WithLogger(logger))
			if !errors.Is(err, ErrAuditRequired) {
				t.Fatalf("New = %v; want ErrAuditRequired", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(err.Error(), notWant) {
					t.Errorf("error %q names an unrelated case %q", err, notWant)
				}
			}
			if got := strings.Contains(err.Error(), waiverHintText); got != tc.waivable {
				t.Errorf("error %q offers the acknowledgement = %v; want %v", err, got, tc.waivable)
			}
			if comps.Decisions != nil || comps.HTTP != nil || logs.Len() != 0 {
				t.Fatalf("refused construction returned components or logged: %s", logs.String())
			}
		})
	}
}

func TestProductionConstructsWithRecordingAuthorityAndReader(t *testing.T) {
	logger, logs := postureLogger()
	comps, err := New(postureRepos(true, true), WithRuntimeMode(environment.ModeProduction), WithLogger(logger), WithDecisionObserver(ignoreDecisions))
	if err != nil {
		t.Fatal(err)
	}
	if comps.Audit == nil {
		t.Fatal("Components.Audit does not expose Repositories.Audit")
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("satisfied posture warned: %s", logs.String())
	}
	line := registrationLine(t, comps, logs)
	for _, want := range []string{"audit_recording=true", "audit_reader=true", "decision_observer=true", "operation_expiry=true", "runtime_mode=production"} {
		if !strings.Contains(line, want) {
			t.Errorf("registration line lacks %s: %s", want, line)
		}
	}
}

func TestProductionWarnsOnceWithoutDecisionObserver(t *testing.T) {
	for _, mode := range []environment.Mode{"", environment.ModeDevelopment, environment.ModeProduction} {
		t.Run(mode.String(), func(t *testing.T) {
			logger, logs := postureLogger()
			comps, err := New(postureRepos(true, true), WithRuntimeMode(mode), WithLogger(logger))
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode.IsProduction() {
				want = 1
			}
			if n := strings.Count(logs.String(), observerWarnText); n != want || strings.Count(logs.String(), "level=WARN") != want {
				t.Fatalf("observer warnings=%d, want %d: %s", n, want, logs.String())
			}
			if line := registrationLine(t, comps, logs); !strings.Contains(line, "decision_observer=false") {
				t.Errorf("registration line lacks decision_observer=false: %s", line)
			}
		})
	}
}

func TestProductionWarnsOnceWithoutOperationExpiry(t *testing.T) {
	for _, mode := range []environment.Mode{"", environment.ModeDevelopment, environment.ModeProduction} {
		t.Run(mode.String(), func(t *testing.T) {
			logger, logs := postureLogger()
			repos := postureRepos(true, true)
			repos.OperationExpiry = nil
			comps, err := New(repos, WithRuntimeMode(mode), WithLogger(logger), WithDecisionObserver(ignoreDecisions))
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode.IsProduction() {
				want = 1
			}
			if n := strings.Count(logs.String(), expiryWarnText); n != want || strings.Count(logs.String(), "level=WARN") != want {
				t.Fatalf("expiry warnings=%d, want %d: %s", n, want, logs.String())
			}
			if line := registrationLine(t, comps, logs); !strings.Contains(line, "operation_expiry=false") {
				t.Errorf("registration line lacks operation_expiry=false: %s", line)
			}
		})
	}
	// Without Mutations nothing records operations, so there is nothing to expire.
	logger, logs := postureLogger()
	store := memory.New(memory.WithAudit())
	if _, err := New(Repositories{Tuples: store.Tuples(), Audit: store.Audit()}, WithRuntimeMode(environment.ModeProduction), WithLogger(logger), WithDecisionObserver(ignoreDecisions)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("a pocket without Mutations warned: %s", logs.String())
	}
}

func TestAuditAcknowledgementWaivesNonRecordingAuthority(t *testing.T) {
	for _, mode := range []environment.Mode{environment.ModeProduction, environment.ModeDevelopment} {
		t.Run(mode.String(), func(t *testing.T) {
			logger, logs := postureLogger()
			comps, err := New(postureRepos(false, false), WithRuntimeMode(mode), WithAuditDisabledAcknowledged(), WithLogger(logger), WithDecisionObserver(ignoreDecisions))
			if err != nil {
				t.Fatalf("acknowledged non-recording authority: %v", err)
			}
			if n := strings.Count(logs.String(), "level=WARN"); n != 1 || !strings.Contains(logs.String(), waiverLogText) {
				t.Fatalf("want exactly one %s warning, got %d: %s", waiverLogText, n, logs.String())
			}
			line := registrationLine(t, comps, logs)
			for _, want := range []string{"audit_recording=false", "audit_reader=false", "runtime_mode=" + mode.String()} {
				if !strings.Contains(line, want) {
					t.Errorf("registration line lacks %s: %s", want, line)
				}
			}
		})
	}
}

func TestAuditAcknowledgementBesideRecordingAuthorityIsRefusedInEveryMode(t *testing.T) {
	for _, mode := range []environment.Mode{"", environment.ModeDevelopment, environment.ModeProduction} {
		for _, reader := range []bool{true, false} {
			_, err := New(postureRepos(true, reader), WithRuntimeMode(mode), WithAuditDisabledAcknowledged())
			if !errors.Is(err, ErrAuditAcknowledgedWithRecording) {
				t.Errorf("mode %q reader %v: New = %v; want ErrAuditAcknowledgedWithRecording", mode, reader, err)
			}
		}
	}
}

func TestDevelopmentWarnsOnAuditWiringProductionRefuses(t *testing.T) {
	logger, logs := postureLogger()
	comps, err := New(postureRepos(false, false), WithRuntimeMode(environment.ModeDevelopment), WithLogger(logger))
	if err != nil {
		t.Fatalf("development refused: %v", err)
	}
	got := logs.String()
	if n := strings.Count(got, "level=WARN"); n != 1 || strings.Contains(got, "posture_waiver") {
		t.Fatalf("want exactly one unacknowledged warning, got %d: %s", n, got)
	}
	for _, want := range []string{"would be refused in production", "runtime_mode=development", falseRecordingText, nilReaderText} {
		if !strings.Contains(got, want) {
			t.Errorf("development warning lacks %q: %s", want, got)
		}
	}
	if comps.Decisions == nil {
		t.Fatal("development construction returned no services")
	}
}

func TestZeroModeIsModeBlind(t *testing.T) {
	logger, logs := postureLogger()
	comps, err := New(postureRepos(false, false), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatalf("zero mode logged a posture line: %s", logs.String())
	}
	line := registrationLine(t, comps, logs)
	for _, want := range []string{"audit_recording=false", "audit_reader=false", `runtime_mode=""`} {
		if !strings.Contains(line, want) {
			t.Errorf("registration line lacks %s: %s", want, line)
		}
	}
	comps, err = New(postureRepos(true, true), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	line = registrationLine(t, comps, logs)
	if !strings.Contains(line, "audit_recording=true") || !strings.Contains(line, "audit_reader=true") {
		t.Errorf("registration line misreports a recording authority: %s", line)
	}
}

func TestRuntimeModeRejectsInvalidSpelling(t *testing.T) {
	for _, mode := range []environment.Mode{"Production", "prod", "staging"} {
		_, err := New(postureRepos(true, true), WithRuntimeMode(mode))
		if !errors.Is(err, environment.ErrModeInvalid) {
			t.Errorf("WithRuntimeMode(%q): New = %v; want environment.ErrModeInvalid", mode, err)
		}
	}
}

func TestPostureSettingsAreProgrammatic(t *testing.T) {
	t.Setenv("RUNTIME_MODE", "production")
	t.Setenv("AUDIT_DISABLED_ACKNOWLEDGED", "true")
	t.Setenv("RuntimeMode", "production")
	t.Setenv("AuditDisabledAcknowledged", "true")
	var cfg config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.RuntimeMode != "" || cfg.AuditDisabledAcknowledged {
		t.Fatalf("posture settings read from the environment: %+v", cfg)
	}
}

const (
	integrityWarnText      = "the integrity policy does not protect owner"
	integrityNilWarnText   = "Repositories.Mutations is nil beside an owner relation"
	auditReadWarnText      = "audit reads are not recorded; wire WithAuditReadObserver"
	integrityPolicyFixHint = "mutations.DefaultIntegrityPolicy()"
)

func integrityModel() decisions.Model {
	owner := decisions.RelationDef{AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}
	return decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "project", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"owner": owner}, Permissions: map[string]decisions.Expression{"manage": decisions.AnyOf(decisions.Direct("owner"))}}},
		{Name: "space", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"owner": owner}, Permissions: map[string]decisions.Expression{"manage": decisions.AnyOf(decisions.Direct("owner"))}}},
		{Name: "team", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"member": owner}, Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("member"))}}},
	})
}

func ownerlessModel() decisions.Model {
	member := decisions.RelationDef{AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}
	return decisions.NewSchema([]decisions.ResourceSchema{{Name: "team", Def: decisions.ResourceTypeDef{
		Relations: map[string]decisions.RelationDef{"member": member}, Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("member"))},
	}}})
}

func integrityRepos(policy *mutations.IntegrityPolicy) Repositories {
	opts := []memory.Option{memory.WithAudit()}
	if policy != nil {
		opts = append(opts, memory.WithIntegrityPolicy(*policy))
	}
	store := memory.New(opts...)
	return Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), OperationExpiry: store.OperationExpiry(), Audit: store.Audit()}
}

func TestIntegrityWarnsWhenOwnerIsUnprotected(t *testing.T) {
	projectOnly := mutations.IntegrityPolicy{Rules: []mutations.IntegrityRule{{ResourceType: "project", Relation: "owner"}}}
	adminOnly := mutations.IntegrityPolicy{Rules: []mutations.IntegrityRule{{Relation: "admin"}}}
	full := mutations.DefaultIntegrityPolicy()
	for _, tc := range []struct {
		name   string
		policy *mutations.IntegrityPolicy
		types  string
		rules  int
	}{
		{"empty policy", nil, "project,space", 0},
		{"one type protected", &projectOnly, "space", 1},
		{"another relation protected", &adminOnly, "project,space", 1},
		{"default policy", &full, "", 1},
	} {
		for _, mode := range []environment.Mode{"", environment.ModeDevelopment, environment.ModeProduction} {
			t.Run(tc.name+"/"+mode.String(), func(t *testing.T) {
				logger, logs := postureLogger()
				comps, err := New(integrityRepos(tc.policy), WithModel(integrityModel()), WithRuntimeMode(mode), WithLogger(logger), WithDecisionObserver(ignoreDecisions))
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if mode != "" && tc.types != "" {
					want = 1
				}
				out := logs.String()
				if n := strings.Count(out, integrityWarnText); n != want || strings.Count(out, "level=WARN") != want {
					t.Fatalf("integrity warnings=%d want %d: %s", n, want, out)
				}
				if want == 1 && (!strings.Contains(out, "resource_types="+tc.types) || !strings.Contains(out, integrityPolicyFixHint)) {
					t.Fatalf("warning does not name %s and the fix: %s", tc.types, out)
				}
				if line := registrationLine(t, comps, logs); !strings.Contains(line, fmt.Sprintf("integrity_rules=%d", tc.rules)) {
					t.Fatalf("registration line lacks integrity_rules=%d: %s", tc.rules, line)
				}
			})
		}
	}
}

func TestIntegrityWarnsWhenMutationsIsNilBesideOwner(t *testing.T) {
	for _, mode := range []environment.Mode{"", environment.ModeDevelopment, environment.ModeProduction} {
		t.Run(mode.String(), func(t *testing.T) {
			repos := integrityRepos(nil)
			repos.Mutations, repos.OperationExpiry = nil, nil
			logger, logs := postureLogger()
			if _, err := New(repos, WithModel(integrityModel()), WithRuntimeMode(mode), WithLogger(logger), WithDecisionObserver(ignoreDecisions)); err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode != "" {
				want = 1
			}
			if n := strings.Count(logs.String(), integrityNilWarnText); n != want || strings.Count(logs.String(), integrityWarnText) != 0 {
				t.Fatalf("nil-mutations warnings=%d want %d: %s", n, want, logs.String())
			}
		})
	}
	// No owner relation, no warning.
	logger, logs := postureLogger()
	repos := integrityRepos(nil)
	if _, err := New(repos, WithModel(ownerlessModel()), WithRuntimeMode(environment.ModeDevelopment), WithLogger(logger)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "protect owner") {
		t.Fatalf("model without owner warned: %s", logs.String())
	}
}

func TestAuditGateWiring(t *testing.T) {
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(sdk.WithPrincipal(r.Context(), sdk.Principal{Type: "user", ID: "admin-1"})))
		})
	}
	repos := integrityRepos(nil)
	repos.Audit = nil
	if _, err := New(repos, WithRoleRoutes(authorizationhttp.RoleRoutes{AuditGate: gate})); !errors.Is(err, authorizationhttp.ErrAuditGateWithoutReader) {
		t.Fatalf("gate without Repositories.Audit: %v", err)
	}

	logger, logs := postureLogger()
	comps, err := New(integrityRepos(nil), WithRoleRoutes(authorizationhttp.RoleRoutes{AuditGate: gate}), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(logs.String(), auditReadWarnText); n != 1 {
		t.Fatalf("observer warnings=%d: %s", n, logs.String())
	}
	logs.Reset()
	if err := comps.Register(pockets.Mount{Router: web.NewWebHandler()}); err != nil {
		t.Fatal(err)
	}
	if line := logs.String(); !strings.Contains(line, "audit_read_route=true") {
		t.Fatalf("registration line lacks audit_read_route=true: %s", line)
	}

	var events []authorizationhttp.AuditReadEvent
	logs.Reset()
	comps, err = New(integrityRepos(nil), WithRoleRoutes(authorizationhttp.RoleRoutes{AuditGate: gate}), WithLogger(logger),
		WithAuditReadObserver(func(_ context.Context, ev authorizationhttp.AuditReadEvent) { events = append(events, ev) }))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), auditReadWarnText) {
		t.Fatalf("observed gate warned: %s", logs.String())
	}
	rec := httptest.NewRecorder()
	comps.HTTP.Audit().ServeHTTP(rec, httptest.NewRequest("GET", "/authorization/audit", nil))
	if rec.Code != http.StatusOK || len(events) != 1 || events[0].Principal.ID != "admin-1" {
		t.Fatalf("status=%d events=%+v body=%s", rec.Code, events, rec.Body)
	}

	comps, err = New(integrityRepos(nil), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	if line := registrationLine(t, comps, logs); !strings.Contains(line, "audit_read_route=false") {
		t.Fatalf("registration line lacks audit_read_route=false: %s", line)
	}
}

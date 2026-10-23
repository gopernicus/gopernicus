package authorization

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const (
	absentRecordingText = "does not report AuditRecording() (stores pgx < v0.19.0 / turso < v0.18.0, or a host wrapper hides it — forward the method)"
	falseRecordingText  = "reports AuditRecording() false (construct the store WithAudit())"
	nilReaderText       = "Repositories.Audit is nil"
	waiverHintText      = "WithAuditDisabledAcknowledged()"
	waiverLogText       = "posture_waiver=AuditDisabledAcknowledged"
	observerWarnText    = "denials are not durably recorded; wire WithDecisionObserver"
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
	repos := Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}
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
	for _, want := range []string{"audit_recording=true", "audit_reader=true", "decision_observer=true", "runtime_mode=production"} {
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

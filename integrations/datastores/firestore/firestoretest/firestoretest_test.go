package firestoretest

import (
	"strings"
	"testing"
)

// env builds an environment lookup from a map, so every decision below is
// exercised without touching the process environment (and without a client,
// credentials, or a network).
func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// TestEmulatorHost pins the scheme stripping the emulator's REST endpoints need
// and the "not configured" answer that drives the loud skip.
func TestEmulatorHost(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantOK   bool
	}{
		{in: "", wantOK: false},
		{in: "   ", wantOK: false},
		{in: "127.0.0.1:8080", wantHost: "127.0.0.1:8080", wantOK: true},
		{in: "http://127.0.0.1:8080", wantHost: "127.0.0.1:8080", wantOK: true},
		{in: "https://firestore.local:8080/", wantHost: "firestore.local:8080", wantOK: true},
	}
	for _, tc := range cases {
		host, ok := emulatorHost(tc.in)
		if ok != tc.wantOK || host != tc.wantHost {
			t.Errorf("emulatorHost(%q) = %q, %v; want %q, %v", tc.in, host, ok, tc.wantHost, tc.wantOK)
		}
	}
}

// TestEmulatorProject pins the documented default.
func TestEmulatorProject(t *testing.T) {
	if got := emulatorProject(""); got != DefaultProject {
		t.Errorf("emulatorProject(\"\") = %q, want %q", got, DefaultProject)
	}
	if got := emulatorProject("other-project"); got != "other-project" {
		t.Errorf("emulatorProject = %q, want the override", got)
	}
}

// TestEmulatorDatabase pins Open's database selection — the C8 isolation
// contract's escape hatch. Blank (or whitespace) means the DEFAULT database,
// which firestore.Config spells as an empty DatabaseID; a value moves the whole
// run onto one named database without editing a test.
func TestEmulatorDatabase(t *testing.T) {
	cases := []struct{ in, want string }{
		{in: "", want: ""},
		{in: "   ", want: ""},
		{in: "ci-42", want: "ci-42"},
		{in: "  ci-42\n", want: "ci-42"},
		{in: "(default)", want: "(default)"},
	}
	for _, tc := range cases {
		if got := emulatorDatabase(tc.in); got != tc.want {
			t.Errorf("emulatorDatabase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEmulatorDatabaseReadsTheEnvironment proves the exported reader is wired to
// DatabaseEnv and to nothing else — a store harness that trusted the wrong
// variable would share a database it thought it owned.
func TestEmulatorDatabaseReadsTheEnvironment(t *testing.T) {
	t.Setenv(DatabaseEnv, "")
	if got := EmulatorDatabase(); got != "" {
		t.Errorf("EmulatorDatabase() = %q with %s unset, want the default database", got, DatabaseEnv)
	}
	t.Setenv(DatabaseEnv, "ci-42")
	if got := EmulatorDatabase(); got != "ci-42" {
		t.Errorf("EmulatorDatabase() = %q, want ci-42", got)
	}
}

// TestLiveTarget is the live factory's whole decision table. Each row is a
// reason the live leg must refuse, or the one shape it accepts.
func TestLiveTarget(t *testing.T) {
	cases := []struct {
		name       string
		values     map[string]string
		wantReason string // substring the refusal must name; "" = accepted
		want       liveConfig
	}{
		{
			name:       "nothing configured names both variables",
			values:     nil,
			wantReason: LiveProjectEnv,
		},
		{
			name:       "missing project is named",
			values:     map[string]string{LiveDatabaseEnv: "ci-42"},
			wantReason: LiveProjectEnv,
		},
		{
			name:       "missing database is named",
			values:     map[string]string{LiveProjectEnv: "gopernicus-live"},
			wantReason: LiveDatabaseEnv,
		},
		{
			name: "an emulator endpoint is refused even when live config is complete",
			values: map[string]string{
				EmulatorHostEnv: "127.0.0.1:8080",
				LiveProjectEnv:  "gopernicus-live",
				LiveDatabaseEnv: "ci-42",
			},
			wantReason: EmulatorHostEnv,
		},
		{
			name: "the default database is refused",
			values: map[string]string{
				LiveProjectEnv:  "gopernicus-live",
				LiveDatabaseEnv: "(default)",
			},
			wantReason: "(default)",
		},
		{
			name: "a run-owned named database is accepted",
			values: map[string]string{
				LiveProjectEnv:  "gopernicus-live",
				LiveDatabaseEnv: "ci-42",
			},
			want: liveConfig{project: "gopernicus-live", database: "ci-42"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := liveTarget(env(tc.values))

			if tc.wantReason == "" {
				if reason != "" {
					t.Fatalf("liveTarget refused a valid configuration: %s", reason)
				}
				if got != tc.want {
					t.Errorf("liveTarget = %+v, want %+v", got, tc.want)
				}
				return
			}
			if reason == "" {
				t.Fatalf("liveTarget accepted %v, want a refusal naming %q", tc.values, tc.wantReason)
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Errorf("refusal = %q, want it to name %q", reason, tc.wantReason)
			}
			if got != (liveConfig{}) {
				t.Errorf("liveTarget returned a target %+v alongside a refusal", got)
			}
		})
	}
}

// TestLiveRequired pins the release gate's switch: only "1" converts a skip into
// a failure, so an unset or accidentally-empty variable cannot fail every run.
func TestLiveRequired(t *testing.T) {
	cases := map[string]bool{"": false, "0": false, "true": false, "1": true, " 1 ": true}
	for value, want := range cases {
		if got := liveRequired(env(map[string]string{LiveRequiredEnv: value})); got != want {
			t.Errorf("liveRequired(%q) = %v, want %v", value, got, want)
		}
	}
}

// TestResetGuard proves the emulator reset refuses anything that is not the
// emulator — the single check standing between a test suite and a real
// database's contents.
func TestResetGuard(t *testing.T) {
	if reason := resetGuard(true, "projects/gopernicus-test/databases/(default)"); reason != "" {
		t.Errorf("resetGuard refused the emulator: %s", reason)
	}
	reason := resetGuard(false, "projects/gopernicus-live/databases/ci-42")
	if reason == "" {
		t.Fatal("resetGuard allowed a NON-emulator database to be cleared wholesale")
	}
	if !strings.Contains(reason, "projects/gopernicus-live/databases/ci-42") {
		t.Errorf("refusal = %q, want it to name the target", reason)
	}
}

// TestResetLiveGuard covers the live reset's three refusals and its accepted
// shape.
func TestResetLiveGuard(t *testing.T) {
	const live = "projects/gopernicus-live/databases/ci-42"

	cases := []struct {
		name        string
		emulated    bool
		target      string
		collections []string
		wantReason  string
	}{
		{name: "accepted", target: live, collections: []string{"users"}},
		{name: "emulator client", emulated: true, target: "projects/gopernicus-test/databases/(default)", collections: []string{"users"}, wantReason: "emulator"},
		{name: "default database", target: "projects/gopernicus-live/databases/(default)", collections: []string{"users"}, wantReason: "(default)"},
		{name: "no collections", target: live, wantReason: "no collections"},
		{name: "empty collection name", target: live, collections: []string{"users", " "}, wantReason: "empty collection"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := resetLiveGuard(tc.emulated, tc.target, tc.collections)
			if tc.wantReason == "" {
				if reason != "" {
					t.Fatalf("resetLiveGuard refused a valid target: %s", reason)
				}
				return
			}
			if reason == "" {
				t.Fatalf("resetLiveGuard allowed %+v", tc)
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Errorf("refusal = %q, want it to name %q", reason, tc.wantReason)
			}
		})
	}
}

// TestSkipMessageIsLoud keeps the skip message in the shape the rest of the
// repository uses: it names the variable and says what was NOT verified.
func TestSkipMessageIsLoud(t *testing.T) {
	if !strings.Contains(skipNoEmulator, EmulatorHostEnv) || !strings.Contains(skipNoEmulator, "NOT verified") {
		t.Errorf("skip message = %q, want it to name %s and say what was NOT verified", skipNoEmulator, EmulatorHostEnv)
	}
}

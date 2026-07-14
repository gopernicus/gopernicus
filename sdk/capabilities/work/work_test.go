package work

import "testing"

// The Status strings are the persisted wire vocabulary: features/jobs writes
// them to a status column and the auth bridge folds them verbatim. Their literal
// values are locked here so this sdk package self-guards its wire vocabulary
// independent of any implementation — the identity_test.go:TestConstantValues
// precedent.
func TestConstantValues(t *testing.T) {
	cases := []struct {
		got  Status
		want string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{StatusDeadLetter, "dead_letter"},
		{StatusCanceled, "canceled"},
		{StatusSuperseded, "superseded"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("Status = %q, want %q", c.got, c.want)
		}
	}
}

// Terminal is true for the four end states and false for the two in-flight
// states, an unknown string, and StatusFailed (retryable, NON-terminal).
func TestTerminal(t *testing.T) {
	cases := []struct {
		status Status
		want   bool
	}{
		{StatusPending, false},
		{StatusRunning, false},
		{StatusCompleted, true},
		{StatusFailed, false},
		{StatusDeadLetter, true},
		{StatusCanceled, true},
		{StatusSuperseded, true},
		{Status("unknown"), false},
	}
	for _, c := range cases {
		if got := c.status.Terminal(); got != c.want {
			t.Errorf("Status(%q).Terminal() = %v, want %v", c.status, got, c.want)
		}
	}
}

// Known is true for exactly the canonical seven and false for anything else.
func TestKnown(t *testing.T) {
	cases := []struct {
		status Status
		want   bool
	}{
		{StatusPending, true},
		{StatusRunning, true},
		{StatusCompleted, true},
		{StatusFailed, true},
		{StatusDeadLetter, true},
		{StatusCanceled, true},
		{StatusSuperseded, true},
		{Status("unknown"), false},
	}
	for _, c := range cases {
		if got := c.status.Known(); got != c.want {
			t.Errorf("Status(%q).Known() = %v, want %v", c.status, got, c.want)
		}
	}
}

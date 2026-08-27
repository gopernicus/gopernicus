package commands

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no args", nil, 1},
		{"unknown command", []string{"bogus"}, 1},
		{"help", []string{"help"}, 0},
		{"version", []string{"version"}, 0},
		{"init stub", []string{"init"}, 1},
		{"new requires subcommand", []string{"new"}, 1},
		{"new unknown subcommand", []string{"new", "bogus"}, 1},
		{"new pocket stub", []string{"new", "pocket"}, 1},
		{"db requires subcommand", []string{"db"}, 1},
		{"db unknown subcommand", []string{"db", "bogus"}, 1},
		{"db migrate no runner errors", []string{"db", "migrate"}, 1},
		{"db status no runner falls back", []string{"db", "status"}, 0},
		{"db create needs slug", []string{"db", "create"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := captureExit(t, c.args); got != c.want {
				t.Fatalf("Run(%v) = %d, want %d", c.args, got, c.want)
			}
		})
	}
}

// TestRetiredScaffoldVerbRejected proves the pre-rename scaffold verb is GONE
// rather than merely undocumented: `gopernicus new <retired>` exits non-zero and
// prints the `new` usage text naming the pocket verb.
func TestRetiredScaffoldVerbRejected(t *testing.T) {
	const retired = "feature"

	out, code := captureStderr(t, func() int { return Run([]string{"new", retired}) })
	if code == 0 {
		t.Fatalf("Run([new %s]) = 0, want non-zero", retired)
	}
	for _, want := range []string{
		`unknown subcommand "` + retired + `"`,
		"gopernicus new — scaffold a new component",
		"pocket    Scaffold a pocket module tree",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr missing %q:\n%s", want, out)
		}
	}
}

func TestVersionOutput(t *testing.T) {
	out := captureStdout(t, func() int { return Run([]string{"version"}) })
	if !strings.Contains(out, version) {
		t.Errorf("version output missing version %q: %q", version, out)
	}
	if !strings.Contains(out, modulePath) {
		t.Errorf("version output missing module path %q: %q", modulePath, out)
	}
}

func captureExit(t *testing.T, args []string) int {
	t.Helper()
	var code int
	captureStdout(t, func() int { code = Run(args); return code })
	return code
}

// captureStdout redirects os.Stdout for the duration of fn and returns what it
// wrote. os.Stderr is silenced so stub/usage noise stays out of test output.
func captureStdout(t *testing.T, fn func() int) string {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	os.Stdout, os.Stderr = wOut, devNull
	defer func() { os.Stdout, os.Stderr = origOut, origErr; devNull.Close() }()

	fn()
	wOut.Close()
	b, _ := io.ReadAll(rOut)
	return string(b)
}

// captureStderr redirects os.Stderr for the duration of fn and returns what it
// wrote plus fn's exit code. os.Stdout is silenced so scaffold noise stays out
// of test output.
func captureStderr(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	os.Stdout, os.Stderr = devNull, wErr
	defer func() { os.Stdout, os.Stderr = origOut, origErr; devNull.Close() }()

	code := fn()
	wErr.Close()
	b, _ := io.ReadAll(rErr)
	return string(b), code
}

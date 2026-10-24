package commands

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fixtureModule is the module path of every generated guard fixture host.
const fixtureModule = "example.com/acme"

// contractRow matches one `| **Hn** | sentence |` row of examples/README.md §3,
// and postureRow one `| **Pn** | sentence |` row of §3b. The import-law table's
// rows carry words after the bold id and never match.
var (
	contractRow = regexp.MustCompile(`^\| \*\*(H[0-9]+)\*\* \| (.*) \|$`)
	postureRow  = regexp.MustCompile(`^\| \*\*(P[0-9]+)\*\* \| (.*) \|$`)
)

// guardFixture is one generated host: files maps a host-relative path to its
// contents, written beside a go.mod and a cmd/server composition root. notes,
// when non-nil, are the informational line labels the rule must print, in any
// order.
type guardFixture struct {
	name      string
	rule      string
	module    string
	files     map[string]string
	violating bool
	notes     []string
}

// TestGuardListMatchesContract is what makes "the binary and this page cannot
// drift" true: every canonical sentence the CLI embeds is byte-equal to the
// examples/README.md §3 (H) or §3b (P) row with the same id, and the ids agree
// in order within each series. `--list` prints H; `--list --posture` H then P.
func TestGuardListMatchesContract(t *testing.T) {
	f, err := os.Open(filepath.Join(repoRoot(t), "examples", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var rows [2][][2]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		for i, re := range []*regexp.Regexp{contractRow, postureRow} {
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				rows[i] = append(rows[i], [2]string{m[1], m[2]})
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	var all [][2]string
	for i, series := range [][]guardRule{guardRules, postureRules} {
		if len(rows[i]) != len(series) {
			t.Fatalf("examples/README.md has %d canonical %s rows, the CLI embeds %d rules", len(rows[i]), series[0].id[:1], len(series))
		}
		for j, rule := range series {
			id, sentence := rows[i][j][0], rows[i][j][1]
			if id != rule.id {
				t.Errorf("row %d: README id %s, CLI id %s", j, id, rule.id)
			}
			if sentence != rule.sentence {
				t.Errorf("%s drifted:\nREADME: %s\nCLI:    %s", rule.id, sentence, rule.sentence)
			}
		}
		all = append(all, rows[i]...)
	}

	for _, c := range []struct {
		args []string
		want [][2]string
	}{
		{[]string{"guard", "--list"}, rows[0]},
		{[]string{"guard", "--list", "--posture"}, all},
	} {
		out := captureStdout(t, func() int { return Run(c.args) })
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(lines) != len(c.want) {
			t.Fatalf("%v printed %d lines, want %d", c.args, len(lines), len(c.want))
		}
		for i, line := range lines {
			id, sentence := c.want[i][0], c.want[i][1]
			if want := id + strings.Repeat(" ", 4-len(id)) + sentence; line != want {
				t.Errorf("%v line %d:\n got %q\nwant %q", c.args, i, line, want)
			}
		}
	}
}

// TestGuardRuleFixtures runs every rule over generated hosts: each H id has at
// least one clean and one violating fixture. Framework paths are assembled from
// baseModule and the hygiene tokens from fragments, so neither G9/G10 nor G11
// ever sees them verbatim in this file.
func TestGuardRuleFixtures(t *testing.T) {
	var (
		host     = fixtureModule
		pgxStore = fwPockets + "/cms/stores/pgx"
		pgx      = "github.com/jackc/pgx/v5"
		templ    = "github.com/a-h/templ"
	)

	cases := []guardFixture{
		{name: "app-local domain trio", rule: "H0", files: map[string]string{
			"internal/logic/domains/x/x.go": goSrc("x", nil, ""),
		}},
		{name: "internal/foo is out of shape", rule: "H0", violating: true, files: map[string]string{
			"internal/foo/foo.go": goSrc("foo", nil, ""),
		}},
		{name: "pockets/<n>/views is out of shape", rule: "H0", violating: true, files: map[string]string{
			"pockets/auth/views/views.go": goSrc("views", []string{fwPockets + "/authentication"}, ""),
		}},

		{name: "logic on sdk and the host's sdk/", rule: "H1", files: map[string]string{
			"internal/logic/domains/x/x.go": goSrc("x", []string{"context", fwSDK + "/pkg/list", host + "/sdk/money"}, ""),
			"sdk/money/money.go":            goSrc("money", nil, ""),
		}},
		{name: "logic imports outbound", rule: "H1", violating: true, files: map[string]string{
			"internal/logic/domains/x/x.go":    goSrc("x", []string{host + "/internal/outbound/domains/x"}, ""),
			"internal/outbound/domains/x/x.go": goSrc("x", nil, ""),
		}},

		{name: "domain imports its own subpackage", rule: "H2", files: map[string]string{
			"internal/logic/domains/x/x.go":       goSrc("x", []string{host + "/internal/logic/domains/x/rules"}, ""),
			"internal/logic/domains/x/rules/r.go": goSrc("rules", nil, ""),
		}},
		{name: "domain imports another domain", rule: "H2", violating: true, files: map[string]string{
			"internal/logic/domains/x/x.go": goSrc("x", []string{host + "/internal/logic/domains/y"}, ""),
			"internal/logic/domains/y/y.go": goSrc("y", nil, ""),
		}},
		{name: "domain imports a composition", rule: "H2", violating: true, files: map[string]string{
			"internal/logic/domains/x/x.go":      goSrc("x", []string{host + "/internal/logic/compositions/c"}, ""),
			"internal/logic/compositions/c/c.go": goSrc("c", nil, ""),
		}},

		{name: "composition over narrow services", rule: "H3", files: map[string]string{
			"internal/logic/domains/x/x.go":      goSrc("x", nil, ""),
			"internal/logic/compositions/c/c.go": goSrc("c", []string{host + "/internal/logic/domains/x"}, compositionBody("")),
		}},
		{name: "composition holds a repository", rule: "H3", violating: true, files: map[string]string{
			"internal/logic/domains/x/x.go": goSrc("x", nil, ""),
			"internal/logic/compositions/c/c.go": goSrc("c", []string{host + "/internal/logic/domains/x"},
				compositionBody("type Engine struct{ repo map[string]x.ItemRepository }")),
		}},
		{name: "composition takes a transactor", rule: "H3", violating: true, files: map[string]string{
			"internal/logic/domains/x/x.go": goSrc("x", nil, ""),
			"internal/logic/compositions/c/c.go": goSrc("c", []string{host + "/internal/logic/domains/x", fwSDK + "/pkg/list"},
				compositionBody("func Run(tx list.Transactor) {}")),
		}},
		{name: "composition aliases a domain service", rule: "H3", violating: true, files: map[string]string{
			"internal/logic/domains/x/x.go": goSrc("x", nil, ""),
			"internal/logic/compositions/c/c.go": goSrc("c", []string{host + "/internal/logic/domains/x"},
				"type ItemService = x.Service\n\ntype Dependencies struct{ Items ItemService }"),
		}},

		{name: "inbound reaches logic and the root pockets contract", rule: "H4", files: map[string]string{
			"internal/logic/domains/x/x.go":   goSrc("x", nil, ""),
			"internal/inbound/domains/x/r.go": goSrc("x", []string{host + "/internal/logic/domains/x", fwPockets, fwSDK + "/pkg/web"}, ""),
		}},
		{name: "inbound imports outbound", rule: "H4", violating: true, files: map[string]string{
			"internal/inbound/domains/x/r.go":  goSrc("x", []string{host + "/internal/outbound/domains/x"}, ""),
			"internal/outbound/domains/x/x.go": goSrc("x", nil, ""),
		}},
		{name: "inbound imports the host's integrations/", rule: "H4", violating: true, files: map[string]string{
			"internal/inbound/domains/x/r.go": goSrc("x", []string{host + "/integrations/mail/smtp"}, ""),
			"integrations/mail/smtp/smtp.go":  goSrc("smtp", nil, ""),
		}},

		{name: "outbound on its own domain, a store, and a driver", rule: "H5", files: map[string]string{
			"internal/logic/domains/x/x.go":    goSrc("x", nil, ""),
			"internal/outbound/domains/x/x.go": goSrc("x", []string{host + "/internal/logic/domains/x", pgxStore, pgx}, ""),
		}},
		{name: "outbound imports another domain", rule: "H5", violating: true, files: map[string]string{
			"internal/logic/domains/y/y.go":    goSrc("y", nil, ""),
			"internal/outbound/domains/x/x.go": goSrc("x", []string{host + "/internal/logic/domains/y"}, ""),
		}},
		{name: "outbound imports the host's integrations/", rule: "H5", violating: true, files: map[string]string{
			"internal/outbound/domains/x/x.go": goSrc("x", []string{host + "/integrations/mail/smtp"}, ""),
			"integrations/mail/smtp/smtp.go":   goSrc("smtp", nil, ""),
		}},

		{name: "host pocket extends a framework pocket", rule: "H6", files: map[string]string{
			"pockets/cms/inbound/theme/theme.go": goSrc("theme", []string{fwPockets + "/cms/views/goth"}, ""),
			"pockets/cms/logic/rules/rules.go":   goSrc("rules", []string{fwPockets + "/cms", fwSDK}, ""),
		}},
		{name: "host pocket imports internal/logic", rule: "H6", violating: true, files: map[string]string{
			"internal/logic/domains/x/x.go":      goSrc("x", nil, ""),
			"pockets/cms/outbound/mem/mem.go":    goSrc("mem", []string{fwPockets + "/cms", host + "/internal/logic/domains/x"}, ""),
			"pockets/cms/inbound/theme/theme.go": goSrc("theme", nil, ""),
		}},
		{name: "host pocket with no framework rim", rule: "H6", violating: true, files: map[string]string{
			"pockets/cms/outbound/mem/mem.go": goSrc("mem", []string{fwSDK}, ""),
		}},

		{name: "outbound names a pocket store", rule: "H7", files: map[string]string{
			"internal/logic/domains/x/x.go":    goSrc("x", nil, ""),
			"internal/outbound/domains/x/x.go": goSrc("x", []string{pgxStore}, ""),
			"internal/inbound/domains/x/r.go":  goSrc("x", nil, ""),
		}},
		{name: "inbound names a pocket store", rule: "H7", violating: true, files: map[string]string{
			"internal/inbound/domains/x/r.go": goSrc("x", []string{pgxStore}, ""),
		}},

		{name: "inbound domain with its logic twin", rule: "H8", files: map[string]string{
			"internal/logic/domains/x/x.go":   goSrc("x", nil, ""),
			"internal/inbound/domains/x/r.go": goSrc("x", nil, ""),
		}},
		{name: "inbound domain with no logic twin", rule: "H8", violating: true, files: map[string]string{
			"internal/inbound/domains/x/r.go": goSrc("x", nil, ""),
		}},

		{name: "raw pool reached inside the host's integrations/", rule: "H9", files: map[string]string{
			"integrations/db/pgx/pgx.go": goSrc("pgx", nil, "func f(p interface{ Underlying() int }) int { return p"+underlyingCall+" }"),
		}},
		{name: "raw pool reached in outbound", rule: "H9", violating: true, files: map[string]string{
			"internal/outbound/domains/x/x.go": goSrc("x", nil, "func f(p interface{ Underlying() int }) int { return p"+underlyingCall+" }"),
		}},
		{name: "lax scanning in workshop", rule: "H9", violating: true, files: map[string]string{
			"workshop/seed/seed.go": goSrc("seed", nil, "var _ = pgx."+laxScanSymbol),
		}},

		{name: "client binary declares interfaces", rule: "H10", files: map[string]string{
			"cmd/cli/main.go":        goSrc("main", []string{fwSDK + "/pkg/web", host + "/cmd/cli/client"}, "type Reader interface{ Read() }\n\nfunc main() {}"),
			"cmd/cli/client/c.go":    goSrc("client", []string{"net/http"}, "type Doer interface{ Do() }"),
			"cmd/server/sql_test.go": goSrc("main", nil, "const reset = \"DELETE FROM items\""),
		}},
		{name: "composition root declares an interface", rule: "H10", violating: true, files: map[string]string{
			"cmd/server/wire.go": goSrc("main", []string{host + "/internal/logic/domains/x"}, "type marker interface{ Mark() }"),
		}},
		{name: "client binary carries an SQL literal", rule: "H10", violating: true, files: map[string]string{
			"cmd/cli/main.go": goSrc("main", nil, "const q = \"SELECT 1\"\n\nfunc main() {}"),
		}},

		{name: "integration named only by cmd", rule: "H11", files: map[string]string{
			"integrations/mail/smtp/smtp.go": goSrc("smtp", []string{fwSDK + "/capabilities/notify/email", host + "/sdk/addr", "github.com/vendor/smtp"}, ""),
			"sdk/addr/addr.go":               goSrc("addr", nil, ""),
			"cmd/server/mail.go":             goSrc("main", []string{host + "/integrations/mail/smtp"}, ""),
		}},
		{name: "integration imports internal/logic", rule: "H11", violating: true, files: map[string]string{
			"integrations/mail/smtp/smtp.go": goSrc("smtp", []string{host + "/internal/logic/domains/x"}, ""),
			"internal/logic/domains/x/x.go":  goSrc("x", nil, ""),
		}},
		{name: "outbound test imports the integration", rule: "H11", violating: true, files: map[string]string{
			"integrations/mail/smtp/smtp.go":        goSrc("smtp", nil, ""),
			"internal/outbound/domains/x/x_test.go": goSrc("x", []string{host + "/integrations/mail/smtp"}, ""),
		}},

		{name: "drivers in outbound, templ in inbound views", rule: "H12", files: map[string]string{
			"internal/outbound/domains/x/x.go": goSrc("x", []string{pgx}, ""),
			"internal/inbound/views/shell.go":  goSrc("views", []string{templ}, ""),
		}},
		{name: "golang.org/x in outbound", rule: "H12", violating: true, files: map[string]string{
			"internal/outbound/domains/x/x.go": goSrc("x", []string{"golang.org/x/crypto/bcrypt"}, ""),
		}},
		{name: "templ in outbound", rule: "H12", violating: true, files: map[string]string{
			"internal/outbound/domains/x/x.go": goSrc("x", []string{templ}, ""),
		}},
		{name: "pgx in inbound", rule: "H12", violating: true, files: map[string]string{
			"internal/inbound/domains/x/r.go": goSrc("x", []string{pgx}, ""),
		}},
		{name: "inbound imports the host's workshop/", rule: "H12", violating: true, files: map[string]string{
			"internal/inbound/domains/x/r.go": goSrc("x", []string{host + "/workshop/testweb"}, ""),
			"workshop/testweb/w.go":           goSrc("testweb", nil, ""),
		}},
		{name: "an example importing another example", rule: "H12", violating: true,
			module: baseModule + "/examples/acme", files: map[string]string{
				"internal/inbound/domains/x/r.go": goSrc("x", []string{baseModule + "/examples/other/internal/logic/domains/y"}, ""),
			}},

		{name: "host sdk/ on framework sdk", rule: "H13", files: map[string]string{
			"sdk/money/money.go": goSrc("money", []string{fwSDK, host + "/sdk/units"}, ""),
			"sdk/units/units.go": goSrc("units", nil, ""),
		}},
		{name: "host sdk/ imports internal/logic", rule: "H13", violating: true, files: map[string]string{
			"sdk/money/money.go":            goSrc("money", []string{host + "/internal/logic/domains/x"}, ""),
			"internal/logic/domains/x/x.go": goSrc("x", nil, ""),
		}},

		{name: "host ui/ on ui/goth and templ", rule: "H14", files: map[string]string{
			"ui/showcase/showcase.go": goSrc("showcase", []string{fwUI + "/goth", templ, fwSDK + "/pkg/web"}, ""),
		}},
		{name: "host ui/ imports internal/inbound", rule: "H14", violating: true, files: map[string]string{
			"ui/showcase/showcase.go":         goSrc("showcase", []string{host + "/internal/inbound/views"}, ""),
			"internal/inbound/views/shell.go": goSrc("views", nil, ""),
		}},
	}
	cases = append(cases, postureFixtures()...)

	clean, violating := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		t.Run(c.rule+"/"+c.name, func(t *testing.T) {
			res := evalFixture(t, c)
			for _, f := range res.findings {
				t.Logf("%s %s: %s", c.rule, f.where, f.msg)
			}
			switch {
			case c.violating && len(res.findings) == 0:
				t.Fatalf("%s reported no finding", c.rule)
			case !c.violating && len(res.findings) > 0:
				t.Fatalf("%s reported findings on a clean fixture: %+v", c.rule, res.findings)
			case !c.violating && res.checked == 0:
				t.Fatalf("%s checked nothing on a clean fixture: the case is vacuous", c.rule)
			}
			if c.notes != nil {
				var got []string
				for _, n := range res.notes {
					got = append(got, n.label)
				}
				if !sameLabels(got, c.notes) {
					t.Fatalf("%s notes = %q, want %q (%+v)", c.rule, got, c.notes, res.notes)
				}
			}
		})
		if c.violating {
			violating[c.rule] = true
		} else {
			clean[c.rule] = true
		}
	}
	for _, rule := range append(append([]guardRule{}, guardRules...), postureRules...) {
		if !clean[rule.id] || !violating[rule.id] {
			t.Errorf("%s lacks a clean (%v) or violating (%v) fixture", rule.id, clean[rule.id], violating[rule.id])
		}
	}
}

func TestGuardExitCodes(t *testing.T) {
	cleanHost := writeFixture(t, guardFixture{})
	dirtyHost := writeFixture(t, guardFixture{files: map[string]string{
		"internal/foo/foo.go": goSrc("foo", nil, ""),
	}})
	postureClean := writeFixture(t, guardFixture{files: map[string]string{"cmd/server/main.go": postureRoot()}})
	postureDirty := writeFixture(t, guardFixture{files: map[string]string{
		"cmd/server/main.go": postureRoot("web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders(), ", ""),
	}})
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no go.mod", []string{"guard", t.TempDir()}, 2},
		{"missing root", []string{"guard", filepath.Join(t.TempDir(), "absent")}, 2},
		{"clean", []string{"guard", cleanHost}, 0},
		{"one finding", []string{"guard", dirtyHost}, 1},
		{"list", []string{"guard", "--list"}, 0},
		{"unknown flag", []string{"guard", "--skip", "H0"}, 2},
		{"two roots", []string{"guard", cleanHost, dirtyHost}, 2},
		{"posture clean", []string{"guard", "--posture", postureClean}, 0},
		{"posture finding", []string{"guard", "--posture", postureDirty}, 1},
		{"posture finding is not an H finding", []string{"guard", postureDirty}, 0},
		{"posture with nothing in scope", []string{"guard", "--posture", cleanHost}, 0},
		{"posture H finding", []string{"guard", "--posture", dirtyHost}, 1},
		{"posture list", []string{"guard", "--list", "--posture"}, 0},
		{"posture after the root", []string{"guard", cleanHost, "--posture"}, 2},
		{"posture no go.mod", []string{"guard", "--posture", t.TempDir()}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := captureExit(t, c.args); got != c.want {
				t.Fatalf("Run(%v) = %d, want %d", c.args, got, c.want)
			}
		})
	}

	unparsable := writeFixture(t, guardFixture{files: map[string]string{"internal/logic/domains/x/x.go": "package x\n\nfunc {"}})
	if got := captureExit(t, []string{"guard", unparsable}); got != 2 {
		t.Fatalf("unparsable file: exit %d, want 2", got)
	}
}

// TestGuardNothingToCheckIsReported proves an inert rule never reads as a clean
// one: a cmd/-only host prints `no scope` for every rule with nothing in scope,
// still passes H0's positive check, and exits clean.
func TestGuardNothingToCheckIsReported(t *testing.T) {
	root := writeFixture(t, guardFixture{})
	var code int
	out := captureStdout(t, func() int { code = Run([]string{"guard", root}); return code })
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < len(guardRules) {
		t.Fatalf("expected a status line per rule:\n%s", out)
	}
	scoped := map[string]bool{"H0": true, "H9": true, "H10": true}
	for i, rule := range guardRules {
		status := "no scope"
		if scoped[rule.id] {
			status = "ok"
		}
		prefix := rule.id + strings.Repeat(" ", 5-len(rule.id)) + status
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("status line %d = %q, want prefix %q", i, lines[i], prefix)
		}
	}
	if !strings.Contains(out, "guard: clean (15 rules, 1 package)") {
		t.Errorf("missing clean summary:\n%s", out)
	}

	noBinary := t.TempDir()
	if err := os.WriteFile(filepath.Join(noBinary, "go.mod"), []byte("module "+fixtureModule+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := captureExit(t, []string{"guard", noBinary}); got != 1 {
		t.Fatalf("a host with no cmd/<binary> exits %d, want 1 (H0 is a positive check)", got)
	}
}

// TestGuardPostureExtendsTheRun proves --posture only appends: the H status
// lines are byte-equal to a plain run's, the ten P lines follow, and the
// informational lines appear only under --posture.
func TestGuardPostureExtendsTheRun(t *testing.T) {
	root := writeFixture(t, guardFixture{files: map[string]string{"cmd/server/main.go": postureRoot()}})
	plain := captureStdout(t, func() int { return Run([]string{"guard", root}) })
	posture := captureStdout(t, func() int { return Run([]string{"guard", "--posture", root}) })

	plainLines, postureLines := strings.Split(plain, "\n"), strings.Split(posture, "\n")
	for i := range guardRules {
		if plainLines[i] != postureLines[i] {
			t.Errorf("status line %d: plain %q, posture %q", i, plainLines[i], postureLines[i])
		}
	}
	for i, rule := range postureRules {
		if line := postureLines[len(guardRules)+i]; !strings.HasPrefix(line, rule.id+" ") {
			t.Errorf("posture status line %d = %q, want %s", len(guardRules)+i, line, rule.id)
		}
	}
	if strings.Contains(plain, "ack ") || !strings.HasSuffix(plain, "guard: clean (15 rules, 1 package)\n") {
		t.Errorf("plain run changed:\n%s", plain)
	}
	if !strings.Contains(posture, "\nack  cmd/server/main.go:") || !strings.HasSuffix(posture, "guard: clean (25 rules, 1 package)\n") {
		t.Errorf("posture run lacks its ack line or summary:\n%s", posture)
	}
}

func TestReadModulePath(t *testing.T) {
	cases := []struct {
		gomod string
		want  string
		ok    bool
	}{
		{"module example.com/a\n", "example.com/a", true},
		{"// header\nmodule example.com/a // trailing\r\n\ngo 1.26\n", "example.com/a", true},
		{"module \"example.com/a\"\n", "example.com/a", true},
		{"modules example.com/a\n", "", false},
		{"go 1.26\n", "", false},
		{"module\n", "", false},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(c.gomod), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := readModulePath(dir)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("readModulePath(%q) = %q, %v; want %q, ok=%v", c.gomod, got, err, c.want, c.ok)
		}
	}
}

func TestReadRequires(t *testing.T) {
	gomod := "module example.com/a\n\ngo 1.26\n\nrequire example.com/one v1.0.0 // trailing\n\n" +
		"require (\n\texample.com/two v0.1.0\n\t\"example.com/three\" v0.2.0 // indirect\n\t// example.com/commented v0.0.1\n)\n\n" +
		"replace (\n\texample.com/two => ../two\n)\n\nrequire(\n\texample.com/four v0.3.0\n)\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readRequires(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"example.com/one", "example.com/two", "example.com/three", "example.com/four"}; !slices.Equal(got, want) {
		t.Fatalf("readRequires = %q, want %q", got, want)
	}
}

// evalFixture writes c, loads it, and evaluates c's rule.
func evalFixture(t *testing.T, c guardFixture) guardResult {
	t.Helper()
	host, err := loadHost(writeFixture(t, c))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range append(append([]guardRule{}, guardRules...), postureRules...) {
		if rule.id == c.rule {
			return rule.eval(host)
		}
	}
	t.Fatalf("no rule %s", c.rule)
	return guardResult{}
}

// writeFixture generates a host into a temp dir: go.mod, a cmd/server root,
// and c.files.
func writeFixture(t *testing.T, c guardFixture) string {
	t.Helper()
	module := c.module
	if module == "" {
		module = fixtureModule
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":             "module " + module + "\n\ngo 1.26\n",
		"cmd/server/main.go": goSrc("main", nil, "func main() {}"),
	}
	for rel, src := range c.files {
		files[rel] = src
	}
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// goSrc renders a Go file: a package clause, one blank import per path, body.
func goSrc(pkg string, imports []string, body string) string {
	var b strings.Builder
	b.WriteString("package " + pkg + "\n\n")
	for _, ip := range imports {
		b.WriteString("import _ " + strconv.Quote(ip) + "\n")
	}
	b.WriteString("\n" + body + "\n")
	return b.String()
}

// compositionBody is a conforming composition plus extra declarations.
func compositionBody(extra string) string {
	return "type ItemService interface{ Get(id string) (string, error) }\n\n" +
		"type Dependencies struct{ Items ItemService }\n\n" + extra
}

// postureBase is a posture-clean composition root over every surface P rules
// read; postureRoot edits it. Its import paths are filled from the framework
// family prefixes, so G11 never sees a quoted pocket path in this file.
const postureBase = `package main

import (
	"context"
	"crypto/rand"
	"net/http"
	"os"

	authentication "{authentication}"
	authorization "{authorization}"
	cms "{cms}"
	pgxdb "{pgxdb}"
	tursodb "{turso}"
	environment "{environment}"
	web "{web}"
)

func main() {
	mode, err := environment.LoadEnvForMode("APP_RUNTIME_MODE")
	if err != nil {
		os.Exit(1)
	}
	_ = run(context.Background(), mode)
}

func run(ctx context.Context, mode environment.Mode) error {
	srv := web.ServerConfig{}
	if mode.IsProduction() {
		if err := srv.ValidateProduction(); err != nil {
			return err
		}
	}
	router := web.NewWebHandler()
	router.Use(web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders(), web.RequestID())
	if err := cms.Register(nil, nil, cms.Config{RuntimeMode: mode, AdminUngatedAcknowledged: true}); err != nil {
		return err
	}
	// extra
	return web.Run(ctx, router, srv, nil)
}
`

// The P7 fixture: a clean .env.example over postureBase's APP_RUNTIME_MODE.
const postureEnvExample = `# APP_RUNTIME_MODE=development|production — process environment only
TRUSTED_PROXY_COUNT=0
PUBLIC_BASE_URL=
DB_SSLMODE=verify-full
# DB_SSLMODE=disable  # local docker only — refused in production
DB_URL=                # e.g. postgres://u@h/db?sslmode=disable
`

// The extra-statement marker in postureBase and the mode resolution in its main.
const (
	postureExtra   = "\t// extra\n"
	postureResolve = "\tmode, err := environment.LoadEnvForMode(\"APP_RUNTIME_MODE\")\n\tif err != nil {\n\t\tos.Exit(1)\n\t}\n"
)

// postureRoot renders postureBase with each (old, new) pair replaced once.
func postureRoot(pairs ...string) string {
	src := strings.NewReplacer(
		"{authentication}", fwPockets+"/authentication",
		"{authorization}", fwPockets+"/authorization",
		"{cms}", fwPockets+"/cms",
		"{pgxdb}", fwIntegrations+"/datastores/pgxdb",
		"{turso}", fwIntegrations+"/datastores/turso",
		"{environment}", fwSDK+"/pkg/environment",
		"{web}", fwSDK+"/pkg/web",
	).Replace(postureBase)
	for i := 0; i+1 < len(pairs); i += 2 {
		if !strings.Contains(src, pairs[i]) {
			panic("postureRoot: no " + strconv.Quote(pairs[i]))
		}
		src = strings.Replace(src, pairs[i], pairs[i+1], 1)
	}
	return src
}

// postureEnv renders postureEnvExample with each (old, new) pair replaced once.
func postureEnv(pairs ...string) string {
	src := postureEnvExample
	for i := 0; i+1 < len(pairs); i += 2 {
		if !strings.Contains(src, pairs[i]) {
			panic("postureEnv: no " + strconv.Quote(pairs[i]))
		}
		src = strings.Replace(src, pairs[i], pairs[i+1], 1)
	}
	return src
}

// postureFixtures are the P cases of TestGuardRuleFixtures: each P id has at
// least one clean and one violating host, and P5/P6 pin their informational
// lines.
func postureFixtures() []guardFixture {
	root := func(pairs ...string) map[string]string {
		return map[string]string{"cmd/server/main.go": postureRoot(pairs...)}
	}
	extra := func(stmts string) map[string]string { return root(postureExtra, stmts) }
	env := func(pairs ...string) map[string]string {
		return map[string]string{"cmd/server/main.go": postureRoot(), ".env.example": postureEnv(pairs...)}
	}
	modeField := func(tag string) string {
		return "\tvar cfg struct {\n\t\tMode environment.Mode `" + tag + "`\n\t}\n" +
			"\tif err := environment.ParseEnvTags(\"\", &cfg); err != nil {\n\t\tos.Exit(1)\n\t}\n\tmode := cfg.Mode\n"
	}
	newHandler := "\nfunc newHandler(srv web.ServerConfig) (http.Handler, error) {\n\tif srv.Port == \"\" {\n\t\treturn nil, nil\n\t}\n" +
		"\trouter := web.NewWebHandler()\n\trouter.Use(web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders())\n\treturn router, nil\n}\n"

	return []guardFixture{
		{name: "mode from LoadEnvForMode, constants only compared", rule: "P0", files: extra(
			"\tif mode == environment.ModeProduction {\n\t}\n\tswitch mode {\n\tcase environment.ModeDevelopment:\n\t}\n")},
		{name: "a required Mode env field", rule: "P0", files: root(postureResolve, modeField(`env:"APP_RUNTIME_MODE" required:"true"`))},
		{name: "LoadEnv reads .env in every mode", rule: "P0", violating: true, files: extra("\t_ = environment.LoadEnv()\n")},
		{name: "a Mode env field with a default", rule: "P0", violating: true, files: root(postureResolve,
			modeField(`env:"APP_RUNTIME_MODE" required:"true" default:"development"`))},
		{name: "ModeDevelopment passed as an argument", rule: "P0", violating: true, files: root(
			"run(context.Background(), mode)", "run(context.Background(), environment.ModeDevelopment)")},
		{name: "no mode resolved from the environment", rule: "P0", violating: true, files: root(postureResolve,
			"\tmode := environment.Mode(os.Getenv(\"APP_RUNTIME_MODE\"))\n")},

		{name: "every surface names the mode", rule: "P1", files: extra(
			"\tvar dbCfg pgxdb.Config\n\tdbCfg.RuntimeMode = mode\n" +
				"\t_, _ = tursodb.Open(ctx, tursodb.Config{URL: \"file:x\", RuntimeMode: mode})\n" +
				"\t_, _ = authentication.New(nil, nil, mode, \"jobs\")\n" +
				"\t_, _ = authorization.New(nil, authorization.WithRuntimeMode(mode))\n")},
		{name: "turso Config without RuntimeMode", rule: "P1", violating: true, files: extra(
			"\t_, _ = tursodb.Open(ctx, tursodb.Config{URL: \"file:x\"})\n")},
		{name: "var pgxdb.Config never given the mode", rule: "P1", violating: true, files: extra(
			"\tvar dbCfg pgxdb.Config\n\t_ = dbCfg\n")},
		{name: "authentication.New with the empty mode", rule: "P1", violating: true, files: extra(
			"\t_, _ = authentication.New(nil, nil, \"\", \"jobs\")\n")},
		{name: "authorization.New without WithRuntimeMode", rule: "P1", violating: true, files: extra(
			"\t_, _ = authorization.New(nil)\n")},

		{name: "router built and returned by a helper", rule: "P2", files: map[string]string{"cmd/server/main.go": postureRoot(
			"\trouter := web.NewWebHandler()\n\trouter.Use(web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders(), web.RequestID())\n",
			"\thandler, err := newHandler(srv)\n\tif err != nil {\n\t\treturn err\n\t}\n",
			"web.Run(ctx, router, srv, nil)", "web.Run(ctx, handler, srv, nil)",
		) + newHandler}},
		{name: "TrustProxies not outermost", rule: "P2", violating: true, files: root(
			"web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders(), web.RequestID()",
			"web.RequestID(), web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders()")},
		{name: "TrustProxiesWithConfig outermost", rule: "P2", files: root(
			"web.TrustProxies(srv.TrustedProxyCount), ",
			"web.TrustProxiesWithConfig(web.TrustProxiesConfig{TrustedProxyCount: srv.TrustedProxyCount, TrustRealIPHeader: true}), ")},
		{name: "TrustProxiesWithConfig not outermost", rule: "P2", violating: true, files: root(
			"web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders(), web.RequestID()",
			"web.SecurityHeaders(), web.TrustProxiesWithConfig(web.TrustProxiesConfig{TrustedProxyCount: srv.TrustedProxyCount}), web.RequestID()")},
		{name: "a wrapped handler", rule: "P2", violating: true, files: root(
			"web.Run(ctx, router, srv, nil)", "web.Run(ctx, http.TimeoutHandler(router, 0, \"\"), srv, nil)")},

		{name: "SecurityHeadersWithConfig in a later Use", rule: "P3", files: root(
			"router.Use(web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders(), web.RequestID())",
			"router.Use(web.TrustProxies(srv.TrustedProxyCount))\n\trouter.Use(web.SecurityHeadersWithConfig(web.SecurityHeadersConfig{FrameOptions: \"SAMEORIGIN\"}))")},
		{name: "no SecurityHeaders", rule: "P3", violating: true, files: root("web.SecurityHeaders(), ", "")},

		{name: "ServerConfig validated under production", rule: "P4", files: root()},
		{name: "ServerConfig never validated", rule: "P4", violating: true, files: root(
			"\tif mode.IsProduction() {\n\t\tif err := srv.ValidateProduction(); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", "")},

		{name: "true literal in an if's init statement", rule: "P5", notes: []string{"ack"}, files: root()},
		{name: "false literal", rule: "P5", notes: []string{}, files: root(
			"AdminUngatedAcknowledged: true", "AdminUngatedAcknowledged: false")},
		{name: "conditional under switch and if", rule: "P5", notes: []string{"ack", "ack (conditional)", "ack (conditional)"}, files: extra(
			"\tvar delivery authentication.DeliveryConfig\n\tswitch mode {\n\tcase environment.ModeDevelopment:\n" +
				"\t\tdelivery.DeliveryEphemeralAcknowledged = true\n\t}\n\tif mode.IsProduction() {\n" +
				"\t\tdelivery.DeliveryJobsAcknowledged = true\n\t}\n\t_ = delivery\n")},
		{name: "option form as a direct argument of New", rule: "P5", notes: []string{"ack", "ack"}, files: extra(
			"\t_, _ = authorization.New(nil, authorization.WithRuntimeMode(mode), authorization.WithAuditDisabledAcknowledged())\n")},
		{name: "an expression", rule: "P5", violating: true, files: root(
			"AdminUngatedAcknowledged: true", "AdminUngatedAcknowledged: os.Getenv(\"X\") != \"\"")},
		{name: "AppendOnlyWritableAcknowledged written true", rule: "P5", notes: []string{"ack", "ack"}, files: extra(
			"\tdbCfg := pgxdb.Config{RuntimeMode: mode, AppendOnlyWritableAcknowledged: true}\n\t_ = dbCfg\n")},
		{name: "AppendOnlyWritableAcknowledged from an expression", rule: "P5", violating: true, files: extra(
			"\tdbCfg := pgxdb.Config{RuntimeMode: mode}\n\tdbCfg.AppendOnlyWritableAcknowledged = os.Getenv(\"X\") != \"\"\n\t_ = dbCfg\n")},
		{name: "MaintenanceRunnerAcknowledged written true, UserErasureRoute no acknowledgement", rule: "P5", notes: []string{"ack", "ack"}, files: extra(
			"\t_ = authentication.WithMaintenance(authentication.MaintenanceConfig{MaintenanceRunnerAcknowledged: true})\n" +
				"\t_ = authentication.WithAdministration(authentication.AdministrationConfig{UserErasureRoute: true})\n")},
		{name: "MaintenanceRunnerAcknowledged from an expression", rule: "P5", violating: true, files: extra(
			"\tvar maint authentication.MaintenanceConfig\n\tmaint.MaintenanceRunnerAcknowledged = os.Getenv(\"X\") != \"\"\n\t_ = maint\n")},
		{name: "CookieDomainAcknowledged written true", rule: "P5", notes: []string{"ack", "ack"}, files: extra(
			"\t_ = authentication.WithBrowser(authentication.BrowserConfig{CookieDomainAcknowledged: true})\n")},
		{name: "CookieDomainAcknowledged from an expression", rule: "P5", violating: true, files: extra(
			"\tvar browser authentication.BrowserConfig\n\tbrowser.CookieDomainAcknowledged = os.Getenv(\"X\") != \"\"\n\t_ = browser\n")},
		{name: "fail-open waivers written true print posture_waiver, not ack", rule: "P5", notes: []string{"ack", "posture_waiver", "posture_waiver"}, files: extra(
			"\t_ = authentication.WithAbuseProtection(authentication.AbuseProtectionConfig{PublicRouteLimiterFailOpen: true})\n" +
				"\t_ = authentication.WithPassword(authentication.PasswordConfig{CompromisedPasswordFailOpen: true})\n")},
		{name: "a fail-open waiver under if, and one written false", rule: "P5", notes: []string{"ack", "posture_waiver (conditional)"}, files: extra(
			"\tvar abuse authentication.AbuseProtectionConfig\n\tif !mode.IsProduction() {\n\t\tabuse.PublicRouteLimiterFailOpen = true\n\t}\n\t_ = abuse\n" +
				"\t_ = authentication.WithPassword(authentication.PasswordConfig{CompromisedPasswordFailOpen: false})\n")},
		{name: "PublicRouteLimiterFailOpen from an expression", rule: "P5", violating: true, files: extra(
			"\t_ = authentication.WithAbuseProtection(authentication.AbuseProtectionConfig{PublicRouteLimiterFailOpen: !mode.IsProduction()})\n")},
		{name: "CompromisedPasswordFailOpen from an expression", rule: "P5", violating: true, files: extra(
			"\tvar password authentication.PasswordConfig\n\tpassword.CompromisedPasswordFailOpen = os.Getenv(\"X\") != \"\"\n\t_ = password\n")},
		{name: "option form outside New", rule: "P5", violating: true, files: extra(
			"\topts := []authorization.Option{authorization.WithAuditDisabledAcknowledged()}\n" +
				"\t_, _ = authorization.New(nil, append(opts, authorization.WithRuntimeMode(mode))...)\n")},

		{name: "keys from a KeysInput through ParseEnvTags", rule: "P6", notes: []string{"rand"}, files: extra(
			"\tvar in authentication.KeysInput\n\tif err := environment.ParseEnvTags(\"\", &in); err != nil {\n\t\treturn err\n\t}\n" +
				"\tif _, err := authentication.NewKeys(mode, in, nil, authentication.KeyJWT); err != nil {\n\t\treturn err\n\t}\n" +
				"\t_, _ = authentication.New(nil, nil, mode, \"jobs\")\n\tcursorKey := make([]byte, 32)\n\t_, _ = rand.Read(cursorKey)\n")},
		{name: "authentication without NewKeys", rule: "P6", violating: true, files: extra(
			"\t_, _ = authentication.New(nil, nil, mode, \"jobs\")\n")},
		{name: "KeysInput not from the environment", rule: "P6", violating: true, files: extra(
			"\tkeys, _ := authentication.NewKeys(mode, authentication.KeysInput{JWTSecret: \"00\"}, nil)\n\t_ = keys\n" +
				"\t_, _ = authentication.New(nil, nil, mode, \"jobs\")\n")},

		{name: "mode commented, TLS on, proxies and origin named", rule: "P7", files: env()},
		{name: "uncommented DB_SSLMODE=disable", rule: "P7", violating: true, files: env(
			"DB_SSLMODE=verify-full", "DB_SSLMODE=disable")},
		{name: "query logging commented out, false or empty", rule: "P7", files: env(
			"DB_SSLMODE=verify-full\n", "DB_SSLMODE=verify-full\n# DB_LOG_QUERIES=true\n# DB_LOG_QUERY_ARGS=true\nDB_LOG_QUERIES=false\nDB_LOG_QUERY_ARGS=\nAUTH_DB_LOG_QUERIES=0\n")},
		{name: "uncommented DB_LOG_QUERIES=true", rule: "P7", violating: true, files: env(
			"DB_SSLMODE=verify-full\n", "DB_SSLMODE=verify-full\nDB_LOG_QUERIES=true\n")},
		{name: "uncommented DB_LOG_QUERY_ARGS=true", rule: "P7", violating: true, files: env(
			"DB_SSLMODE=verify-full\n", "DB_SSLMODE=verify-full\nDB_LOG_QUERY_ARGS=true\n")},
		{name: "prefixed, quoted and spelled 1", rule: "P7", violating: true, files: env(
			"DB_SSLMODE=verify-full\n", "DB_SSLMODE=verify-full\nexport AUTH_DB_LOG_QUERIES=\"1\"  # dev only\n")},
		{name: "the mode key assigned, even empty", rule: "P7", violating: true, files: env(
			"TRUSTED_PROXY_COUNT=0\n", "TRUSTED_PROXY_COUNT=0\nAPP_RUNTIME_MODE=\n")},
		{name: "AUTH_PUBLIC_BASE_URL does not name PUBLIC_BASE_URL", rule: "P7", violating: true, files: env(
			"\nPUBLIC_BASE_URL=", "\nAUTH_PUBLIC_BASE_URL=")},
		{name: "the mode key never mentioned", rule: "P7", violating: true, files: env(
			"# APP_RUNTIME_MODE=development|production — process environment only\n", "")},

		{name: "cms.Config gated", rule: "P8", files: root(
			"AdminUngatedAcknowledged: true", "AdminMiddleware: []web.Middleware{web.RequestID()}")},
		{name: "cms.Config names neither", rule: "P8", violating: true, files: root(", AdminUngatedAcknowledged: true", "")},

		{name: "every store file copied byte-identical under new numbers", rule: "P9", files: p9Host(true, p9Ledger(nil))},
		{name: "a store file missing from the ledger", rule: "P9", violating: true, files: p9Host(true, p9Ledger(map[string]string{
			"0008_widgets_idx.sql": ""}))},
		{name: "an edited copy", rule: "P9", violating: true, files: p9Host(true, p9Ledger(map[string]string{
			"0008_widgets_idx.sql": "CREATE INDEX widgets_id ON widgets (id DESC);\n"}))},
		{name: "an unresolvable store module", rule: "P9", violating: true, files: withFiles(p9Host(true, p9Ledger(nil)), map[string]string{
			"go.mod": p9GoMod("./_absent")})},
		{name: "an immutability trigger file declined by omission", rule: "P9", files: p9Host(true, p9Ledger(map[string]string{
			"0009_widgets_immutable.sql": ""}))},
		{name: "an edited immutability trigger copy", rule: "P9", violating: true, files: p9Host(true, p9Ledger(map[string]string{
			"0009_widgets_immutable.sql": "CREATE TRIGGER widgets_immutable;\n"}))},
	}
}

// The P9 fixture: a framework-shaped store module nested under _store (the walk
// skips `_` directories) and resolved through a relative replace, its three
// migrations (the last an immutability trigger file) and a scratch file, and the
// host ledger's renumbered copies.
var (
	p9Store      = fwPockets + "/acme/stores/pgx"
	p9StoreFiles = map[string]string{
		"0001_widgets.sql":           "CREATE TABLE widgets (id TEXT PRIMARY KEY);\n",
		"0002_widgets_idx.sql":       "CREATE INDEX widgets_id ON widgets (id);\n",
		"0003_widgets_immutable.sql": "CREATE OR REPLACE TRIGGER widgets_immutable;\n",
		"_scratch.sql":               "-- not a migration\n",
	}
	p9LedgerFiles = map[string]string{
		"0001_host_notes.sql":        "CREATE TABLE notes (id TEXT PRIMARY KEY);\n",
		"0007_widgets.sql":           p9StoreFiles["0001_widgets.sql"],
		"0008_widgets_idx.sql":       p9StoreFiles["0002_widgets_idx.sql"],
		"0009_widgets_immutable.sql": p9StoreFiles["0003_widgets_immutable.sql"],
	}
)

// p9GoMod requires the fixture store and replaces it with dir.
func p9GoMod(dir string) string {
	return "module " + fixtureModule + "\n\ngo 1.26\n\nrequire " + p9Store + " v0.0.0\n\nreplace " + p9Store + " => " + dir + "\n"
}

// p9Ledger returns the clean ledger with edits applied: an empty value drops
// the file, any other replaces its bytes.
func p9Ledger(edits map[string]string) map[string]string {
	out := map[string]string{}
	for name, src := range p9LedgerFiles {
		out[name] = src
	}
	for name, src := range edits {
		if src == "" {
			delete(out, name)
			continue
		}
		out[name] = src
	}
	return out
}

// p9Host renders a host requiring the fixture store, importing it from
// cmd/server in production (or only from a test), with the given ledger.
func p9Host(production bool, ledger map[string]string) map[string]string {
	files := map[string]string{
		"go.mod":              p9GoMod("./_store"),
		"_store/go.mod":       "module " + p9Store + "\n\ngo 1.26\n",
		"_store/store.go":     goSrc("pgx", nil, ""),
		"cmd/server/store.go": goSrc("main", []string{p9Store}, ""),
	}
	if !production {
		delete(files, "cmd/server/store.go")
		files["cmd/server/store_test.go"] = goSrc("main", []string{p9Store}, "")
	}
	for name, src := range p9StoreFiles {
		files["_store/migrations/"+name] = src
	}
	for name, src := range ledger {
		files["workshop/migrations/primary/"+name] = src
	}
	return files
}

// withFiles returns files with extra added or replaced.
func withFiles(files, extra map[string]string) map[string]string {
	for rel, src := range extra {
		files[rel] = src
	}
	return files
}

// TestGuardP9Scope proves P9 never reads as clean when it checked nothing: no
// store required, a store named only by tests, and a mounted store without a
// migrations/ directory each report `no scope` with their reason.
func TestGuardP9Scope(t *testing.T) {
	noMigrations := p9Host(true, p9Ledger(nil))
	for rel := range noMigrations {
		if strings.HasPrefix(rel, "_store/migrations/") {
			delete(noMigrations, rel)
		}
	}
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no store module required", nil, "no framework store module mounted"},
		{"a store named only by a test", p9Host(false, p9Ledger(nil)), "no framework store module mounted"},
		{"a mounted store without migrations/", noMigrations, "no mounted store ships migrations/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := evalFixture(t, guardFixture{rule: "P9", files: c.files})
			if len(res.findings) > 0 || res.checked != 0 || res.noScope != c.want {
				t.Fatalf("P9 = checked %d, noScope %q, findings %+v; want no scope (%s)", res.checked, res.noScope, res.findings, c.want)
			}
		})
	}

	root := writeFixture(t, guardFixture{files: p9Host(true, p9Ledger(nil))})
	out := captureStdout(t, func() int { return Run([]string{"guard", "--posture", root}) })
	if !strings.Contains(out, "\nP9   ok        (1 store)\n") {
		t.Fatalf("guard --posture lacks the P9 ok line:\n%s", out)
	}
}

// TestGuardP9Declined proves the decline path is visible, never silent: a
// ledger without a store's immutability trigger file is clean and the status
// line names the declined file; with a required file also missing, the
// findings line still names it.
func TestGuardP9Declined(t *testing.T) {
	declined := p9Ledger(map[string]string{"0009_widgets_immutable.sql": ""})
	var code int
	root := writeFixture(t, guardFixture{files: p9Host(true, declined)})
	out := captureStdout(t, func() int { code = Run([]string{"guard", "--posture", root}); return code })
	if code != 0 || !strings.Contains(out, "\nP9   ok        (1 store; declined: 0003_widgets_immutable.sql)\n") {
		t.Fatalf("exit %d, want 0 with the declined note on the P9 status line:\n%s", code, out)
	}

	declined = p9Ledger(map[string]string{"0009_widgets_immutable.sql": "", "0008_widgets_idx.sql": ""})
	root = writeFixture(t, guardFixture{files: p9Host(true, declined)})
	out = captureStdout(t, func() int { code = Run([]string{"guard", "--posture", root}); return code })
	if code != 1 || !strings.Contains(out, "\nP9   1 finding (declined: 0003_widgets_immutable.sql)\n") {
		t.Fatalf("exit %d, want 1 with the declined note on the P9 findings line:\n%s", code, out)
	}
}

// TestGuardP7QueryLogging pins the query-logging clause's finding: one per
// uncommented line set true, at that line, naming the key as written.
func TestGuardP7QueryLogging(t *testing.T) {
	env := postureEnv("DB_SSLMODE=verify-full\n", "DB_SSLMODE=verify-full\nDB_LOG_QUERIES=true\nAUTH_DB_LOG_QUERY_ARGS=TRUE\n")
	res := evalFixture(t, guardFixture{rule: "P7", files: map[string]string{"cmd/server/main.go": postureRoot(), ".env.example": env}})
	want := []guardFinding{
		{where: ".env.example:5", msg: "uncommented DB_LOG_QUERIES set true (comment it out; production refuses query logging)"},
		{where: ".env.example:6", msg: "uncommented AUTH_DB_LOG_QUERY_ARGS set true (comment it out; production refuses query logging)"},
	}
	if !slices.Equal(res.findings, want) {
		t.Fatalf("P7 findings = %+v, want %+v", res.findings, want)
	}
}

// sameLabels reports whether got and want hold the same labels in any order.
func sameLabels(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	return slices.Equal(g, w)
}

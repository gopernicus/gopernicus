package commands

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// fixtureModule is the module path of every generated guard fixture host.
const fixtureModule = "example.com/acme"

// contractRow matches one `| **Hn** | sentence |` row of examples/README.md §3.
// The import-law table's rows carry words after the bold id and never match.
var contractRow = regexp.MustCompile(`^\| \*\*(H[0-9]+)\*\* \| (.*) \|$`)

// guardFixture is one generated host: files maps a host-relative path to its
// contents, written beside a go.mod and a cmd/server composition root.
type guardFixture struct {
	name      string
	rule      string
	module    string
	files     map[string]string
	violating bool
}

// TestGuardListMatchesContract is what makes "the binary and this page cannot
// drift" true: every canonical sentence the CLI embeds is byte-equal to the
// examples/README.md §3 row with the same id, and the ids agree in order.
func TestGuardListMatchesContract(t *testing.T) {
	f, err := os.Open(filepath.Join(repoRoot(t), "examples", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var ids, sentences []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if m := contractRow.FindStringSubmatch(sc.Text()); m != nil {
			ids = append(ids, m[1])
			sentences = append(sentences, m[2])
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	if len(ids) != len(guardRules) {
		t.Fatalf("examples/README.md §3 has %d canonical rows, the CLI embeds %d rules", len(ids), len(guardRules))
	}
	for i, rule := range guardRules {
		if ids[i] != rule.id {
			t.Errorf("row %d: README id %s, CLI id %s", i, ids[i], rule.id)
		}
		if sentences[i] != rule.sentence {
			t.Errorf("%s drifted:\nREADME: %s\nCLI:    %s", rule.id, sentences[i], rule.sentence)
		}
	}

	out := captureStdout(t, func() int { return Run([]string{"guard", "--list"}) })
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != len(guardRules) {
		t.Fatalf("--list printed %d lines, want %d", len(lines), len(guardRules))
	}
	for i, line := range lines {
		want := ids[i] + strings.Repeat(" ", 4-len(ids[i])) + sentences[i]
		if line != want {
			t.Errorf("--list line %d:\n got %q\nwant %q", i, line, want)
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
		})
		if c.violating {
			violating[c.rule] = true
		} else {
			clean[c.rule] = true
		}
	}
	for _, rule := range guardRules {
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

// evalFixture writes c, loads it, and evaluates c's rule.
func evalFixture(t *testing.T, c guardFixture) guardResult {
	t.Helper()
	host, err := loadHost(writeFixture(t, c))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range guardRules {
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

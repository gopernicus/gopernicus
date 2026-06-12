package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gopernicus/gopernicus/workshop/codegen/cli"

	"github.com/gopernicus/gopernicus/workshop/codegen/generators"
	"github.com/gopernicus/gopernicus/workshop/codegen/goversion"
	"github.com/gopernicus/gopernicus/workshop/codegen/manifest"
	"github.com/gopernicus/gopernicus/workshop/codegen/project"
	"github.com/gopernicus/gopernicus/workshop/codegen/sqlguard"
)

func init() {
	cli.RegisterCommand(&cli.Command{
		Name:  "doctor",
		Short: "Check project health and configuration",
		Long: `Check that your project is correctly configured for gopernicus.

Verifies:
  - go.mod exists and is a valid Go module
  - Go version meets minimum requirement
  - gopernicus.yml manifest exists and is valid
  - Workshop directory exists
  - Gopernicus framework is in go.mod dependencies`,
		Usage: "gopernicus doctor [--json]",
		Run:   runDoctor,
	})
}

type check struct {
	name   string
	passed bool
	detail string
	warn   bool // warning, not failure
}

// doctorResult is the machine-readable shape of a doctor run. Field names
// are a stable contract — agents and scripts parse this output.
type doctorResult struct {
	Root      string        `json:"root"`
	Framework string        `json:"framework,omitempty"`
	OK        bool          `json:"ok"`
	Checks    []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Warn   bool   `json:"warn,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func runDoctor(_ context.Context, args []string) error {
	asJSON := hasFlag(args, "--json")

	root, err := project.FindRoot()
	if err != nil {
		rootCheck := check{name: "project root", passed: false, detail: "no go.mod found in current or parent directories"}
		if asJSON {
			printDoctorJSON(buildDoctorResult("", "", []check{rootCheck}))
		} else {
			fmt.Println("✗ project root — no go.mod found in current or parent directories")
		}
		return fmt.Errorf("doctor found problems")
	}

	frameworkCheck, frameworkVersion := checkFrameworkDep(root)
	checks := []check{
		checkGoMod(root),
		checkGoVersion(root),
		checkManifest(root),
		checkWorkshopDir(root),
		frameworkCheck,
	}
	checks = append(checks, checkSQLGuards(root)...)
	checks = append(checks, checkBodyLimits(root))
	checks = append(checks, checkBootstrapDrift(root))

	result := buildDoctorResult(root, frameworkVersion, checks)

	if asJSON {
		printDoctorJSON(result)
	} else {
		printDoctorHuman(result)
	}

	if !result.OK {
		return fmt.Errorf("doctor found problems")
	}
	return nil
}

// buildDoctorResult folds raw checks into the stable result shape. A run is
// OK when no check is a hard failure (warnings don't fail).
func buildDoctorResult(root, frameworkVersion string, checks []check) doctorResult {
	result := doctorResult{
		Root:      root,
		Framework: frameworkVersion,
		OK:        true,
		Checks:    make([]doctorCheck, 0, len(checks)),
	}
	for _, c := range checks {
		if !c.passed && !c.warn {
			result.OK = false
		}
		result.Checks = append(result.Checks, doctorCheck{
			Name:   c.name,
			Passed: c.passed,
			Warn:   c.warn,
			Detail: c.detail,
		})
	}
	return result
}

func printDoctorHuman(result doctorResult) {
	fmt.Printf("  project root: %s\n\n", result.Root)

	for _, c := range result.Checks {
		symbol := "✓"
		if !c.Passed && !c.Warn {
			symbol = "✗"
		} else if c.Warn {
			symbol = "!"
		}
		line := fmt.Sprintf("%s %s", symbol, c.Name)
		if c.Detail != "" {
			line += fmt.Sprintf(" — %s", c.Detail)
		}
		fmt.Println(line)
	}

	fmt.Println()
	if !result.OK {
		fmt.Println("Some checks failed. Run 'gopernicus init' to set up a project.")
		return
	}
	fmt.Println("All checks passed.")
}

func printDoctorJSON(result doctorResult) {
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Printf(`{"root":%q,"ok":false,"checks":[{"name":"json encoding","passed":false,"detail":%q}]}`+"\n", result.Root, err.Error())
		return
	}
	fmt.Println(string(out))
}

// hasFlag reports whether a bare boolean flag is present in args.
func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

func checkGoMod(root string) check {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return check{name: "go.mod", passed: false, detail: "not found"}
	}
	return check{name: "go.mod", passed: true}
}

func checkGoVersion(root string) check {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return check{name: "go version", passed: false, detail: "could not read go.mod"}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "go ") {
			v := strings.TrimPrefix(line, "go ")
			v = strings.Fields(v)[0] // strip any inline comments
			name := fmt.Sprintf("go version (go %s)", v)
			if !goversion.MeetsMinimum(v, goversion.MinGoVersion) {
				return check{name: name, passed: false, detail: fmt.Sprintf("requires go %s or later", goversion.MinGoVersion)}
			}
			return check{name: name, passed: true}
		}
	}
	return check{name: "go version", passed: false, detail: "not found in go.mod"}
}

func checkManifest(root string) check {
	if _, err := manifest.Load(root); err != nil {
		return check{name: "gopernicus.yml", passed: false, detail: "not found — run 'gopernicus init'"}
	}
	return check{name: "gopernicus.yml", passed: true}
}

func checkWorkshopDir(root string) check {
	if _, err := os.Stat(filepath.Join(root, "workshop/migrations")); err != nil {
		return check{name: "workshop/migrations/", passed: false, detail: "not found — run 'gopernicus init'"}
	}
	return check{name: "workshop/migrations/", passed: true}
}

// checkSQLGuards runs the standing SQL-injection guard over store packages:
// unsanctioned dynamic concatenation into SQL fails; Pred.Raw usage warns
// (it is a legitimate escape hatch that deserves review on every run).
func checkSQLGuards(root string) []check {
	findings, err := sqlguard.Scan(root)
	if err != nil {
		return []check{{name: "sql guards", passed: false, detail: err.Error()}}
	}

	var concat, raw []sqlguard.Finding
	for _, f := range findings {
		if f.Kind == "raw" {
			raw = append(raw, f)
		} else {
			concat = append(concat, f)
		}
	}

	checks := make([]check, 0, 2)
	if len(concat) == 0 {
		checks = append(checks, check{name: "sql: parameterized queries", passed: true})
	} else {
		detail := concat[0].Pos + " " + concat[0].Message
		if len(concat) > 1 {
			detail = fmt.Sprintf("%s (+%d more)", detail, len(concat)-1)
		}
		checks = append(checks, check{name: "sql: parameterized queries", passed: false, detail: detail})
	}
	if len(raw) > 0 {
		detail := raw[0].Pos + " " + raw[0].Message
		if len(raw) > 1 {
			detail = fmt.Sprintf("%s (+%d more)", detail, len(raw)-1)
		}
		checks = append(checks, check{name: "sql: Pred.Raw usage", passed: true, warn: true, detail: detail})
	}
	return checks
}

// checkBodyLimits warns when a write route (Create/Update) in any bridge.yml
// has no max_body_size middleware — unbounded request bodies are a
// resource-exhaustion vector (P6). A warning, not a failure: the default
// limit may be intentional.
func checkBodyLimits(root string) check {
	var unbounded []string

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "bridge.yml" {
			return nil
		}
		yml, perr := generators.ParseBridgeYML(path)
		if perr != nil || yml == nil {
			return nil
		}
		for _, route := range yml.Routes {
			if route.Func != "Create" && route.Func != "Update" {
				continue
			}
			hasLimit := false
			for _, mw := range route.Middleware {
				if mw.MaxBodySize > 0 {
					hasLimit = true
					break
				}
			}
			if !hasLimit {
				rel, _ := filepath.Rel(root, path)
				unbounded = append(unbounded, fmt.Sprintf("%s:%s", rel, route.Func))
			}
		}
		return nil
	})

	if len(unbounded) == 0 {
		return check{name: "bridge: write routes bound body size", passed: true}
	}
	detail := unbounded[0] + " has no max_body_size"
	if len(unbounded) > 1 {
		detail = fmt.Sprintf("%s (+%d more)", detail, len(unbounded)-1)
	}
	return check{name: "bridge: write routes bound body size", passed: true, warn: true, detail: detail}
}

// checkBootstrapDrift compares each bootstrap file's creation marker
// (`// gopernicus:bootstrap kind=... template=<hash>`) against the current
// framework's template hash for that kind. A mismatch means the file was
// created from an older template — a warning, never a failure: bootstraps
// are user-owned and refresh is a deliberate act. Conventionally-named
// bootstrap files without a marker (created before v0.4) are counted in
// the detail so the one-time refresh path is visible.
func checkBootstrapDrift(root string) check {
	const name = "bootstrap: template drift"

	basenames := generators.BootstrapBasenames()
	var drifted []string
	tracked, unmarked := 0, 0

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)

		// Deploy profile files (workshop/deploy/, .github/workflows/) are
		// non-Go bootstraps with app-specific names — any file there that
		// carries a marker is tracked; unmarked files are the user's own.
		deployFile := strings.HasPrefix(rel, filepath.Join("workshop", "deploy")+string(filepath.Separator)) ||
			strings.HasPrefix(rel, filepath.Join(".github", "workflows")+string(filepath.Separator))

		if !deployFile {
			if !basenames[d.Name()] || !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			if !strings.HasPrefix(rel, "core"+string(filepath.Separator)) &&
				!strings.HasPrefix(rel, "bridge"+string(filepath.Separator)) &&
				!strings.HasPrefix(rel, filepath.Join("workshop", "testing")+string(filepath.Separator)) {
				return nil
			}
		}

		firstLine, rerr := readMarkerLine(path)
		if rerr != nil {
			return nil
		}
		kind, hash, ok := generators.ParseBootstrapMarker(firstLine)
		if !ok {
			if !deployFile {
				unmarked++
			}
			return nil
		}
		current, known := generators.BootstrapTemplateHash(kind)
		if !known {
			// Marker from a different framework vintage whose kind no
			// longer exists — surface it as drift, the honest reading.
			drifted = append(drifted, rel+" (unknown kind "+kind+")")
			return nil
		}
		tracked++
		if hash != current {
			drifted = append(drifted, rel)
		}
		return nil
	})

	if len(drifted) > 0 {
		detail := drifted[0] + " created from an older template — review or refresh (see the upgrading guide)"
		if len(drifted) > 1 {
			detail = fmt.Sprintf("%s (+%d more)", detail, len(drifted)-1)
		}
		return check{name: name, passed: true, warn: true, detail: detail}
	}

	detail := ""
	switch {
	case tracked > 0 && unmarked > 0:
		detail = fmt.Sprintf("%d tracked; %d pre-marker bootstrap files (created before v0.4)", tracked, unmarked)
	case tracked > 0:
		detail = fmt.Sprintf("%d tracked", tracked)
	case unmarked > 0:
		detail = fmt.Sprintf("%d pre-marker bootstrap files (created before v0.4 — refresh to start tracking)", unmarked)
	}
	return check{name: name, passed: true, detail: detail}
}

// readMarkerLine returns the line a bootstrap marker would occupy: the
// first line, or the second when the file opens with a shebang (shell
// bootstraps keep `#!` on line 1).
func readMarkerLine(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#!") && scanner.Scan() {
			return scanner.Text(), nil
		}
		return line, nil
	}
	return "", scanner.Err()
}

// checkFrameworkDep verifies the framework pin in go.mod and also returns
// the pinned version string ("" when absent) for the doctor result's
// top-level framework field.
func checkFrameworkDep(root string) (check, string) {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return check{name: "gopernicus dependency", passed: false, detail: "could not read go.mod"}, ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, generators.FrameworkModulePath) {
			// Extract version from the require line. In the framework repo
			// itself the match is the module declaration, so the trailing
			// token is the module path — only report version-shaped tokens
			// in the result's framework field.
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				token := parts[len(parts)-1]
				version := ""
				if strings.HasPrefix(token, "v") {
					version = token
				}
				return check{name: fmt.Sprintf("gopernicus framework (%s)", token), passed: true}, version
			}
			return check{name: "gopernicus framework", passed: true}, ""
		}
	}
	return check{
		name:   "gopernicus framework",
		passed: false,
		detail: "not found in go.mod — run 'go get " + generators.FrameworkModulePath + "@latest'",
	}, ""
}

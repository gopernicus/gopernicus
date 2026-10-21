package commands

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The framework module families the classifier enumerates. Assembled from
// baseModule so no source in workshop/ carries the quoted pocket/example prefix
// that G11 greps for.
var (
	fwSDK          = baseModule + "/sdk"
	fwPockets      = baseModule + "/pockets"
	fwIntegrations = baseModule + "/integrations"
	fwUI           = baseModule + "/ui"
)

// The recognized lists of examples/README.md §3 "Recognized lists". Rule text
// and implementation name the same paths; a module matches by path prefix.
var (
	recognizedDrivers = []string{
		"github.com/jackc/pgx/v5",
		"github.com/tursodatabase/libsql-client-go",
		"modernc.org/sqlite",
		"github.com/lib/pq",
	}
	recognizedViewTech = []string{
		"github.com/a-h/templ",
	}
)

// The directory names the walk never descends into, at any depth — the same
// set the go tool ignores, plus node_modules.
var guardSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"testdata":     true,
}

// importKind is the classification of one import path, host first.
type importKind int

const (
	impStdlib importKind = iota
	impHost
	impFwSDK
	impFwPocketCore
	impFwPocketStore
	impFwPocketViews
	impFwIntegration
	impFwUI
	impDriver
	impViewTech
	impThirdParty
)

// guardImport is one import spec of a parsed file.
type guardImport struct {
	path string
	line int
	kind importKind
	// rel is the host-relative path of a host import ("" for the module root).
	rel string
	// pocket is the framework pocket name of a pocket core/store/views import
	// ("" for the root contract package).
	pocket string
}

// guardFile is one parsed Go file of a host package.
type guardFile struct {
	rel     string // slash path relative to the host root
	test    bool
	fset    *token.FileSet
	ast     *ast.File
	imports []guardImport
}

// guardPackage is one directory holding at least one non-ignored .go file.
type guardPackage struct {
	rel   string // slash path relative to the host root; "." for the root
	files []*guardFile
}

// guardHost is everything the rule evaluators read: the module path, the
// packages in sorted order, and the nested modules the walk did not enter.
type guardHost struct {
	root     string
	module   string
	packages []*guardPackage
	nested   []string
}

// readModulePath returns the first `module` line of <root>/go.mod: a trailing
// `//` comment is stripped, a quoted path unquoted, CRLF tolerated.
func readModulePath(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))
		rest, ok := strings.CutPrefix(line, "module")
		if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t' && rest[0] != '"') {
			continue
		}
		if i := strings.Index(rest, "//"); i >= 0 {
			rest = rest[:i]
		}
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, `"`) || strings.HasPrefix(rest, "`") {
			u, err := strconv.Unquote(rest)
			if err != nil {
				return "", fmt.Errorf("go.mod: malformed module line %q", line)
			}
			rest = u
		}
		if rest == "" || strings.ContainsAny(rest, " \t") {
			return "", fmt.Errorf("go.mod: malformed module line %q", line)
		}
		return rest, nil
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("go.mod: no module line")
}

// loadHost walks root and parses every package. Symlinks are not followed. A
// directory other than root that holds a go.mod is a separate module: it is
// recorded in nested and never entered.
func loadHost(root string) (*guardHost, error) {
	module, err := readModulePath(root)
	if err != nil {
		return nil, err
	}
	h := &guardHost{root: root, module: module}
	byDir := map[string]*guardPackage{}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			name := d.Name()
			if guardSkipDirs[name] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				h.nested = append(h.nested, rel)
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !d.Type().IsRegular() || !strings.HasSuffix(name, ".go") ||
			strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			return nil
		}
		gf, err := parseGuardFile(p, rel, module)
		if err != nil {
			return err
		}
		if gf == nil {
			return nil
		}
		dir := path.Dir(rel)
		pkg := byDir[dir]
		if pkg == nil {
			pkg = &guardPackage{rel: dir}
			byDir[dir] = pkg
			h.packages = append(h.packages, pkg)
		}
		pkg.files = append(pkg.files, gf)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(h.packages, func(i, j int) bool { return h.packages[i].rel < h.packages[j].rel })
	for _, pkg := range h.packages {
		sort.Slice(pkg.files, func(i, j int) bool { return pkg.files[i].rel < pkg.files[j].rel })
	}
	sort.Strings(h.nested)
	return h, nil
}

// parseGuardFile fully parses one file and classifies its imports. A file
// carrying `//go:build ignore` returns nil. Other build constraints are
// deliberately not evaluated: an import is an import regardless of tag.
func parseGuardFile(p, rel, module string) (*guardFile, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.TrimSpace(c.Text) == "//go:build ignore" {
				return nil, nil
			}
		}
	}
	gf := &guardFile{rel: rel, test: strings.HasSuffix(rel, "_test.go"), fset: fset, ast: f}
	for _, spec := range f.Imports {
		ip, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: bad import %s", rel, spec.Path.Value)
		}
		imp := classifyImport(ip, module)
		imp.line = fset.Position(spec.Pos()).Line
		gf.imports = append(gf.imports, imp)
	}
	return gf, nil
}

// classifyImport resolves an import host first, then against the enumerated
// framework families. Framework workshop/ and examples/ paths, and anything
// else under the framework prefix, are third-party for every rule.
func classifyImport(ip, module string) guardImport {
	imp := guardImport{path: ip}
	switch {
	case ip == module:
		imp.kind = impHost
		return imp
	case strings.HasPrefix(ip, module+"/"):
		imp.kind, imp.rel = impHost, strings.TrimPrefix(ip, module+"/")
		return imp
	case underPath(ip, fwSDK):
		imp.kind = impFwSDK
		return imp
	case underPath(ip, fwIntegrations) && ip != fwIntegrations:
		imp.kind = impFwIntegration
		return imp
	case underPath(ip, fwUI) && ip != fwUI:
		imp.kind = impFwUI
		return imp
	case ip == fwPockets:
		imp.kind = impFwPocketCore
		return imp
	case strings.HasPrefix(ip, fwPockets+"/"):
		parts := strings.Split(strings.TrimPrefix(ip, fwPockets+"/"), "/")
		imp.pocket = parts[0]
		imp.kind = impFwPocketCore
		if len(parts) > 1 {
			switch parts[1] {
			case "stores":
				imp.kind = impFwPocketStore
			case "views":
				imp.kind = impFwPocketViews
			}
		}
		return imp
	}
	first, _, _ := strings.Cut(ip, "/")
	switch {
	case !strings.Contains(first, "."):
		imp.kind = impStdlib
	case matchesAny(ip, recognizedDrivers):
		imp.kind = impDriver
	case matchesAny(ip, recognizedViewTech):
		imp.kind = impViewTech
	default:
		imp.kind = impThirdParty
	}
	return imp
}

func underPath(p, prefix string) bool {
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

func matchesAny(p string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if underPath(p, prefix) {
			return true
		}
	}
	return false
}

// hasProduction reports whether a package has a non-test file.
func (p *guardPackage) hasProduction() bool {
	for _, f := range p.files {
		if !f.test {
			return true
		}
	}
	return false
}

// isFwPocket reports whether an import names any framework pocket path.
func (i guardImport) isFwPocket() bool {
	return i.kind == impFwPocketCore || i.kind == impFwPocketStore || i.kind == impFwPocketViews
}

// segments splits a host-relative slash path; "." and "" are the root.
func segments(rel string) []string {
	if rel == "." || rel == "" {
		return nil
	}
	return strings.Split(rel, "/")
}

// shapeOK reports whether a package directory sits in one of the contract's
// sanctioned slots (H0). The positive cmd/<binary> check is separate.
func shapeOK(rel string) bool {
	s := segments(rel)
	if len(s) == 0 {
		return false
	}
	switch s[0] {
	case "cmd", "sdk", "integrations", "ui", "workshop":
		return true
	case "internal":
		if len(s) < 2 {
			return false
		}
		switch s[1] {
		case "logic":
			return len(s) >= 4 && (s[2] == "domains" || s[2] == "compositions")
		case "inbound":
			if len(s) < 3 {
				return false
			}
			switch s[2] {
			case "domains", "compositions":
				return len(s) >= 4
			case "middleware", "wire", "views":
				return true
			}
		case "outbound":
			return len(s) >= 4 && s[2] == "domains"
		}
		return false
	case "pockets":
		return len(s) >= 3 && (s[2] == "logic" || s[2] == "inbound" || s[2] == "outbound")
	}
	return false
}

// underRel reports whether a host-relative path sits at or below prefix.
func underRel(rel, prefix string) bool {
	return rel == prefix || strings.HasPrefix(rel, prefix+"/")
}

// hostPocketOf returns the host pocket name and layer of a host-relative path
// under pockets/<n>/<layer>, or "" when the path is not in a host pocket.
func hostPocketOf(rel string) (name, layer string) {
	s := segments(rel)
	if len(s) < 2 || s[0] != "pockets" {
		return "", ""
	}
	if len(s) >= 3 {
		layer = s[2]
	}
	return s[1], layer
}

// slotName returns the <name> segment of internal/<side>/<kind>/<name>/…, or "".
func slotName(rel, prefix string) string {
	rest, ok := strings.CutPrefix(rel, prefix+"/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}

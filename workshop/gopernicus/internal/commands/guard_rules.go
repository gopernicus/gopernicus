package commands

import (
	"go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The canonical sentences of examples/README.md §3, byte for byte. They are the
// single source for `guard --list` and for the rule line under each finding
// block; TestGuardListMatchesContract fails the moment the two drift. H9's two
// tokens are split so the repo's whole-tree G9/G10 greps never match this file.
const (
	sentenceH0  = "A host's production Go packages live only under `cmd/`, `sdk/`, `integrations/`, `internal/{logic,inbound,outbound}`, `pockets/<name>/{logic,inbound,outbound}`, `ui/`, or `workshop/`, and at least one package sits under `cmd/<binary>/`."
	sentenceH1  = "`internal/logic/**` imports only the standard library, framework `sdk/...`, the host's own `sdk/...`, and its own `internal/logic/...` — an allow-list, with no escape valve."
	sentenceH2  = "A logic domain never imports a composition, and never imports another logic domain."
	sentenceH3  = "A composition declares a `Dependencies` struct whose every field is a non-empty `*Service` interface declared in that same package, imports at least one logic domain, and holds no repository port, no storage import, and no transaction handle."
	sentenceH4  = "`internal/inbound/**` may reach `internal/logic`, framework and host `sdk`, framework pocket cores and their `views/<pkg>`, `ui/*`, and a host pocket's `logic` and `inbound` — never `internal/outbound`, the host's `integrations/`, or a host pocket's `outbound`."
	sentenceH5  = "`internal/outbound/domains/<d>` imports, among host packages, only `internal/logic/domains/<d>`, the host's `sdk/`, and host pockets — never inbound, never another logic domain, never the host's `integrations/` (it declares the narrow port; `cmd` injects the integration)."
	sentenceH6  = "A host pocket extends at least one framework pocket, its `logic` stays on stdlib + `sdk` + the host's `sdk/` + framework pocket rims, and no part of it imports `internal/*` or another host pocket."
	sentenceH7  = "Only `cmd/**`, `internal/outbound/**`, the host's `integrations/**`, a host pocket's `outbound/**`, and `workshop/**` may import a framework pocket's `stores/*`, a framework integration, or a recognized database driver."
	sentenceH8  = "Every `internal/inbound/domains/<d>`, `internal/inbound/compositions/<c>`, and `internal/outbound/domains/<d>` has its `internal/logic` counterpart — the implication runs one way only."
	sentenceH9  = "`." + "Underlying()` is called nowhere outside the host's `integrations/` and `workshop/`, and `RowToStructByName" + "Lax` appears nowhere."
	sentenceH10 = "In production files, `cmd/**` contains no SQL statement literal, and a composition root under `cmd/<binary>/` declares no interface type; a client binary — a `cmd/<binary>/` tree that imports nothing under `internal/`, `pockets/`, or the host's `integrations/`, and no framework pocket, store, integration, or database driver — may own its commands, API client, wire types, and interfaces."
	sentenceH11 = "A host integration `integrations/<capability>[/<vendor>]` holds no domain: it imports only the standard library, framework `sdk/...`, the host's `sdk/...`, and exactly one connector or vendor client library plus the companion modules its API forces; it never imports `internal/*` or a pocket of either scope, and only `cmd/**` and `workshop/**` import it — tests included."
	sentenceH12 = "The outer fence, which H1, H4, H6 and H7 tighten and never loosen: `internal/**` and `pockets/**`, tests included, import only the standard library, the host module, and the framework's `sdk`, `pockets`, `integrations` and `ui` modules; recognized database drivers are additionally allowed under `internal/outbound/**` and a host pocket's `outbound/**`, recognized view technology under `internal/inbound/**` and a host pocket's `inbound/**`; production files never import the host's `workshop/`."
	sentenceH13 = "The host's `sdk/**` imports only the standard library, framework `sdk/...`, and its own `sdk/...`, and holds no domain entity, repository port, or `Service` — a type two domains both need is still each domain's own."
	sentenceH14 = "The host's `ui/**` imports only the standard library, framework `sdk/...`, framework `ui/*`, recognized view technology, the host's `sdk/...`, and its own `ui/...` — never `internal/*`, a pocket of either scope, or an integration of either scope."
)

// The H10 SQL-literal floor: a string literal whose trimmed, upper-cased text
// starts with one of these verbs followed by whitespace.
var sqlVerbs = []string{"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "DROP"}

// guardRules is the H table in id order.
var guardRules = []guardRule{
	{"H0", sentenceH0, evalH0},
	{"H1", sentenceH1, evalH1},
	{"H2", sentenceH2, evalH2},
	{"H3", sentenceH3, evalH3},
	{"H4", sentenceH4, evalH4},
	{"H5", sentenceH5, evalH5},
	{"H6", sentenceH6, evalH6},
	{"H7", sentenceH7, evalH7},
	{"H8", sentenceH8, evalH8},
	{"H9", sentenceH9, evalH9},
	{"H10", sentenceH10, evalH10},
	{"H11", sentenceH11, evalH11},
	{"H12", sentenceH12, evalH12},
	{"H13", sentenceH13, evalH13},
	{"H14", sentenceH14, evalH14},
}

// guardRule is one H or P id: its canonical sentence and its evaluator.
type guardRule struct {
	id       string
	sentence string
	eval     func(h *guardHost) guardResult
}

// guardResult is one evaluator's verdict. checked counts the packages (or
// directories) in scope — or, when unit is set, that unit; a result with
// nothing checked and no findings reports noScope as its reason. notes are
// informational lines that never change the exit code.
type guardResult struct {
	checked  int
	unit     string
	noScope  string
	findings []guardFinding
	notes    []guardNote
}

// guardFinding is one violation: where (a package path or file:line) and why.
type guardFinding struct {
	where string
	msg   string
}

// guardNote is one informational line: a label, where, and what.
type guardNote struct {
	label string
	where string
	msg   string
}

func (r *guardResult) add(where, msg string) {
	r.findings = append(r.findings, guardFinding{where, msg})
}

func (r *guardResult) note(label, where, msg string) {
	r.notes = append(r.notes, guardNote{label, where, msg})
}

func (r *guardResult) addImport(f *guardFile, imp guardImport, why string) {
	r.add(f.rel+":"+strconv.Itoa(imp.line), "imports "+imp.path+" ("+why+")")
}

// scan runs fn over every file of every package whose path satisfies inScope,
// production files only unless withTests, and counts the packages visited.
func (r *guardResult) scan(h *guardHost, inScope func(rel string) bool, withTests bool, fn func(pkg *guardPackage, f *guardFile)) {
	for _, pkg := range h.packages {
		if !inScope(pkg.rel) {
			continue
		}
		visited := false
		for _, f := range pkg.files {
			if f.test && !withTests {
				continue
			}
			visited = true
			fn(pkg, f)
		}
		if visited {
			r.checked++
		}
	}
}

func prefixScope(prefix string) func(string) bool {
	return func(rel string) bool { return underRel(rel, prefix) }
}

func evalH0(h *guardHost) guardResult {
	var r guardResult
	hasBinary := false
	for _, pkg := range h.packages {
		if !pkg.hasProduction() {
			continue
		}
		r.checked++
		s := segments(pkg.rel)
		if len(s) >= 2 && s[0] == "cmd" {
			hasBinary = true
		}
		if shapeOK(pkg.rel) {
			continue
		}
		what := "package outside the contract's roots"
		if len(s) > 0 && (s[0] == "internal" || s[0] == "pockets") {
			what = "package outside the contract's shape"
		}
		r.add(pkg.rel, what+" — home: "+suggestHome(h, pkg))
	}
	if !hasBinary {
		r.add("cmd/", "no production package under cmd/<binary>/ — a host has at least one composition root")
	}
	return r
}

// suggestHome names the slot an out-of-shape package belongs in: a host
// pocket's inbound/outbound when it imports a framework pocket, otherwise an
// app-local domain trio.
func suggestHome(h *guardHost, pkg *guardPackage) string {
	s := segments(pkg.rel)
	x := path.Base(h.module)
	if len(s) > 0 {
		x = s[len(s)-1]
	}
	if len(s) >= 2 && s[0] == "pockets" {
		home := "pockets/" + s[1] + "/{logic,inbound,outbound}"
		if len(s) > 2 {
			home += "/" + strings.Join(s[2:], "/")
		}
		return home
	}
	for _, f := range pkg.files {
		if f.test {
			continue
		}
		if f.ast.Name.Name == "main" {
			return "cmd/" + x
		}
		for _, imp := range f.imports {
			if imp.isFwPocket() && imp.pocket != "" {
				return "pockets/" + imp.pocket + "/{inbound,outbound}/" + x
			}
		}
	}
	return "internal/{logic,inbound,outbound}/domains/" + x
}

func evalH1(h *guardHost) guardResult {
	r := guardResult{noScope: "no internal/logic"}
	r.scan(h, prefixScope("internal/logic"), false, func(_ *guardPackage, f *guardFile) {
		for _, imp := range f.imports {
			switch {
			case imp.kind == impStdlib, imp.kind == impFwSDK:
			case imp.kind == impHost && (underRel(imp.rel, "sdk") || underRel(imp.rel, "internal/logic")):
			default:
				r.addImport(f, imp, "internal/logic imports only stdlib, sdk, the host's sdk/, and internal/logic")
			}
		}
	})
	return r
}

func evalH2(h *guardHost) guardResult {
	r := guardResult{noScope: "no internal/logic/domains"}
	r.scan(h, prefixScope("internal/logic/domains"), false, func(pkg *guardPackage, f *guardFile) {
		d := slotName(pkg.rel, "internal/logic/domains")
		for _, imp := range f.imports {
			if imp.kind != impHost {
				continue
			}
			switch {
			case underRel(imp.rel, "internal/logic/compositions"):
				r.addImport(f, imp, "a logic domain never imports a composition")
			case underRel(imp.rel, "internal/logic/domains") && slotName(imp.rel, "internal/logic/domains") != d:
				r.addImport(f, imp, "a logic domain never imports another logic domain")
			}
		}
	})
	return r
}

func evalH3(h *guardHost) guardResult {
	r := guardResult{noScope: "no internal/logic/compositions"}
	for _, pkg := range h.packages {
		if !underRel(pkg.rel, "internal/logic/compositions") || !pkg.hasProduction() {
			continue
		}
		r.checked++
		checkComposition(&r, pkg)
	}
	return r
}

// checkComposition applies H3's five checks to one composition package.
func checkComposition(r *guardResult, pkg *guardPackage) {
	types := map[string]*ast.TypeSpec{}
	var deps *ast.TypeSpec
	var depsFile *guardFile
	importsDomain := false

	for _, f := range pkg.files {
		if f.test {
			continue
		}
		for _, imp := range f.imports {
			switch {
			case imp.kind == impHost && underRel(imp.rel, "internal/logic/domains"):
				importsDomain = true
			case imp.kind == impHost && (underRel(imp.rel, "internal/outbound") || underRel(imp.rel, "integrations")),
				imp.kind == impFwIntegration, imp.kind == impFwPocketStore, imp.kind == impDriver:
				r.addImport(f, imp, "a composition holds no storage import")
			}
		}
		seen := map[token.Pos]bool{}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TypeSpec:
				types[n.Name.Name] = n
				if n.Name.Name == "Dependencies" {
					deps, depsFile = n, f
				}
				if strings.HasSuffix(n.Name.Name, "Repository") {
					r.add(posOf(f, n.Pos()), "declares type "+n.Name.Name+" (a composition holds no repository port)")
				}
				reportTypeNames(r, f, n.Type, seen)
			case *ast.Field:
				reportTypeNames(r, f, n.Type, seen)
			}
			return true
		})
	}

	if !importsDomain {
		r.add(pkg.rel, "imports no internal/logic/domains/<d> (a composition spans domains)")
	}
	if deps == nil {
		r.add(pkg.rel, "declares no Dependencies struct")
		return
	}
	st, ok := deps.Type.(*ast.StructType)
	if !ok || deps.Assign.IsValid() {
		r.add(posOf(depsFile, deps.Pos()), "Dependencies is not a struct declared in this package")
		return
	}
	for _, field := range st.Fields.List {
		where := posOf(depsFile, field.Pos())
		id, ok := field.Type.(*ast.Ident)
		if !ok || !strings.HasSuffix(id.Name, "Service") {
			r.add(where, "Dependencies field is not a *Service interface declared in this package")
			continue
		}
		spec := types[id.Name]
		if spec == nil {
			r.add(where, "Dependencies field "+id.Name+" is not declared in this package")
			continue
		}
		if spec.Assign.IsValid() {
			r.add(where, "Dependencies field "+id.Name+" is a type alias (declare the narrow interface here)")
			continue
		}
		it, ok := spec.Type.(*ast.InterfaceType)
		if !ok {
			r.add(where, "Dependencies field "+id.Name+" is not an interface")
			continue
		}
		if len(it.Methods.List) == 0 {
			r.add(where, "Dependencies field "+id.Name+" is an empty interface")
		}
	}
}

// reportTypeNames flags a Repository-suffixed name or a transaction handle
// anywhere inside a type expression: pointers, slices, maps, funcs, channels,
// generics, and qualified selectors alike. Nested field and method names are
// not types and are skipped; seen keeps an identifier reachable from both a
// TypeSpec and a Field from being reported twice.
func reportTypeNames(r *guardResult, f *guardFile, expr ast.Expr, seen map[token.Pos]bool) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(n ast.Node) bool {
		if field, ok := n.(*ast.Field); ok {
			reportTypeNames(r, f, field.Type, seen)
			return false
		}
		id, ok := n.(*ast.Ident)
		if !ok || seen[id.Pos()] {
			return true
		}
		seen[id.Pos()] = true
		switch {
		case strings.HasSuffix(id.Name, "Repository"):
			r.add(posOf(f, id.Pos()), "references "+id.Name+" (a composition holds no repository port)")
		case id.Name == "Transactor" || id.Name == "Tx":
			r.add(posOf(f, id.Pos()), "references "+id.Name+" (a composition holds no transaction handle)")
		}
		return true
	})
}

func posOf(f *guardFile, pos token.Pos) string {
	return f.rel + ":" + strconv.Itoa(f.fset.Position(pos).Line)
}

func evalH4(h *guardHost) guardResult {
	r := guardResult{noScope: "no internal/inbound"}
	r.scan(h, prefixScope("internal/inbound"), false, func(_ *guardPackage, f *guardFile) {
		for _, imp := range f.imports {
			if imp.kind != impHost || underRel(imp.rel, "workshop") {
				continue
			}
			name, layer := hostPocketOf(imp.rel)
			switch {
			case underRel(imp.rel, "internal/logic"), underRel(imp.rel, "internal/inbound"),
				underRel(imp.rel, "sdk"), underRel(imp.rel, "ui"),
				name != "" && (layer == "logic" || layer == "inbound"):
			case underRel(imp.rel, "internal/outbound"):
				r.addImport(f, imp, "inbound never reaches internal/outbound")
			case underRel(imp.rel, "integrations"):
				r.addImport(f, imp, "the host's integrations/ are named only by cmd")
			case name != "" && layer == "outbound":
				r.addImport(f, imp, "inbound never reaches a host pocket's outbound")
			default:
				r.addImport(f, imp, "outside inbound's reach")
			}
		}
	})
	return r
}

func evalH5(h *guardHost) guardResult {
	r := guardResult{noScope: "no internal/outbound/domains"}
	r.scan(h, prefixScope("internal/outbound/domains"), false, func(pkg *guardPackage, f *guardFile) {
		d := slotName(pkg.rel, "internal/outbound/domains")
		for _, imp := range f.imports {
			if imp.kind != impHost || underRel(imp.rel, "workshop") {
				continue
			}
			switch {
			case underRel(imp.rel, "internal/logic/domains/"+d), underRel(imp.rel, "internal/outbound/domains/"+d),
				underRel(imp.rel, "sdk"), underRel(imp.rel, "pockets"):
			case underRel(imp.rel, "internal/inbound"):
				r.addImport(f, imp, "outbound never imports inbound")
			case underRel(imp.rel, "integrations"):
				r.addImport(f, imp, "host integrations are named only by cmd")
			case underRel(imp.rel, "internal/logic/domains"):
				r.addImport(f, imp, "an adapter serves one domain: "+d)
			default:
				r.addImport(f, imp, "outside internal/outbound/domains/"+d+"'s host imports")
			}
		}
	})
	return r
}

func evalH6(h *guardHost) guardResult {
	r := guardResult{noScope: "no host pockets"}
	rims := map[string]bool{}
	var names []string
	r.scan(h, prefixScope("pockets"), true, func(pkg *guardPackage, f *guardFile) {
		name, layer := hostPocketOf(pkg.rel)
		if name == "" {
			return
		}
		if _, seen := rims[name]; !seen {
			rims[name] = false
			names = append(names, name)
		}
		for _, imp := range f.imports {
			if !f.test && imp.isFwPocket() {
				rims[name] = true
			}
			if imp.kind == impHost {
				other, _ := hostPocketOf(imp.rel)
				switch {
				case underRel(imp.rel, "internal"):
					if !f.test {
						r.addImport(f, imp, "a host pocket never imports internal/*")
					}
					continue
				case other != "" && other != name:
					if !f.test {
						r.addImport(f, imp, "two host pockets meet only in cmd")
					}
					continue
				}
			}
			if layer != "logic" {
				continue
			}
			switch {
			case imp.kind == impStdlib, imp.kind == impFwSDK, imp.kind == impFwPocketCore:
			case imp.kind == impHost && (underRel(imp.rel, "sdk") || underRel(imp.rel, "pockets/"+name+"/logic")):
			case f.test && imp.kind == impFwPocketStore && testStore(imp.path), f.test && imp.kind == impFwPocketViews:
			case f.test && (imp.kind == impThirdParty || imp.kind == impDriver || imp.kind == impViewTech):
			default:
				r.addImport(f, imp, "a host pocket's logic stays on stdlib, sdk, the host's sdk/, and framework pocket cores")
			}
		}
	})
	sort.Strings(names)
	for _, name := range names {
		if !rims[name] {
			r.add("pockets/"+name, "no production package imports a framework pocket (a host pocket extends one; otherwise it belongs under internal/)")
		}
	}
	return r
}

// testStore reports whether a framework pocket store import is one a host
// pocket's logic tests may use: stores/memory or stores/storetest.
func testStore(ip string) bool {
	rest := strings.TrimPrefix(ip, fwPockets+"/")
	parts := strings.Split(rest, "/")
	return len(parts) >= 3 && (parts[2] == "memory" || parts[2] == "storetest")
}

func evalH7(h *guardHost) guardResult {
	r := guardResult{noScope: "every package sits in an adapter-naming root"}
	exempt := func(rel string) bool {
		_, layer := hostPocketOf(rel)
		return underRel(rel, "cmd") || underRel(rel, "internal/outbound") || underRel(rel, "integrations") ||
			underRel(rel, "workshop") || layer == "outbound"
	}
	r.scan(h, func(rel string) bool { return !exempt(rel) }, false, func(_ *guardPackage, f *guardFile) {
		for _, imp := range f.imports {
			switch imp.kind {
			case impFwPocketStore:
				r.addImport(f, imp, "only cmd, outbound, integrations, and workshop name a pocket store")
			case impFwIntegration:
				r.addImport(f, imp, "only cmd, outbound, integrations, and workshop name a framework integration")
			case impDriver:
				r.addImport(f, imp, "only cmd, outbound, integrations, and workshop name a database driver")
			}
		}
	})
	return r
}

func evalH8(h *guardHost) guardResult {
	r := guardResult{noScope: "no internal/inbound or internal/outbound slots"}
	mirrors := []struct{ from, to string }{
		{"internal/inbound/domains", "internal/logic/domains"},
		{"internal/inbound/compositions", "internal/logic/compositions"},
		{"internal/outbound/domains", "internal/logic/domains"},
	}
	seen := map[string]bool{}
	for _, pkg := range h.packages {
		for _, m := range mirrors {
			name := slotName(pkg.rel, m.from)
			key := m.from + "/" + name
			if name == "" || seen[key] {
				continue
			}
			seen[key] = true
			r.checked++
			want := m.to + "/" + name
			if fi, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(want))); err != nil || !fi.IsDir() {
				r.add(key, "no "+want+" counterpart")
			}
		}
	}
	return r
}

func evalH9(h *guardHost) guardResult {
	r := guardResult{noScope: "no packages"}
	lax := "RowToStructByName" + "Lax"
	r.scan(h, func(string) bool { return true }, true, func(pkg *guardPackage, f *guardFile) {
		underlyingOK := underRel(pkg.rel, "integrations") || underRel(pkg.rel, "workshop")
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Underlying" && !underlyingOK {
					r.add(posOf(f, n.Pos()), "calls ."+"Underlying() (consume the transaction.Transactor seam)")
				}
			case *ast.Ident:
				if n.Name == lax {
					r.add(posOf(f, n.Pos()), "uses "+lax+" (strict scanning only)")
				}
			}
			return true
		})
	})
	return r
}

func evalH10(h *guardHost) guardResult {
	r := guardResult{noScope: "no cmd/"}
	roots := map[string]bool{}
	for _, pkg := range h.packages {
		s := segments(pkg.rel)
		if len(s) < 2 || s[0] != "cmd" {
			continue
		}
		for _, f := range pkg.files {
			if f.test {
				continue
			}
			for _, imp := range f.imports {
				if inwardImport(imp) {
					roots[s[1]] = true
				}
			}
		}
	}
	r.scan(h, prefixScope("cmd"), false, func(pkg *guardPackage, f *guardFile) {
		s := segments(pkg.rel)
		root := len(s) >= 2 && roots[s[1]]
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TypeSpec:
				if _, ok := n.Type.(*ast.InterfaceType); ok && root {
					r.add(posOf(f, n.Pos()), "declares interface "+n.Name.Name+" (a composition root declares no interface type)")
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING && isSQLLiteral(n.Value) {
					r.add(posOf(f, n.Pos()), "contains an SQL statement literal (cmd wires; SQL lives in an adapter)")
				}
			}
			return true
		})
	})
	return r
}

// inwardImport reports whether an import makes a cmd/<binary> tree a
// composition root rather than a client binary.
func inwardImport(imp guardImport) bool {
	switch imp.kind {
	case impHost:
		return underRel(imp.rel, "internal") || underRel(imp.rel, "pockets") || underRel(imp.rel, "integrations")
	case impFwPocketCore, impFwPocketStore, impFwPocketViews, impFwIntegration, impDriver:
		return true
	}
	return false
}

func isSQLLiteral(lit string) bool {
	s, err := strconv.Unquote(lit)
	if err != nil {
		return false
	}
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, verb := range sqlVerbs {
		rest, ok := strings.CutPrefix(s, verb)
		if ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' || rest[0] == '\r') {
			return true
		}
	}
	return false
}

func evalH11(h *guardHost) guardResult {
	r := guardResult{noScope: "no integrations/"}
	r.scan(h, prefixScope("integrations"), false, func(pkg *guardPackage, f *guardFile) {
		capability := slotName(pkg.rel, "integrations")
		for _, imp := range f.imports {
			switch {
			case imp.kind == impHost && underRel(imp.rel, "sdk"),
				imp.kind == impHost && underRel(imp.rel, "integrations/"+capability):
			case imp.kind == impHost:
				r.addImport(f, imp, "a host integration imports no host package but the host's sdk/")
			case imp.isFwPocket():
				r.addImport(f, imp, "a host integration imports no pocket of either scope")
			case imp.kind == impFwUI:
				r.addImport(f, imp, "a host integration is a technology adapter, not presentation")
			}
		}
	})
	for _, pkg := range h.packages {
		if underRel(pkg.rel, "cmd") || underRel(pkg.rel, "workshop") || underRel(pkg.rel, "integrations") {
			continue
		}
		for _, f := range pkg.files {
			for _, imp := range f.imports {
				if imp.kind == impHost && underRel(imp.rel, "integrations") {
					r.addImport(f, imp, "only cmd and workshop import a host integration")
				}
			}
		}
	}
	return r
}

func evalH12(h *guardHost) guardResult {
	r := guardResult{noScope: "no internal/ or pockets/"}
	inScope := func(rel string) bool { return underRel(rel, "internal") || underRel(rel, "pockets") }
	r.scan(h, inScope, true, func(pkg *guardPackage, f *guardFile) {
		_, layer := hostPocketOf(pkg.rel)
		outbound := underRel(pkg.rel, "internal/outbound") || layer == "outbound"
		inbound := underRel(pkg.rel, "internal/inbound") || layer == "inbound"
		for _, imp := range f.imports {
			switch imp.kind {
			case impStdlib, impFwSDK, impFwPocketCore, impFwPocketStore, impFwPocketViews, impFwIntegration, impFwUI:
			case impHost:
				if !f.test && underRel(imp.rel, "workshop") {
					r.addImport(f, imp, "production files never import the host's workshop/")
				}
			case impDriver:
				if !outbound {
					r.addImport(f, imp, "a database driver only under internal/outbound or a host pocket's outbound")
				}
			case impViewTech:
				if !inbound {
					r.addImport(f, imp, "view technology only under internal/inbound or a host pocket's inbound")
				}
			default:
				r.addImport(f, imp, "outside the fence")
			}
		}
	})
	return r
}

func evalH13(h *guardHost) guardResult {
	r := guardResult{noScope: "no sdk/"}
	r.scan(h, prefixScope("sdk"), false, func(_ *guardPackage, f *guardFile) {
		for _, imp := range f.imports {
			switch {
			case imp.kind == impStdlib, imp.kind == impFwSDK:
			case imp.kind == impHost && underRel(imp.rel, "sdk"):
			default:
				r.addImport(f, imp, "the host's sdk/ imports only stdlib, sdk, and itself")
			}
		}
	})
	return r
}

func evalH14(h *guardHost) guardResult {
	r := guardResult{noScope: "no ui/"}
	r.scan(h, prefixScope("ui"), false, func(_ *guardPackage, f *guardFile) {
		for _, imp := range f.imports {
			switch {
			case imp.kind == impStdlib, imp.kind == impFwSDK, imp.kind == impFwUI, imp.kind == impViewTech:
			case imp.kind == impHost && (underRel(imp.rel, "sdk") || underRel(imp.rel, "ui")):
			default:
				r.addImport(f, imp, "the host's ui/ imports only stdlib, sdk, framework ui, view technology, the host's sdk/, and itself")
			}
		}
	})
	return r
}

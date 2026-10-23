package commands

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The canonical sentences of examples/README.md §3b, byte for byte. They are
// the single source for `guard --list --posture` and for the rule line under
// each finding block; TestGuardListMatchesContract fails the moment the two
// drift.
const (
	sentenceP0 = "The host resolves its runtime mode from the process environment only: through `environment.LoadEnvForMode`, `environment.ParseMode`, or an `environment.Mode`-typed env field tagged `required:\"true\"` with no `default:`; scanned production files call `environment.LoadEnv(`/`LoadPath(` nowhere; `ModeDevelopment`/`ModeProduction` appear only as an operand of `==`/`!=` or a `case`."
	sentenceP1 = "For every recognized `Config{` literal or `var x <pkg>.Config` in a scanned function, that function names `RuntimeMode:` or `x.RuntimeMode =`; every `authentication.New` call passes a mode argument and every `authorization.New` call passes `WithRuntimeMode(`. (The guard proves the mode is NAMED; the pocket proves it is honored.)"
	sentenceP2 = "`web.TrustProxies(` is the first argument of the first `.Use(` call on the handler the composition root passes to `web.Run`/`HTTPServer`."
	sentenceP3 = "`web.SecurityHeaders(` or `web.SecurityHeadersWithConfig(` appears in a `.Use(` call on that handler."
	sentenceP4 = "A host that builds a `web.ServerConfig` calls its `ValidateProduction()`."
	sentenceP5 = "Every recognized acknowledgement in scanned files is a boolean literal (`true` or `false`), never another expression; an option-form acknowledgement is a direct argument of the `New(` call; `--posture` prints one `ack` line (file:line, flag) per `true`, and `ack (conditional)` when it sits under `if`/`switch` — informational, exit code unchanged."
	sentenceP6 = "A host that mounts authentication builds its keys through `authentication.NewKeys(` with a `KeysInput` filled by `environment.ParseEnvTags` in a scanned file; `--posture` prints one informational `rand` line per `crypto/rand` use in scanned files so a review sees any other key minting (no ban: non-auth keys such as auth-cms's cursor key, `documents.go:43`, are legitimate)."
	sentenceP7 = "`.env.example`, when present, carries no **uncommented** `sslmode=disable`/`DB_SSLMODE=disable` line; mentions the host's mode key ONLY in a comment (`# APP_RUNTIME_MODE=…` — an uncommented assignment, even empty, is a finding, because `LoadEnvForMode` refuses it); and names `TRUSTED_PROXY_COUNT` and `PUBLIC_BASE_URL`."
	sentenceP8 = "Every `cms.Config` literal in scanned files names `AdminMiddleware` or `AdminUngatedAcknowledged` (the flip is not a boot refusal, so downstream hosts need the pre-deploy signal; G31 covers only this repo)."
	sentenceP9 = "For every store the host mounts, every store migration file has a byte-identical copy in the host ledger; an immutability trigger file may be declined by omitting it, and the posture report says so."
)

// The no-scope reasons the P rules share.
const (
	postureNoSurface = "no recognized posture surface imported"
	postureNoServe   = "no web.Run or HTTPServer call"
)

// The Recognized posture surfaces of examples/README.md §3b. Rule text and
// implementation name the same identifiers; a new surface is a list edit
// shipped in a workshop minor. Identifiers are matched as AST text, never by
// importing the packages that declare them (G11).
var (
	postureAuthentication = fwPockets + "/authentication"
	postureAuthorization  = fwPockets + "/authorization"
	postureCMS            = fwPockets + "/cms"
	posturePgxdb          = fwIntegrations + "/datastores/pgxdb"
	postureTurso          = fwIntegrations + "/datastores/turso"
	postureOtel           = fwIntegrations + "/tracing/otel"

	// postureSurfaces are the packages carrying a surface. A scanned file that
	// imports one of them, or a package beneath it, puts P0, P1, P5, P6 and P8
	// in scope.
	postureSurfaces = []string{postureAuthentication, postureAuthorization, postureCMS, posturePgxdb, postureTurso, postureOtel}

	// postureModeRecords are the packages whose Config carries RuntimeMode (P1).
	postureModeRecords = []string{postureCMS, posturePgxdb, postureTurso, postureOtel}

	// postureAcknowledgements are the acknowledgement field names (P5).
	postureAcknowledgements = map[string]bool{
		"AuditDisabledAcknowledged":      true,
		"SingleFactorAcknowledged":       true,
		"OpportunisticTLSAcknowledged":   true,
		"DeliveryJobsAcknowledged":       true,
		"DeliveryEphemeralAcknowledged":  true,
		"AdminUngatedAcknowledged":       true,
		"TLSUnverifiedAcknowledged":      true,
		"TLSDisabledAcknowledged":        true,
		"AppendOnlyWritableAcknowledged": true,
	}

	// postureAckOptionPkgs are the packages whose New takes the option-form
	// acknowledgement postureAckOption (P5).
	postureAckOptionPkgs = []string{postureAuthentication, postureAuthorization}
	postureAckOption     = "WithAuditDisabledAcknowledged"

	sdkWeb         = fwSDK + "/pkg/web"
	sdkEnvironment = fwSDK + "/pkg/environment"
)

// The P7 shapes: the host-root file it reads, and the two keys it must name.
var (
	envExampleFile  = ".env.example"
	envExampleNames = []string{"TRUSTED_PROXY_COUNT", "PUBLIC_BASE_URL"}
	sslmodeDisable  = "sslmode=disable"
)

// The P9 shapes: the directory a store module ships its migrations in, the
// suffix of an immutability trigger file a host may decline by omitting it, the
// fix every finding names, and the bound on one `go list -m`.
const (
	storeMigrationsDir = "migrations"
	immutableSuffix    = "_immutable.sql"
	ledgerReexport     = "re-export: copy the store file into the ledger; never edit a copy"
	storeListTimeout   = 2 * time.Minute
)

// postureRules is the P table in id order; `guard --posture` appends it to
// guardRules.
var postureRules = []guardRule{
	{"P0", sentenceP0, evalP0},
	{"P1", sentenceP1, evalP1},
	{"P2", sentenceP2, evalP2},
	{"P3", sentenceP3, evalP3},
	{"P4", sentenceP4, evalP4},
	{"P5", sentenceP5, evalP5},
	{"P6", sentenceP6, evalP6},
	{"P7", sentenceP7, evalP7},
	{"P8", sentenceP8, evalP8},
	{"P9", sentenceP9, evalP9},
}

// postureFile is one production file in the P rules' scanned trees, with the
// local name under which it imports each path.
type postureFile struct {
	*guardFile
	pkg   *guardPackage
	names map[string]string
}

// funcRef is a package-level function and the file that declares it.
type funcRef struct {
	decl *ast.FuncDecl
	file *postureFile
}

// serveSite is one web.Run or HTTPServer call and, when the handler it serves
// resolves to a router variable, that router's .Use( calls in source order.
type serveSite struct {
	file     *postureFile
	call     *ast.CallExpr
	verb     string
	resolved bool
	uses     []*ast.CallExpr
	useFile  *postureFile
}

// postureScan returns the production files the P rules read: cmd/**,
// internal/outbound/**, the host's integrations/**, a host pocket's
// outbound/**, and workshop/** main packages.
func postureScan(h *guardHost) []*postureFile {
	var out []*postureFile
	for _, pkg := range h.packages {
		if !postureScope(pkg) {
			continue
		}
		for _, f := range pkg.files {
			if f.test {
				continue
			}
			names := map[string]string{}
			for _, spec := range f.ast.Imports {
				ip, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					continue
				}
				name := path0(ip)
				if spec.Name != nil {
					name = spec.Name.Name
				}
				if name == "_" || name == "." {
					continue
				}
				names[ip] = name
			}
			out = append(out, &postureFile{guardFile: f, pkg: pkg, names: names})
		}
	}
	return out
}

// postureScope reports whether a package sits in the P rules' scanned trees.
func postureScope(pkg *guardPackage) bool {
	_, layer := hostPocketOf(pkg.rel)
	switch {
	case underRel(pkg.rel, "cmd"), underRel(pkg.rel, "internal/outbound"), underRel(pkg.rel, "integrations"), layer == "outbound":
		return true
	case underRel(pkg.rel, "workshop"):
		for _, f := range pkg.files {
			if !f.test && f.ast.Name.Name == "main" {
				return true
			}
		}
	}
	return false
}

// path0 is the default package name of an import path: its last element.
func path0(ip string) string {
	if i := strings.LastIndex(ip, "/"); i >= 0 {
		return ip[i+1:]
	}
	return ip
}

// name returns the local name under which f imports ip, or "".
func (f *postureFile) name(ip string) string {
	return f.names[ip]
}

// importsSurface reports whether any scanned file imports a posture surface.
func importsSurface(files []*postureFile) bool {
	for _, f := range files {
		for ip := range f.names {
			if matchesAny(ip, postureSurfaces) {
				return true
			}
		}
	}
	return false
}

// importsPath reports whether any scanned file imports ip itself.
func importsPath(files []*postureFile, ip string) bool {
	for _, f := range files {
		if f.name(ip) != "" {
			return true
		}
	}
	return false
}

func packageCount(files []*postureFile) int {
	seen := map[*guardPackage]bool{}
	for _, f := range files {
		seen[f.pkg] = true
	}
	return len(seen)
}

// funcDecls returns the function declarations of f that have a body.
func funcDecls(f *postureFile) []*ast.FuncDecl {
	var out []*ast.FuncDecl
	for _, decl := range f.ast.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
			out = append(out, fd)
		}
	}
	return out
}

// selectorOn returns the selected name when expr is x.<name> for a non-empty
// package name x.
func selectorOn(expr ast.Expr, x string) (string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || x == "" {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != x {
		return "", false
	}
	return sel.Sel.Name, true
}

// isCallTo reports whether expr is a call to x.<one of names>.
func isCallTo(expr ast.Expr, x string, names ...string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := selectorOn(call.Fun, x)
	if !ok {
		return false
	}
	for _, n := range names {
		if sel == n {
			return true
		}
	}
	return false
}

// walkStack visits every node under root depth-first, passing its ancestors,
// outermost first.
func walkStack(root ast.Node, fn func(n ast.Node, stack []ast.Node)) {
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		fn(n, stack)
		stack = append(stack, n)
		return true
	})
}

// parentOf returns the nearest ancestor that is not a parenthesis, and the
// child of it on the path to n.
func parentOf(n ast.Node, stack []ast.Node) (parent, child ast.Node) {
	parent, child, _ = parentAt(n, stack)
	return parent, child
}

// parentAt is parentOf plus the parent's index in stack (-1 for none).
func parentAt(n ast.Node, stack []ast.Node) (parent, child ast.Node, at int) {
	child = n
	for i := len(stack) - 1; i >= 0; i-- {
		if _, ok := stack[i].(*ast.ParenExpr); ok {
			child = stack[i]
			continue
		}
		return stack[i], child, i
	}
	return nil, child, -1
}

// comparisonOperand reports whether n is an operand of == or != or a case
// expression.
func comparisonOperand(n ast.Node, stack []ast.Node) bool {
	parent, child := parentOf(n, stack)
	switch p := parent.(type) {
	case *ast.BinaryExpr:
		return p.Op == token.EQL || p.Op == token.NEQ
	case *ast.CaseClause:
		for _, e := range p.List {
			if ast.Node(e) == child {
				return true
			}
		}
	}
	return false
}

// conditional reports whether n sits in the body of an if or a switch (never
// its init statement or condition).
func conditional(n ast.Node, stack []ast.Node) bool {
	for i, anc := range stack {
		child := n
		if i+1 < len(stack) {
			child = stack[i+1]
		}
		switch s := anc.(type) {
		case *ast.IfStmt:
			if child == ast.Node(s.Body) || (s.Else != nil && child == ast.Node(s.Else)) {
				return true
			}
		case *ast.SwitchStmt:
			if child == ast.Node(s.Body) {
				return true
			}
		case *ast.TypeSwitchStmt:
			if child == ast.Node(s.Body) {
				return true
			}
		}
	}
	return false
}

// zeroMode reports whether expr is the empty mode: "" or a conversion of it.
func zeroMode(expr ast.Expr) bool {
	switch e := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING && (e.Value == `""` || e.Value == "``")
	case *ast.CallExpr:
		return len(e.Args) == 1 && zeroMode(e.Args[0])
	}
	return false
}

// boolLiteral returns the literal's value when expr is true or false.
func boolLiteral(expr ast.Expr) (value, ok bool) {
	id, isIdent := ast.Unparen(expr).(*ast.Ident)
	if !isIdent || (id.Name != "true" && id.Name != "false") {
		return false, false
	}
	return id.Name == "true", true
}

// literalVar names the variable a composite literal is assigned or declared
// into (through an &), or "" when it is passed directly.
func literalVar(lit ast.Node, stack []ast.Node) string {
	parent, child, at := parentAt(lit, stack)
	if u, ok := parent.(*ast.UnaryExpr); ok && u.Op == token.AND {
		parent, child = parentOf(u, stack[:at])
	}
	switch p := parent.(type) {
	case *ast.AssignStmt:
		if len(p.Lhs) != len(p.Rhs) {
			return ""
		}
		for i, rhs := range p.Rhs {
			if ast.Node(rhs) == child {
				if id, ok := p.Lhs[i].(*ast.Ident); ok {
					return id.Name
				}
			}
		}
	case *ast.ValueSpec:
		for i, v := range p.Values {
			if ast.Node(v) == child && i < len(p.Names) {
				return p.Names[i].Name
			}
		}
	}
	return ""
}

// assignsField reports whether body assigns <name>.<field> anywhere.
func assignsField(body ast.Node, name string, fields ...string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || found {
			return !found
		}
		for _, lhs := range as.Lhs {
			sel, ok := selectorOn(lhs, name)
			if !ok {
				continue
			}
			for _, field := range fields {
				if sel == field {
					found = true
				}
			}
		}
		return true
	})
	return found
}

// recordType returns the local package name when expr is <record>.Config for
// one of the given local names.
func recordType(expr ast.Expr, records map[string]bool) (string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Config" {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || !records[id.Name] {
		return "", false
	}
	return id.Name, true
}

// litKey returns the value of the key named field in a keyed composite
// literal.
func litKey(lit *ast.CompositeLit, field string) (ast.Expr, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == field {
			return kv.Value, true
		}
	}
	return nil, false
}

// modeEnvField returns the env key of a struct field typed environment.Mode
// that carries an env tag, with its parsed tag.
func modeEnvField(field *ast.Field, env string) (string, reflect.StructTag, bool) {
	if sel, ok := selectorOn(field.Type, env); !ok || sel != "Mode" || field.Tag == nil {
		return "", "", false
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return "", "", false
	}
	tag := reflect.StructTag(raw)
	key, ok := tag.Lookup("env")
	key, _, _ = strings.Cut(key, ",")
	if !ok || key == "" {
		return "", "", false
	}
	return key, tag, true
}

// stringConsts collects the package-level string constants of the scanned
// files, so a mode key held in a constant still resolves.
func stringConsts(files []*postureFile) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		for _, decl := range f.ast.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, v := range vs.Values {
					if s, ok := stringValue(v, nil); ok && i < len(vs.Names) {
						out[vs.Names[i].Name] = s
					}
				}
			}
		}
	}
	return out
}

// stringValue resolves a string literal, or an identifier naming one of
// consts.
func stringValue(expr ast.Expr, consts map[string]string) (string, bool) {
	switch e := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil && s != ""
	case *ast.Ident:
		s, ok := consts[e.Name]
		return s, ok
	}
	return "", false
}

// postureModeKeys returns the environment keys the host reads its mode from:
// the argument of LoadEnvForMode, of ParseMode(os.Getenv(…)), and the env tag
// of an environment.Mode field.
func postureModeKeys(files []*postureFile) []string {
	consts := stringConsts(files)
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, f := range files {
		env := f.name(sdkEnvironment)
		if env == "" {
			continue
		}
		osName := f.name("os")
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				sel, ok := selectorOn(n.Fun, env)
				if !ok || len(n.Args) != 1 {
					return true
				}
				switch {
				case sel == "LoadEnvForMode":
					if k, ok := stringValue(n.Args[0], consts); ok {
						add(k)
					}
				case sel == "ParseMode":
					if get, ok := n.Args[0].(*ast.CallExpr); ok && isCallTo(get, osName, "Getenv") && len(get.Args) == 1 {
						if k, ok := stringValue(get.Args[0], consts); ok {
							add(k)
						}
					}
				}
			case *ast.Field:
				if k, _, ok := modeEnvField(n, env); ok {
					add(k)
				}
			}
			return true
		})
	}
	return keys
}

func evalP0(h *guardHost) guardResult {
	r := guardResult{noScope: postureNoSurface}
	files := postureScan(h)
	if !importsSurface(files) {
		return r
	}
	r.checked = packageCount(files)
	resolved := false
	for _, f := range files {
		env := f.name(sdkEnvironment)
		if env == "" {
			continue
		}
		walkStack(f.ast, func(n ast.Node, stack []ast.Node) {
			switch n := n.(type) {
			case *ast.CallExpr:
				switch sel, _ := selectorOn(n.Fun, env); sel {
				case "LoadEnvForMode", "ParseMode":
					resolved = true
				case "LoadEnv", "LoadPath":
					r.add(posOf(f.guardFile, n.Pos()), "calls environment."+sel+" (it reads .env in every mode; resolve the mode with environment.LoadEnvForMode)")
				}
			case *ast.SelectorExpr:
				sel, ok := selectorOn(n, env)
				if ok && (sel == "ModeDevelopment" || sel == "ModeProduction") && !comparisonOperand(n, stack) {
					r.add(posOf(f.guardFile, n.Pos()), "uses environment."+sel+" outside a comparison (the mode comes from the process environment, never from code)")
				}
			case *ast.Field:
				key, tag, ok := modeEnvField(n, env)
				if !ok {
					return
				}
				if _, hasDefault := tag.Lookup("default"); tag.Get("required") == "true" && !hasDefault {
					resolved = true
					return
				}
				r.add(posOf(f.guardFile, n.Pos()), "environment.Mode field "+key+" must be tagged required:\"true\" with no default: (a default chooses the posture for the host)")
			}
		})
	}
	if !resolved {
		r.add("cmd/", "resolves no runtime mode from the process environment (environment.LoadEnvForMode, environment.ParseMode, or a required environment.Mode env field)")
	}
	return r
}

func evalP1(h *guardHost) guardResult {
	r := guardResult{noScope: postureNoSurface, unit: "site"}
	files := postureScan(h)
	if !importsSurface(files) {
		return r
	}
	r.noScope = "no recognized Config, authentication.New, or authorization.New"
	for _, f := range files {
		records := map[string]bool{}
		for _, ip := range postureModeRecords {
			if n := f.name(ip); n != "" {
				records[n] = true
			}
		}
		authn, authz := f.name(postureAuthentication), f.name(postureAuthorization)
		for _, fd := range funcDecls(f) {
			walkStack(fd.Body, func(n ast.Node, stack []ast.Node) {
				switch n := n.(type) {
				case *ast.CompositeLit:
					pkg, ok := recordType(n.Type, records)
					if !ok {
						return
					}
					r.checked++
					if v, named := litKey(n, "RuntimeMode"); named && !zeroMode(v) {
						return
					}
					if name := literalVar(n, stack); name != "" && assignsField(fd.Body, name, "RuntimeMode") {
						return
					}
					r.add(posOf(f.guardFile, n.Pos()), pkg+".Config names no RuntimeMode (set RuntimeMode: mode, or x.RuntimeMode = mode in this function)")
				case *ast.ValueSpec:
					pkg, ok := recordType(n.Type, records)
					if !ok || len(n.Values) > 0 {
						return
					}
					for _, id := range n.Names {
						r.checked++
						if !assignsField(fd.Body, id.Name, "RuntimeMode") {
							r.add(posOf(f.guardFile, id.Pos()), "var "+id.Name+" "+pkg+".Config: this function never sets "+id.Name+".RuntimeMode")
						}
					}
				case *ast.CallExpr:
					if sel, ok := selectorOn(n.Fun, authn); ok && sel == "New" {
						r.checked++
						if len(n.Args) < 3 || zeroMode(n.Args[2]) {
							r.add(posOf(f.guardFile, n.Pos()), authn+".New passes no runtime mode (its third argument)")
						}
					}
					if sel, ok := selectorOn(n.Fun, authz); ok && sel == "New" {
						r.checked++
						if !passesRuntimeMode(fd.Body, n, authz) {
							r.add(posOf(f.guardFile, n.Pos()), authz+".New passes no "+authz+".WithRuntimeMode(mode)")
						}
					}
				}
			})
		}
	}
	return r
}

// passesRuntimeMode reports whether an authorization.New call passes
// WithRuntimeMode with a non-zero mode, directly or — when it spreads an
// option slice — anywhere in the calling function.
func passesRuntimeMode(body ast.Node, call *ast.CallExpr, authz string) bool {
	for _, arg := range call.Args {
		if isCallTo(arg, authz, "WithRuntimeMode") {
			opt := arg.(*ast.CallExpr)
			return len(opt.Args) == 1 && !zeroMode(opt.Args[0])
		}
	}
	if !call.Ellipsis.IsValid() {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && isCallTo(c, authz, "WithRuntimeMode") && len(c.Args) == 1 && !zeroMode(c.Args[0]) {
			found = true
		}
		return !found
	})
	return found
}

// postureServeSites finds every web.Run and HTTPServer call in the scanned
// files and follows the handler each serves back to a router variable: through
// local assignments and, when that variable is the result of a package-level
// function, into that function's return statements.
func postureServeSites(h *guardHost) []serveSite {
	files := postureScan(h)
	byPkg := map[*guardPackage][]*postureFile{}
	var pkgs []*guardPackage
	for _, f := range files {
		if _, ok := byPkg[f.pkg]; !ok {
			pkgs = append(pkgs, f.pkg)
		}
		byPkg[f.pkg] = append(byPkg[f.pkg], f)
	}

	var sites []serveSite
	for _, pkg := range pkgs {
		funcs := map[string]funcRef{}
		for _, f := range byPkg[pkg] {
			for _, fd := range funcDecls(f) {
				if fd.Recv == nil {
					funcs[fd.Name.Name] = funcRef{fd, f}
				}
			}
		}
		for _, f := range byPkg[pkg] {
			web := f.name(sdkWeb)
			if web == "" {
				continue
			}
			for _, fd := range funcDecls(f) {
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					var handler ast.Expr
					var verb string
					if sel, ok := selectorOn(call.Fun, web); ok && sel == "Run" && len(call.Args) >= 2 {
						handler, verb = call.Args[1], web+".Run"
					} else if s, ok := call.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "HTTPServer" && len(call.Args) == 1 {
						handler, verb = call.Args[0], "HTTPServer"
					} else {
						return true
					}
					site := serveSite{file: f, call: call, verb: verb}
					if ref, name := resolveHandler(funcs, funcRef{fd, f}, handler, 0); name != "" {
						site.resolved, site.useFile = true, ref.file
						site.uses = usesOn(ref.decl.Body, name)
					}
					sites = append(sites, site)
					return true
				})
			}
		}
	}
	return sites
}

// resolveHandler follows expr within ref to the router variable it names.
func resolveHandler(funcs map[string]funcRef, ref funcRef, expr ast.Expr, depth int) (funcRef, string) {
	if depth > 4 {
		return funcRef{}, ""
	}
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		if rhs, idx := assignmentOf(ref.decl.Body, e.Name); rhs != nil {
			if callee, ok := localCallee(funcs, rhs); ok {
				if ret := returnedExpr(callee.decl, idx); ret != nil {
					return resolveHandler(funcs, callee, ret, depth+1)
				}
			}
		}
		return ref, e.Name
	case *ast.CallExpr:
		if callee, ok := localCallee(funcs, e); ok {
			if ret := returnedExpr(callee.decl, 0); ret != nil {
				return resolveHandler(funcs, callee, ret, depth+1)
			}
		}
	}
	return funcRef{}, ""
}

// assignmentOf returns the first expression assigned to name in body and the
// result index it takes from a multi-value right-hand side.
func assignmentOf(body ast.Node, name string) (ast.Expr, int) {
	var rhs ast.Expr
	idx := 0
	ast.Inspect(body, func(n ast.Node) bool {
		if rhs != nil {
			return false
		}
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != name {
					continue
				}
				switch {
				case len(s.Rhs) == len(s.Lhs):
					rhs = s.Rhs[i]
				case len(s.Rhs) == 1:
					rhs, idx = s.Rhs[0], i
				}
				return false
			}
		case *ast.ValueSpec:
			for i, id := range s.Names {
				if id.Name != name {
					continue
				}
				switch {
				case len(s.Values) == len(s.Names):
					rhs = s.Values[i]
				case len(s.Values) == 1:
					rhs, idx = s.Values[0], i
				}
				return false
			}
		}
		return true
	})
	return rhs, idx
}

// localCallee returns the package-level function expr calls, if any.
func localCallee(funcs map[string]funcRef, expr ast.Expr) (funcRef, bool) {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok {
		return funcRef{}, false
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return funcRef{}, false
	}
	ref, ok := funcs[id.Name]
	return ref, ok
}

// returnedExpr returns result idx of the first return statement of fd that
// returns something other than nil there. Returns inside closures are not
// fd's.
func returnedExpr(fd *ast.FuncDecl, idx int) ast.Expr {
	var out ast.Expr
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if out != nil {
			return false
		}
		switch s := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if idx < len(s.Results) {
				if id, ok := s.Results[idx].(*ast.Ident); !ok || id.Name != "nil" {
					out = s.Results[idx]
				}
			}
		}
		return true
	})
	return out
}

// usesOn returns every <name>.Use( call in body, closures included, in source
// order.
func usesOn(body ast.Node, name string) []*ast.CallExpr {
	var uses []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := selectorOn(call.Fun, name); ok && sel == "Use" {
				uses = append(uses, call)
			}
		}
		return true
	})
	return uses
}

// unresolved records the finding P2 and P3 share for a handler they cannot
// follow, and reports whether it did.
func (s serveSite) unresolved(r *guardResult) bool {
	where := posOf(s.file.guardFile, s.call.Pos())
	switch {
	case !s.resolved:
		r.add(where, "the handler passed to "+s.verb+" does not resolve to a router variable this guard can follow to its .Use( calls (pass the router itself, wrapped in nothing)")
	case len(s.uses) == 0:
		r.add(where, "the handler passed to "+s.verb+" has no .Use( call")
	default:
		return false
	}
	return true
}

func evalP2(h *guardHost) guardResult {
	r := guardResult{noScope: postureNoServe, unit: "handler"}
	for _, s := range postureServeSites(h) {
		r.checked++
		if s.unresolved(&r) {
			continue
		}
		first := s.uses[0]
		if len(first.Args) == 0 || !isCallTo(first.Args[0], s.useFile.name(sdkWeb), "TrustProxies") {
			r.add(posOf(s.useFile.guardFile, first.Pos()), "the first .Use( on the served handler does not start with web.TrustProxies( (it runs outermost, so every later layer sees the resolved client IP)")
		}
	}
	return r
}

func evalP3(h *guardHost) guardResult {
	r := guardResult{noScope: postureNoServe, unit: "handler"}
	for _, s := range postureServeSites(h) {
		r.checked++
		if s.unresolved(&r) {
			continue
		}
		web := s.useFile.name(sdkWeb)
		found := false
		for _, use := range s.uses {
			for _, arg := range use.Args {
				if isCallTo(arg, web, "SecurityHeaders", "SecurityHeadersWithConfig") {
					found = true
				}
			}
		}
		if !found {
			r.add(posOf(s.file.guardFile, s.call.Pos()), "no .Use( on the handler passed to "+s.verb+" installs web.SecurityHeaders() or web.SecurityHeadersWithConfig(…)")
		}
	}
	return r
}

func evalP4(h *guardHost) guardResult {
	r := guardResult{noScope: "no web.ServerConfig built", unit: "site"}
	var sites []string
	validated := false
	for _, f := range postureScan(h) {
		web := f.name(sdkWeb)
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				if sel, ok := selectorOn(n.Type, web); ok && sel == "ServerConfig" {
					sites = append(sites, posOf(f.guardFile, n.Pos()))
				}
			case *ast.ValueSpec:
				if sel, ok := selectorOn(n.Type, web); ok && sel == "ServerConfig" && len(n.Values) == 0 {
					sites = append(sites, posOf(f.guardFile, n.Pos()))
				}
			case *ast.CallExpr:
				if s, ok := n.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "ValidateProduction" && len(n.Args) == 0 {
					validated = true
				}
			}
			return true
		})
	}
	r.checked = len(sites)
	if validated {
		return r
	}
	for _, where := range sites {
		r.add(where, "builds a web.ServerConfig, and the host never calls its ValidateProduction() (call it under mode.IsProduction())")
	}
	return r
}

func evalP5(h *guardHost) guardResult {
	r := guardResult{noScope: postureNoSurface, unit: "acknowledgement"}
	files := postureScan(h)
	if !importsSurface(files) {
		return r
	}
	r.noScope = "no acknowledgement"
	for _, f := range files {
		var optionPkgs []string
		for _, ip := range postureAckOptionPkgs {
			if n := f.name(ip); n != "" {
				optionPkgs = append(optionPkgs, n)
			}
		}
		walkStack(f.ast, func(n ast.Node, stack []ast.Node) {
			switch n := n.(type) {
			case *ast.KeyValueExpr:
				id, ok := n.Key.(*ast.Ident)
				if parent, _ := parentOf(n, stack); !ok || !postureAcknowledgements[id.Name] || !isLit(parent) {
					return
				}
				acknowledge(&r, f, n, stack, id.Name, n.Value)
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || !postureAcknowledgements[sel.Sel.Name] {
						continue
					}
					var value ast.Expr
					if len(n.Rhs) == len(n.Lhs) {
						value = n.Rhs[i]
					}
					acknowledge(&r, f, lhs, stack, sel.Sel.Name, value)
				}
			case *ast.CallExpr:
				for _, pkg := range optionPkgs {
					if !isCallTo(n, pkg, postureAckOption) {
						continue
					}
					r.checked++
					where := posOf(f.guardFile, n.Pos())
					parent, child := parentOf(n, stack)
					if call, ok := parent.(*ast.CallExpr); ok && isCallTo(call, pkg, "New") && ast.Node(call.Fun) != child {
						r.note(ackLabel(n, stack), where, pkg+"."+postureAckOption+"()")
						continue
					}
					r.add(where, pkg+"."+postureAckOption+"() is not a direct argument of "+pkg+".New( (an option-form acknowledgement is written at the call it waives)")
				}
			}
		})
	}
	return r
}

func isLit(n ast.Node) bool {
	_, ok := n.(*ast.CompositeLit)
	return ok
}

// acknowledge checks one field-form acknowledgement and records its ack line.
func acknowledge(r *guardResult, f *postureFile, at ast.Node, stack []ast.Node, flag string, value ast.Expr) {
	r.checked++
	where := posOf(f.guardFile, at.Pos())
	set, ok := boolLiteral(value)
	if value == nil || !ok {
		r.add(where, flag+" is set from an expression, not a boolean literal (an acknowledgement is a wiring assertion written in the composition root)")
		return
	}
	if set {
		r.note(ackLabel(at, stack), where, flag)
	}
}

func ackLabel(n ast.Node, stack []ast.Node) string {
	if conditional(n, stack) {
		return "ack (conditional)"
	}
	return "ack"
}

func evalP6(h *guardHost) guardResult {
	r := guardResult{noScope: postureNoSurface, unit: "mount"}
	files := postureScan(h)
	for _, f := range files {
		rnd := f.name("crypto/rand")
		ast.Inspect(f.ast, func(n ast.Node) bool {
			if sel, ok := selectorOn(asExpr(n), rnd); ok {
				r.note("rand", posOf(f.guardFile, n.Pos()), "crypto/rand."+sel)
			}
			return true
		})
	}
	if !importsSurface(files) {
		return r
	}
	r.noScope = "no authentication.New call"

	var mounts, newKeys []string
	filled := false
	for _, f := range files {
		authn, env := f.name(postureAuthentication), f.name(sdkEnvironment)
		if authn == "" {
			continue
		}
		for _, fd := range funcDecls(f) {
			// The KeysInput variables this function declares; one counts as
			// filled once it goes through ParseEnvTags.
			inputs := map[string]bool{}
			walkStack(fd.Body, func(n ast.Node, stack []ast.Node) {
				switch n := n.(type) {
				case *ast.CallExpr:
					switch sel, _ := selectorOn(n.Fun, authn); sel {
					case "New":
						mounts = append(mounts, posOf(f.guardFile, n.Pos()))
					case "NewKeys":
						newKeys = append(newKeys, posOf(f.guardFile, n.Pos()))
					}
				case *ast.ValueSpec:
					if sel, ok := selectorOn(n.Type, authn); ok && sel == "KeysInput" {
						for _, id := range n.Names {
							inputs[id.Name] = true
						}
					}
				case *ast.CompositeLit:
					if sel, ok := selectorOn(n.Type, authn); ok && sel == "KeysInput" {
						if name := literalVar(n, stack); name != "" {
							inputs[name] = true
						}
					}
				}
			})
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isCallTo(call, env, "ParseEnvTags") || len(call.Args) != 2 {
					return true
				}
				if u, ok := call.Args[1].(*ast.UnaryExpr); ok && u.Op == token.AND {
					if id, ok := u.X.(*ast.Ident); ok && inputs[id.Name] {
						filled = true
					}
				}
				return true
			})
		}
	}
	r.checked = len(mounts)
	if len(mounts) == 0 {
		return r
	}
	switch {
	case len(newKeys) == 0:
		for _, where := range mounts {
			r.add(where, "mounts authentication without authentication.NewKeys (decode the keys from a KeysInput)")
		}
	case !filled:
		for _, where := range newKeys {
			r.add(where, "no authentication.KeysInput in a scanned file is filled by environment.ParseEnvTags")
		}
	}
	return r
}

// asExpr returns n as an expression, or nil.
func asExpr(n ast.Node) ast.Expr {
	e, _ := n.(ast.Expr)
	return e
}

func evalP7(h *guardHost) guardResult {
	r := guardResult{noScope: "no " + envExampleFile, unit: "file"}
	raw, err := os.ReadFile(filepath.Join(h.root, envExampleFile))
	if errors.Is(err, fs.ErrNotExist) {
		return r
	}
	r.checked = 1
	if err != nil {
		r.add(envExampleFile, "cannot be read: "+err.Error())
		return r
	}
	keys := postureModeKeys(postureScan(h))
	commented := map[string]bool{}
	text := string(raw)
	for i, line := range strings.Split(text, "\n") {
		where := envExampleFile + ":" + strconv.Itoa(i+1)
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		if comment, ok := strings.CutPrefix(line, "#"); ok {
			for _, k := range keys {
				if mentions(comment, k) {
					commented[k] = true
				}
			}
			continue
		}
		key, value, _ := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		key = strings.TrimSpace(key)
		if i := inlineComment(value); i >= 0 {
			value = value[:i]
		}
		if strings.Contains(strings.ToLower(key+"="+strings.TrimSpace(value)), sslmodeDisable) {
			r.add(where, "uncommented "+key+" with sslmode=disable (comment it out; production refuses plaintext)")
		}
		for _, k := range keys {
			if key == k {
				r.add(where, "assigns the mode key "+k+" (LoadEnvForMode refuses it in .env; mention it only in a comment)")
			}
		}
	}
	for _, k := range keys {
		if !commented[k] {
			r.add(envExampleFile, "does not mention the mode key "+k+" in a comment (# "+k+"=development|production — read from the process environment only)")
		}
	}
	for _, name := range envExampleNames {
		if !mentions(text, name) {
			r.add(envExampleFile, "does not name "+name)
		}
	}
	return r
}

// mentions reports whether text names key as a whole word: AUTH_PUBLIC_BASE_URL
// does not name PUBLIC_BASE_URL.
func mentions(text, key string) bool {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(key) + `([^A-Za-z0-9_]|$)`).MatchString(text)
}

// inlineComment returns the index of a # preceded by whitespace in an
// unquoted dotenv value, the sdk loader's inline-comment rule, or -1.
func inlineComment(value string) int {
	for i := 1; i < len(value); i++ {
		if value[i] == '#' && (value[i-1] == ' ' || value[i-1] == '\t') {
			return i
		}
	}
	return -1
}

func evalP8(h *guardHost) guardResult {
	r := guardResult{noScope: "cms pocket not imported", unit: "literal"}
	files := postureScan(h)
	if !importsPath(files, postureCMS) {
		return r
	}
	r.noScope = "no cms.Config literal"
	for _, f := range files {
		cms := f.name(postureCMS)
		if cms == "" {
			continue
		}
		records := map[string]bool{cms: true}
		for _, fd := range funcDecls(f) {
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CompositeLit:
					pkg, ok := recordType(n.Type, records)
					if !ok {
						return true
					}
					r.checked++
					_, gated := litKey(n, "AdminMiddleware")
					_, acknowledged := litKey(n, "AdminUngatedAcknowledged")
					if !gated && !acknowledged {
						r.add(posOf(f.guardFile, n.Pos()), pkg+".Config names neither AdminMiddleware nor AdminUngatedAcknowledged (its admin surface is unmounted: pass an authorizing gate, or acknowledge a development demo)")
					}
				case *ast.ValueSpec:
					pkg, ok := recordType(n.Type, records)
					if !ok || len(n.Values) > 0 {
						return true
					}
					for _, id := range n.Names {
						r.checked++
						if !assignsField(fd.Body, id.Name, "AdminMiddleware", "AdminUngatedAcknowledged") {
							r.add(posOf(f.guardFile, id.Pos()), "var "+id.Name+" "+pkg+".Config: this function sets neither AdminMiddleware nor AdminUngatedAcknowledged")
						}
					}
				}
				return true
			})
		}
	}
	return r
}

func evalP9(h *guardHost) guardResult {
	r := guardResult{noScope: "no framework store module mounted", unit: "store"}
	required, err := readRequires(h.root)
	if err != nil {
		r.add("go.mod", "cannot be read: "+err.Error())
		return r
	}
	mounted := mountedStores(h, required)
	if len(mounted) == 0 {
		return r
	}
	r.noScope = "no mounted store ships " + storeMigrationsDir + "/"

	ledgerRel, ledgerPath := "workshop/migrations/"+defaultLedger, ledgerDir(h.root, defaultLedger)
	ledger, err := ledgerFiles(ledgerPath)
	if err != nil {
		r.add(ledgerRel, "cannot be read: "+err.Error())
		return r
	}
	bySuffix := map[string][]string{}
	for _, name := range ledger {
		if suffix, ok := migrationSuffix(name); ok {
			bySuffix[suffix] = append(bySuffix[suffix], name)
		}
	}

	var declined []string
	for _, module := range mounted {
		dir, err := moduleDir(h.root, module)
		if err != nil {
			r.add(module, "cannot resolve the module directory with go list -m: "+err.Error())
			continue
		}
		migrations := filepath.Join(dir, storeMigrationsDir)
		if info, err := os.Stat(migrations); err != nil || !info.IsDir() {
			continue
		}
		r.checked++
		files, err := ledgerFiles(migrations)
		if err != nil {
			r.add(module+"/"+storeMigrationsDir, "cannot be read: "+err.Error())
			continue
		}
		for _, name := range files {
			suffix, ok := migrationSuffix(name)
			if !ok {
				continue
			}
			where := module + "/" + storeMigrationsDir + "/" + name
			want, err := os.ReadFile(filepath.Join(migrations, name))
			if err != nil {
				r.add(where, "cannot be read: "+err.Error())
				continue
			}
			copies := bySuffix[suffix]
			if len(copies) == 0 && strings.HasSuffix(name, immutableSuffix) {
				declined = append(declined, name)
				continue
			}
			if len(copies) == 0 {
				r.add(where, "ledger copy missing from "+ledgerRel+" ("+ledgerReexport+")")
				continue
			}
			identical := false
			var edited []string
			for _, c := range copies {
				got, err := os.ReadFile(filepath.Join(ledgerPath, c))
				if err == nil && bytes.Equal(got, want) {
					identical = true
					break
				}
				edited = append(edited, ledgerRel+"/"+c)
			}
			if !identical {
				r.add(where, "ledger copy "+strings.Join(edited, ", ")+" is not byte-identical ("+ledgerReexport+")")
			}
		}
	}
	if len(declined) > 0 {
		r.detail = "declined: " + strings.Join(declined, ", ")
	}
	return r
}

// readRequires returns the module path of every require directive in
// <root>/go.mod, single-line and block form.
func readRequires(root string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	var out []string
	block := false
	for _, line := range strings.Split(string(raw), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case block && line == ")":
			block = false
			continue
		case block:
		default:
			rest, ok := strings.CutPrefix(line, "require")
			if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t' && rest[0] != '(') {
				continue
			}
			line = strings.TrimSpace(rest)
			if line == "(" {
				block = true
				continue
			}
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		p := fields[0]
		if u, err := strconv.Unquote(p); err == nil {
			p = u
		}
		out = append(out, p)
	}
	return out, nil
}

// mountedStores returns, sorted, the required framework store modules
// (pockets/<pocket>/stores/<dialect>) that a production file of the host
// imports, the module itself or a package beneath it. A store named only by
// tests is not mounted.
func mountedStores(h *guardHost, required []string) []string {
	var out []string
	for _, module := range required {
		parts := strings.Split(strings.TrimPrefix(module, fwPockets+"/"), "/")
		if !strings.HasPrefix(module, fwPockets+"/") || len(parts) != 3 || parts[1] != "stores" {
			continue
		}
		if importedInProduction(h, module) {
			out = append(out, module)
		}
	}
	sort.Strings(out)
	return out
}

func importedInProduction(h *guardHost, module string) bool {
	for _, pkg := range h.packages {
		for _, f := range pkg.files {
			if f.test {
				continue
			}
			for _, imp := range f.imports {
				if underPath(imp.path, module) {
					return true
				}
			}
		}
	}
	return false
}

// moduleDir resolves a module's directory the way the host's build does —
// `go list -m` in the host root, so a go.work, a replace directive or the
// module cache answers — bounded by storeListTimeout.
func moduleDir(root, module string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), storeListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}", module)
	cmd.Dir = root
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", errors.New("timed out after " + storeListTimeout.String())
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", errors.New("not in the module cache (go mod download " + module + ")")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", errors.New("resolves to " + dir + ", which is not a directory")
	}
	return dir, nil
}

// migrationSuffix returns what follows the numeric prefix of NNNN_<suffix>.sql;
// ok is false for a name without one.
func migrationSuffix(name string) (string, bool) {
	prefix, suffix, ok := strings.Cut(name, "_")
	if !ok || prefix == "" || suffix == "" {
		return "", false
	}
	for _, c := range prefix {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	return suffix, true
}

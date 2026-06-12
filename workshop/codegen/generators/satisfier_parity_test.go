package generators

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
)

// The half-shipped-feature guard (v0.5 parity audit): when an ENGINE struct
// gains a field, every satisfier mapping that constructs it must populate
// the new field, or it silently zero-values through the whole feature — the
// invitations RedirectURL failure class. This test parses the satisfier
// sources, collects which fields each engine-struct composite literal
// assigns, and diffs against the engine type via reflection.
//
// A field that is legitimately not row-backed belongs in
// satisfierParityIgnores with a comment saying why — never silently.
// Not watched, with reasons: authentication.Principal is constructed by
// the engine itself, never by a satisfier; invitations types convert via
// field-identical struct conversion (compiler-enforced, plus the reflect
// TestStructParity in the invitations satisfier package).
var satisfierParityWatched = map[string]reflect.Type{
	"authentication.User":    reflect.TypeOf(authentication.User{}),
	"authentication.Session": reflect.TypeOf(authentication.Session{}),
	"authentication.APIKey":  reflect.TypeOf(authentication.APIKey{}),
}

// satisfierParityIgnores lists engine-struct fields a mapping may omit, each
// with a reason. Empty entries are the goal.
var satisfierParityIgnores = map[string]map[string]string{}

func TestSatisfierMappingsPopulateEngineStructs(t *testing.T) {
	root := frameworkRootForParity(t)

	var sources []string
	sources = append(sources, authenticationSatisfierPaths...)
	sources = append(sources, authorizationSatisfierPaths...)
	sources = append(sources, invitationsSatisfierPaths...)

	assigned := map[string]map[string]bool{} // watched type → fields seen
	for name := range satisfierParityWatched {
		assigned[name] = map[string]bool{}
	}

	fset := token.NewFileSet()
	for _, rel := range sources {
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// Variables in this function holding a watched type, by name —
			// mappings like `as := authentication.Session{...}` followed by
			// guarded `as.Field = ...` assignments count too.
			varTypes := map[string]string{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CompositeLit:
					typeName := selectorTypeName(node.Type)
					fields, watched := assigned[typeName]
					if !watched {
						return true
					}
					for _, elt := range node.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.Ident); ok {
								fields[key.Name] = true
							}
						}
					}
				case *ast.AssignStmt:
					// var := watched{...} tracking.
					for i, rhs := range node.Rhs {
						if lit, ok := rhs.(*ast.CompositeLit); ok && i < len(node.Lhs) {
							if name, ok := node.Lhs[i].(*ast.Ident); ok {
								if tn := selectorTypeName(lit.Type); tn != "" {
									if _, watched := assigned[tn]; watched {
										varTypes[name.Name] = tn
									}
								}
							}
						}
					}
					// var.Field = ... assignments on tracked vars.
					for _, lhs := range node.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok {
							if recv, ok := sel.X.(*ast.Ident); ok {
								if tn, tracked := varTypes[recv.Name]; tracked {
									assigned[tn][sel.Sel.Name] = true
								}
							}
						}
					}
				}
				return true
			})
		}
	}

	for typeName, typ := range satisfierParityWatched {
		seen := assigned[typeName]
		if len(seen) == 0 {
			t.Errorf("%s: no satisfier constructs this engine struct — if the mapping moved, update satisfierParityWatched", typeName)
			continue
		}
		var missing []string
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			if seen[name] {
				continue
			}
			if reason, ok := satisfierParityIgnores[typeName][name]; ok && reason != "" {
				continue
			}
			missing = append(missing, name)
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: engine fields never populated by any satisfier mapping: %s — a new engine field must be mapped (or explicitly ignored with a reason in satisfierParityIgnores)", typeName, strings.Join(missing, ", "))
		}
	}
}

// selectorTypeName renders pkg.Type composite literal types ("" otherwise).
func selectorTypeName(expr ast.Expr) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

// frameworkRootForParity walks up from this file to go.mod.
func frameworkRootForParity(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	for i := 0; i < 10; i++ {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found above satisfier_parity_test.go")
	return ""
}

package commands

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type pocketBoundaryFinding struct {
	rule    string
	file    string
	message string
}

// These checks also run against emitted pockets. Walking module boundaries keeps
// memory and conformance packages covered even though they live under stores/.
func pocketBoundaryFindings(root, modulePath string, checkLogic bool) ([]pocketBoundaryFinding, error) {
	var findings []pocketBoundaryFinding
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					rel, err := filepath.Rel(root, path)
					if err != nil {
						return err
					}
					parts := strings.Split(filepath.ToSlash(rel), "/")
					sharedChild := modulePath == baseModule+"/pockets" && len(parts) == 1
					adapter := len(parts) == 2 && (parts[0] == "views" ||
						(parts[0] == "stores" && parts[1] != "memory" && parts[1] != "storetest"))
					if !sharedChild && !adapter {
						findings = append(findings, pocketBoundaryFinding{"isolation", filepath.ToSlash(filepath.Join(rel, "go.mod")),
							"unexpected nested module: only driver stores and view adapters get separate modules"})
					}
					return filepath.SkipDir
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		add := func(rule, message string) {
			findings = append(findings, pocketBoundaryFinding{rule, rel, message})
		}
		isTest := strings.HasSuffix(rel, "_test.go")
		isStoreSupport := strings.HasPrefix(rel, "stores/memory/") || strings.HasPrefix(rel, "stores/storetest/")
		isLogic := strings.HasPrefix(rel, "logic/") || strings.HasPrefix(rel, "domain/") || strings.HasPrefix(rel, "internal/logic/")
		imports := make(map[string]string)
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			alias := filepath.Base(importPath)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			imports[alias] = importPath
			ownImport := importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")
			ownStoreSupport := importPath == modulePath+"/stores/memory" || strings.HasPrefix(importPath, modulePath+"/stores/memory/") ||
				importPath == modulePath+"/stores/storetest" || strings.HasPrefix(importPath, modulePath+"/stores/storetest/")
			if strings.HasPrefix(importPath, baseModule+"/pockets/") && (!ownImport || modulePath == baseModule+"/pockets") {
				add("cross-pocket", "imports another pocket: "+importPath)
			}
			if strings.HasPrefix(importPath, baseModule+"/integrations/") || strings.HasPrefix(importPath, baseModule+"/examples/") || strings.HasPrefix(importPath, baseModule+"/ui/") {
				add("isolation", "core imports an infrastructure or host adapter: "+importPath)
			}
			if strings.HasPrefix(importPath, modulePath+"/stores/") || strings.HasPrefix(importPath, modulePath+"/views/") {
				if !ownStoreSupport || (!isTest && !isStoreSupport) {
					add("isolation", "core imports an adapter: "+importPath)
				}
			}
			if isStoreSupport && !ownImport && importPath != baseModule+"/pockets" &&
				importPath != baseModule+"/sdk" && !strings.HasPrefix(importPath, baseModule+"/sdk/") &&
				strings.Contains(strings.Split(importPath, "/")[0], ".") {
				add("isolation", "memory and conformance packages require only stdlib, SDK and their own pocket: "+importPath)
			}
			if checkLogic && isLogic && !isTest && (importPath == "net/http" || strings.HasPrefix(importPath, "net/http/") ||
				importPath == baseModule+"/sdk/pkg/web" || importPath == modulePath ||
				importPath == modulePath+"/inbound" || strings.HasPrefix(importPath, modulePath+"/inbound/") ||
				importPath == modulePath+"/internal/inbound" || strings.HasPrefix(importPath, modulePath+"/internal/inbound/")) {
				add("logic", "logic must not depend on transport or root composition: "+importPath)
			}
		}
		if !isTest {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if ok && ((imports[pkg.Name] == "encoding/json" && selector.Sel.Name == "NewEncoder") ||
					(imports[pkg.Name] == "net/http" && selector.Sel.Name == "Error")) {
					add("transport", "HTTP responses must use sdk/pkg/web responders")
				}
				return true
			})
		}
		return nil
	})
	return findings, err
}

func TestPocketBoundaries(t *testing.T) {
	root := repoRoot(t)
	modules, err := pocketCoreModules(filepath.Join(root, "pockets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"isolation", "cross-pocket", "transport", "logic"} {
		t.Run(rule, func(t *testing.T) {
			for _, dir := range modules {
				name := filepath.Base(dir)
				modulePath := baseModule + "/pockets"
				if name != "pockets" {
					modulePath += "/" + name
				}
				findings, err := pocketBoundaryFindings(dir, modulePath, name != "cms")
				if err != nil {
					t.Fatal(err)
				}
				for _, finding := range findings {
					if finding.rule == rule {
						t.Errorf("pockets/%s/%s: %s", name, finding.file, finding.message)
					}
				}
				if rule == "cross-pocket" {
					views, err := filepath.Glob(filepath.Join(dir, "views", "*", "go.mod"))
					if err != nil {
						t.Fatal(err)
					}
					for _, mod := range views {
						findings, err := pocketBoundaryFindings(filepath.Dir(mod), modulePath, false)
						if err != nil {
							t.Fatal(err)
						}
						for _, finding := range findings {
							if finding.rule == rule {
								t.Errorf("%s/%s: %s", filepath.Dir(mod), finding.file, finding.message)
							}
						}
					}
				}
			}
		})
	}
}

func pocketCoreModules(root string) ([]string, error) {
	modules := []string{root}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			modules = append(modules, dir)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return modules, nil
}

func TestPocketBoundaryFixtures(t *testing.T) {
	modulePath := "example.com/notes"
	tests := []struct {
		name, file, source, rule string
		nestedModule             bool
	}{
		{"host uses memory in API test", "service_test.go", `package notes_test; import "example.com/notes/stores/memory"`, "", false},
		{"logic cannot wire memory", "internal/logic/service.go", `package logic; import "example.com/notes/stores/memory"`, "isolation", false},
		{"moved memory cannot import connector", "stores/memory/store.go", `package memory; import "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"`, "isolation", false},
		{"moved conformance cannot import driver", "stores/storetest/suite.go", `package storetest; import "github.com/jackc/pgx/v5"`, "isolation", false},
		{"moved memory cannot import foreign pocket", "stores/memory/store.go", fmt.Sprintf(`package memory; import %q`, baseModule+"/pockets/jobs"), "cross-pocket", false},
		{"external driver module is separate", "stores/pgx/store.go", `package pgx; import "github.com/jackc/pgx/v5"`, "", true},
		{"memory cannot become a separate module", "stores/memory/store.go", `package memory`, "isolation", true},
		{"conformance cannot become a separate module", "stores/storetest/suite.go", `package storetest`, "isolation", true},
		{"logic cannot hide behind a module", "internal/logic/service.go", `package logic; import "net/http"`, "isolation", true},
		{"unmodularized store is still core", "stores/pgx/store.go", `package pgx; import "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"`, "isolation", false},
		{"logic cannot write HTTP", "internal/logic/service.go", `package logic; import "net/http"`, "logic", false},
		{"public logic cannot write HTTP", "logic/notes/service.go", `package notes; import "net/http"`, "logic", false},
		{"public logic cannot import public inbound", "logic/notes/service.go", `package notes; import "example.com/notes/inbound/http"`, "logic", false},
		{"public logic cannot import root composition", "logic/notes/service.go", `package notes; import "example.com/notes"`, "logic", false},
		{"public logic cannot hide behind a module", "logic/notes/service.go", `package notes; import "net/http"`, "isolation", true},
		{"public HTTP may consume logic", "inbound/http/routes.go", `package noteshttp; import "example.com/notes/logic/notes"`, "", false},
		{"public HTTP may depend on web", "inbound/http/routes.go", `package noteshttp; import "github.com/gopernicus/gopernicus/sdk/pkg/web"`, "", false},
		{"domain cannot depend on web", "domain/note/model.go", `package note; import "github.com/gopernicus/gopernicus/sdk/pkg/web"`, "logic", false},
		{"HTTP adapter may depend on web", "internal/inbound/routes.go", `package inbound; import "github.com/gopernicus/gopernicus/sdk/pkg/web"`, "", false},
		{"aliased manual HTTP response", "internal/inbound/routes.go", `package inbound; import h "net/http"; func respond() { h.Error(nil, "error", 500) }`, "transport", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, filepath.FromSlash(test.file))
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(test.source), 0644); err != nil {
				t.Fatal(err)
			}
			if test.nestedModule {
				if err := os.WriteFile(filepath.Join(filepath.Dir(file), "go.mod"), []byte("module "+modulePath+"/stores/pgx\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			findings, err := pocketBoundaryFindings(root, modulePath, true)
			if err != nil {
				t.Fatal(err)
			}
			if test.rule == "" {
				if len(findings) != 0 {
					t.Fatalf("unexpected findings: %v", findings)
				}
				return
			}
			for _, finding := range findings {
				if finding.rule == test.rule {
					return
				}
			}
			t.Fatalf("expected %s finding, got %v", test.rule, findings)
		})
	}
}

func TestPocketBoundariesDiscoverNewCore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pockets")
	core := filepath.Join(root, "newpocket")
	if err := os.MkdirAll(core, 0755); err != nil {
		t.Fatal(err)
	}
	modulePath := baseModule + "/pockets/newpocket"
	if err := os.WriteFile(filepath.Join(core, "go.mod"), []byte("module "+modulePath+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf("package newpocket\nimport %q\n", baseModule+"/integrations/datastores/pgxdb")
	if err := os.WriteFile(filepath.Join(core, "service.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	modules, err := pocketCoreModules(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range modules {
		if dir != core {
			continue
		}
		findings, err := pocketBoundaryFindings(dir, modulePath, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range findings {
			if finding.rule == "isolation" {
				return
			}
		}
		t.Fatal("new pocket's adapter import was not rejected")
	}
	t.Fatal("new pocket module was not discovered")
}

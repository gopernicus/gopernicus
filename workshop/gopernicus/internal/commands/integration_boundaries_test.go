package commands

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func integrationBoundaryFindings(root string) ([]string, error) {
	data, err := exec.Command("go", "mod", "edit", "-json", filepath.Join(root, "go.mod")).Output()
	if err != nil {
		return nil, err
	}
	var mod struct {
		Module  struct{ Path string }
		Require []struct{ Path string }
	}
	if err := json.Unmarshal(data, &mod); err != nil {
		return nil, err
	}
	allowed := func(path string) bool {
		if path != baseModule && !strings.HasPrefix(path, baseModule+"/") {
			return true
		}
		return path == baseModule+"/sdk" || strings.HasPrefix(path, baseModule+"/sdk/") ||
			path == mod.Module.Path || strings.HasPrefix(path, mod.Module.Path+"/")
	}
	var findings []string
	for _, dependency := range mod.Require {
		if !allowed(dependency.Path) {
			findings = append(findings, "go.mod requires "+dependency.Path)
		}
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if !allowed(importPath) {
				findings = append(findings, fmt.Sprintf("%s imports %s", path, importPath))
			}
		}
		return nil
	})
	return findings, err
}

func TestIntegrationBoundaries(t *testing.T) {
	modules, err := filepath.Glob(filepath.Join(repoRoot(t), "integrations", "*", "*", "go.mod"))
	if err != nil || len(modules) == 0 {
		t.Fatalf("integration inventory: modules=%d err=%v", len(modules), err)
	}
	for _, mod := range modules {
		t.Run(filepath.Dir(mod), func(t *testing.T) {
			findings, err := integrationBoundaryFindings(filepath.Dir(mod))
			if err != nil {
				t.Fatal(err)
			}
			for _, finding := range findings {
				t.Error(finding)
			}
		})
	}
}

func TestIntegrationBoundaryFixtures(t *testing.T) {
	self := baseModule + "/integrations/datastores/example"
	for _, tc := range []struct {
		name, imported, required string
		invalid                  bool
	}{
		{"SDK", baseModule + "/sdk/pkg/list", baseModule + "/sdk", false},
		{"self", self + "/internal/helper", "", false},
		{"vendor companion", "google.golang.org/grpc/status", "google.golang.org/grpc", false},
		{"peer import", baseModule + "/integrations/datastores/other", "", true},
		{"peer requirement", "context", baseModule + "/integrations/datastores/other", true},
		{"similar prefix", self + "extra", "", true},
		{"pocket", baseModule + "/pockets", "", true},
		{"UI", baseModule + "/ui/goth", "", true},
		{"host requirement", "context", baseModule + "/examples/minimal", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mod := "module " + self + "\n\ngo 1.26.1\n"
			if tc.required != "" {
				mod += "require " + tc.required + " v0.1.0\n"
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0o644); err != nil {
				t.Fatal(err)
			}
			source := "package example\nimport _ " + strconv.Quote(tc.imported) + "\n"
			if err := os.WriteFile(filepath.Join(root, "example.go"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			findings, err := integrationBoundaryFindings(root)
			if err != nil {
				t.Fatal(err)
			}
			if (len(findings) > 0) != tc.invalid {
				t.Fatalf("findings=%v, want invalid=%v", findings, tc.invalid)
			}
		})
	}
}

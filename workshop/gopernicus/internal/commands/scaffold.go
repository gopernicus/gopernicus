package commands

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"
)

// baseModule is the gopernicus module root. Emitted import paths are built from
// it so no Go source in this package hardcodes a bare legacy import prefix.
const baseModule = "github.com/gopernicus/gopernicus"

// templateFile is one emitted artifact: an embedded template path and the path
// (relative to the target dir) it renders to. Format runs go/format on the
// rendered bytes — set it for Go sources only.
type templateFile struct {
	Template string
	Out      string
	Format   bool
}

// emit renders each file against params and writes it under targetDir, creating
// parent directories. It refuses to clobber: if any target path already exists,
// nothing is written. This is the shared render engine every scaffold command
// reuses (init here; new feature in W3).
func emit(tmpls fs.FS, targetDir string, files []templateFile, params any) error {
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(targetDir, f.Out)); err == nil {
			return fmt.Errorf("refusing to overwrite existing file: %s", f.Out)
		}
	}
	for _, f := range files {
		rendered, err := render(tmpls, f.Template, params)
		if err != nil {
			return fmt.Errorf("render %s: %w", f.Template, err)
		}
		if f.Format {
			if formatted, ferr := format.Source(rendered); ferr == nil {
				rendered = formatted
			}
		}
		dst := filepath.Join(targetDir, f.Out)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, rendered, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// render reads one embedded template and executes it against params. missingkey
// errors surface unresolved fields instead of emitting "<no value>".
func render(tmpls fs.FS, name string, params any) ([]byte, error) {
	src, err := fs.ReadFile(tmpls, name)
	if err != nil {
		return nil, err
	}
	t, err := template.New(filepath.Base(name)).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, params); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

package delivery_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AV3D-2.4 structural guard — no producer bypasses the delivery dispatcher seam.
//
// Every outbound authentication message is admitted through the deliveryQueue seam
// (Enqueue/Replace/Status), which delivery.Service backs with a transport-neutral
// Dispatcher (AV3D-2.3). A producer must NEVER call a provider directly: the render
// happens through delivery.Router.Render (allowed — it only produces an Envelope),
// but the actual send (Router.Deliver, an email.Sender.Send, or a notify.Notifier
// .Notify) is the worker/processor's job, off the request path. A producer that
// called any of those verbs would deliver a secret on the request path, defeating
// enumeration safety and the durable-outbox contract.
//
// This is the "no producer calls a provider directly / bypasses the dispatcher
// seam" tripwire. A Makefile grep would be disproportionate (it belongs to one
// feature's internal layering), so it lives as a Go test beside the seam it guards.
// It scans every sibling package of delivery under internal/logic (authsvc,
// invitationsvc, and any future producer) so a newly added producer is covered
// automatically — the enumeration is by directory, not a hand-maintained list.

// providerSendVerbs are the provider-send methods a producer may never call: the
// render/enqueue seam is the only sanctioned outbound path. Deliver is the Router's
// send; Send is an email.Sender; Notify is a notify.Notifier.
var providerSendVerbs = map[string]struct{}{
	"Deliver": {},
	"Send":    {},
	"Notify":  {},
}

// TestNoProducerBypassesDispatcherSeam fails if any producer package (every sibling
// of delivery under internal/logic) calls a provider-send verb directly instead of
// admitting the message through the deliveryQueue seam.
func TestNoProducerBypassesDispatcherSeam(t *testing.T) {
	// The test runs with the delivery package as its working directory; "../" is
	// internal/logic, whose non-delivery subdirectories are the producer packages.
	logicDir := ".."
	entries, err := os.ReadDir(logicDir)
	if err != nil {
		t.Fatalf("read internal/logic: %v", err)
	}

	var scanned []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "delivery" {
			continue
		}
		pkgDir := filepath.Join(logicDir, e.Name())
		scanned = append(scanned, e.Name())
		scanProducerPackage(t, pkgDir)
	}

	// A guard that scanned nothing is a silently-passing guard: assert it actually
	// found the known producer packages.
	for _, want := range []string{"authsvc", "invitationsvc"} {
		found := false
		for _, s := range scanned {
			if s == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("guard scanned %v, but did not find producer package %q — the scan is not covering producers", scanned, want)
		}
	}
}

// scanProducerPackage parses every non-test .go file directly under pkgDir and
// reports any call to a provider-send verb with its file:line.
func scanProducerPackage(t *testing.T, pkgDir string) {
	t.Helper()
	files, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read %s: %v", pkgDir, err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, f.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, banned := providerSendVerbs[sel.Sel.Name]; banned {
				pos := fset.Position(call.Pos())
				t.Errorf("producer bypasses the delivery dispatcher seam: %s:%d calls %q directly — outbound must go through the deliveryQueue seam (Enqueue/Replace), never a provider send",
					filepath.Base(path), pos.Line, sel.Sel.Name)
			}
			return true
		})
	}
}

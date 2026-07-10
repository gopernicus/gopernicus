// Package cacher_test (external test package) wires cacher.Memory into the
// shared cachertest conformance suite. This lives outside package cacher
// (rather than in memory_test.go, package cacher) because cachertest imports
// cacher: an in-package test file pulling in cachertest would be an import
// cycle (cacher's test variant -> cachertest -> cacher). The external test
// package is the standard Go way to consume a testing helper that itself
// imports the package under test (mirrors net/http vs net/http/httptest).
package cacher_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher/cachertest"
)

func TestMemory_Conformance(t *testing.T) {
	cachertest.Run(t, func(t *testing.T) cacher.Storer { return cacher.NewMemory() })
}

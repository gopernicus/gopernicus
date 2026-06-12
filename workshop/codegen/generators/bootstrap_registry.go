package generators

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// bootstrapMarkerPrefix opens the marker line written as the first line of
// every bootstrap file at creation time. The marker records which template
// (by content hash) created the file, so doctor can detect when a project
// carries a bootstrap from an older template vintage. The hash covers the
// TEMPLATE, not the rendered file — user edits to bootstraps are expected
// and never count as drift.
const bootstrapMarkerPrefix = "// gopernicus:bootstrap "

// bootstrapTemplates is the registry of every bootstrap kind the generators
// emit, mapping kind → the template constant that renders it. Kind names
// are `<generator>/<file>` and are part of the marker contract — renaming
// one orphans existing markers. A new bootstrap genFile entry MUST be
// registered here: bootstrapMarker panics on unknown kinds so generation
// fails loud, never silently unmarked.
var bootstrapTemplates = map[string]string{
	"repository/repository.go":         repoRepositoryTemplate,
	"repository/fop.go":                repoFopTemplate,
	"pgxstore/store.go":                storeBootstrapTemplate,
	"specstore/store.go":               specStoreBootstrapTemplate,
	"bridge/bridge.go":                 bridgeBridgeTemplate,
	"bridge/routes.go":                 bridgeRoutesTemplate,
	"bridge/http.go":                   bridgeHttpTemplate,
	"bridge/fop.go":                    bridgeFopTemplate,
	"authschema/authschema.go":         authSchemaBootstrapTemplate,
	"cache/cache.go":                   cacheBootstrapTemplate,
	"fixtures/fixtures.go":             fixtureBootstrapTemplate,
	"integrationtest/store_test.go":    integrationTestBootstrapTemplate,
	"bridge-e2e/e2e_test.go":           bridgeE2EBootstrapTemplate,
	"bridge-security/security_test.go": bridgeSecurityBootstrapTemplate,
	"tsclient/client.ts":               tsClientBootstrapTemplate,
}

// BootstrapTemplateHash returns the content hash of the registered template
// for kind, and whether the kind is known to this framework version.
func BootstrapTemplateHash(kind string) (string, bool) {
	tmpl, ok := bootstrapTemplates[kind]
	if !ok {
		return "", false
	}
	return hashTemplate(tmpl), true
}

// BootstrapBasenames returns the set of file basenames that are bootstrap
// files by convention — used by doctor to count pre-marker bootstraps.
func BootstrapBasenames() map[string]bool {
	names := make(map[string]bool, len(bootstrapTemplates))
	for kind := range bootstrapTemplates {
		if i := strings.IndexByte(kind, '/'); i >= 0 {
			names[kind[i+1:]] = true
		}
	}
	return names
}

// ParseBootstrapMarker parses a bootstrap marker line into its kind and
// template hash. ok is false when the line is not a marker.
func ParseBootstrapMarker(line string) (kind, hash string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(line), bootstrapMarkerPrefix)
	if !found {
		return "", "", false
	}
	for _, field := range strings.Fields(rest) {
		if v, found := strings.CutPrefix(field, "kind="); found {
			kind = v
		}
		if v, found := strings.CutPrefix(field, "template="); found {
			hash = v
		}
	}
	return kind, hash, kind != "" && hash != ""
}

// prependBootstrapMarker prefixes rendered bootstrap output with its marker
// line. Panics on an unregistered kind: a bootstrap file written without a
// marker would be invisible to drift detection forever, so generation must
// fail loud at develop time, not ship silently unmarked.
func prependBootstrapMarker(kind string, rendered []byte) []byte {
	tmpl, ok := bootstrapTemplates[kind]
	if !ok {
		panic(fmt.Sprintf("generators: bootstrap kind %q not in bootstrapTemplates — register it in bootstrap_registry.go", kind))
	}
	marker := fmt.Sprintf("%skind=%s template=%s\n", bootstrapMarkerPrefix, kind, hashTemplate(tmpl))
	return append([]byte(marker), rendered...)
}

func hashTemplate(tmpl string) string {
	sum := sha256.Sum256([]byte(tmpl))
	return hex.EncodeToString(sum[:])[:12]
}

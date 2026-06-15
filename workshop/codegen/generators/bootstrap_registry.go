package generators

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// The marker line is written as the first line of every bootstrap file at
// creation time (second line when the file opens with a shebang). The
// marker records which template (by content hash) created the file, so
// doctor can detect when a project carries a bootstrap from an older
// template vintage. The hash covers the TEMPLATE, not the rendered file —
// user edits to bootstraps are expected and never count as drift.
//
// The marker body is identical across comment styles; only the comment
// syntax varies by file type: `//` for Go/TypeScript, `#` for
// yaml/makefile/shell-family files, `<!-- -->` for markdown.
const (
	bootstrapMarkerPrefix     = "// gopernicus:bootstrap "
	bootstrapMarkerPrefixHash = "# gopernicus:bootstrap "
	bootstrapMarkerPrefixMD   = "<!-- gopernicus:bootstrap "
)

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

	// Deploy profiles (gopernicus new deploy <target>) — non-Go files;
	// markers use the comment style matching each file type.
	"deploy/do-app/workflow.yml":           deployDoAppWorkflowTemplate,
	"deploy/do-app/app-spec.yaml":          deployDoAppSpecTemplate,
	"deploy/do-app/README.md":              deployDoAppReadmeTemplate,
	"deploy/cloud-run/makefile.cloud-run":  deployCloudRunMakefileTemplate,
	"deploy/cloud-run/README.md":           deployCloudRunReadmeTemplate,
	"deploy/compose-prod/compose.prod.yml": deployComposeProdComposeTemplate,
	"deploy/compose-prod/Caddyfile":        deployComposeProdCaddyfileTemplate,
	"deploy/compose-prod/deploy.sh":        deployComposeProdDeployShTemplate,
	"deploy/compose-prod/backup.sh":        deployComposeProdBackupShTemplate,
	"deploy/compose-prod/systemd.service":  deployComposeProdSystemdTemplate,
	"deploy/compose-prod/README.md":        deployComposeProdReadmeTemplate,
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
// template hash, accepting any of the comment styles. ok is false when
// the line is not a marker.
func ParseBootstrapMarker(line string) (kind, hash string, ok bool) {
	line = strings.TrimSpace(line)
	var rest string
	var found bool
	for _, prefix := range []string{bootstrapMarkerPrefix, bootstrapMarkerPrefixHash, bootstrapMarkerPrefixMD} {
		if rest, found = strings.CutPrefix(line, prefix); found {
			break
		}
	}
	if !found {
		return "", "", false
	}
	rest = strings.TrimSuffix(strings.TrimSpace(rest), "-->")
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

// prependBootstrapMarkerStyled is prependBootstrapMarker for non-Go files:
// the comment style is chosen from the destination filename. Shell files
// keep their shebang on line 1 — the marker goes on line 2.
func prependBootstrapMarkerStyled(kind, filename string, rendered []byte) []byte {
	tmpl, ok := bootstrapTemplates[kind]
	if !ok {
		panic(fmt.Sprintf("generators: bootstrap kind %q not in bootstrapTemplates — register it in bootstrap_registry.go", kind))
	}

	var marker string
	switch {
	case strings.HasSuffix(filename, ".md"):
		marker = fmt.Sprintf("%skind=%s template=%s -->\n", bootstrapMarkerPrefixMD, kind, hashTemplate(tmpl))
	case strings.HasSuffix(filename, ".go") || strings.HasSuffix(filename, ".ts"):
		marker = fmt.Sprintf("%skind=%s template=%s\n", bootstrapMarkerPrefix, kind, hashTemplate(tmpl))
	default: // yaml, makefile, shell, Caddyfile, systemd units, cron
		marker = fmt.Sprintf("%skind=%s template=%s\n", bootstrapMarkerPrefixHash, kind, hashTemplate(tmpl))
	}

	if shebang, body, found := strings.Cut(string(rendered), "\n"); found && strings.HasPrefix(shebang, "#!") {
		return []byte(shebang + "\n" + marker + body)
	}
	return append([]byte(marker), rendered...)
}

func hashTemplate(tmpl string) string {
	sum := sha256.Sum256([]byte(tmpl))
	return hex.EncodeToString(sum[:])[:12]
}

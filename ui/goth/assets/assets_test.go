package assets

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
)

type manifestEntry struct {
	LogicalName string `json:"logicalName"`
	Path        string `json:"path"`
	Integrity   string `json:"integrity"`
	Bytes       int64  `json:"bytes"`
}

type manifestDoc struct {
	Assets []manifestEntry `json:"assets"`
}

func loadManifest(t *testing.T) manifestDoc {
	t.Helper()
	var doc manifestDoc
	if err := json.Unmarshal(ManifestBytes, &doc); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(doc.Assets) == 0 {
		t.Fatal("manifest has no assets — run the Node-gated asset build")
	}
	return doc
}

// TestManifestMatchesEmbeddedBytes proves every manifest entry resolves to a real
// embedded file whose length and sha384 SRI digest match the recorded values. A
// hand-edited manifest or dist file fails this.
func TestManifestMatchesEmbeddedBytes(t *testing.T) {
	doc := loadManifest(t)
	for _, e := range doc.Assets {
		if !strings.HasPrefix(e.Path, "dist/") {
			t.Errorf("%s: path %q must retain the dist/ segment", e.LogicalName, e.Path)
		}
		data, err := fs.ReadFile(FS, e.Path)
		if err != nil {
			t.Errorf("%s: embedded file %q not found: %v", e.LogicalName, e.Path, err)
			continue
		}
		if int64(len(data)) != e.Bytes {
			t.Errorf("%s: bytes = %d, manifest = %d", e.LogicalName, len(data), e.Bytes)
		}
		sum := sha512.Sum384(data)
		want := "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
		if e.Integrity != want {
			t.Errorf("%s: integrity mismatch\n got %s\nwant %s", e.LogicalName, e.Integrity, want)
		}
	}
}

// TestFingerprintMatchesContent proves each fingerprinted filename is the sha256
// content hash prefix, so the manifest path is content-addressed (immutable-safe).
func TestFingerprintMatchesContent(t *testing.T) {
	doc := loadManifest(t)
	for _, e := range doc.Assets {
		data, err := fs.ReadFile(FS, e.Path)
		if err != nil {
			continue
		}
		name := strings.TrimPrefix(e.Path, "dist/")
		parts := strings.Split(name, ".")
		if len(parts) < 3 {
			t.Errorf("%s: %q is not <base>.<hash>.<ext>", e.LogicalName, name)
			continue
		}
		fp := parts[len(parts)-2]
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:])[:len(fp)]; got != fp {
			t.Errorf("%s: fingerprint %q != content hash prefix %q", e.LogicalName, fp, got)
		}
	}
}

// TestExpectedAssetSet locks the four build outputs (theme.css + the separate
// injected default palette theme-default.css + runtime.js + htmx.js, amendment-1
// D3) and proves no build/test tooling leaked into dist (axe-core is MPL-2.0,
// build/test only — never shipped).
func TestExpectedAssetSet(t *testing.T) {
	doc := loadManifest(t)
	want := map[string]bool{"theme.css": false, "theme-default.css": false, "runtime.js": false, "htmx.js": false}
	referenced := map[string]bool{}
	for _, e := range doc.Assets {
		if _, ok := want[e.LogicalName]; !ok {
			t.Errorf("unexpected manifest asset %q", e.LogicalName)
		}
		want[e.LogicalName] = true
		referenced[e.Path] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("manifest missing expected asset %q", name)
		}
	}
	// Every embedded dist file is referenced (no stale/orphan output) and none is
	// forbidden tooling.
	err := fs.WalkDir(FS, "dist", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		low := strings.ToLower(p)
		for _, forbidden := range []string{"axe", "playwright", "test"} {
			if strings.Contains(low, forbidden) {
				t.Errorf("dist contains forbidden tooling artifact %q", p)
			}
		}
		if !referenced[p] {
			t.Errorf("embedded dist file %q is not referenced by the manifest (stale output)", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk dist: %v", err)
	}
}

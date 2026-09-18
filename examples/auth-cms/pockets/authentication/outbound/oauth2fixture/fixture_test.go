package oauth2fixture

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestFixtureIsLoopbackOnlyAndSnapshotsConfiguration(t *testing.T) {
	for _, raw := range []string{"https://example.com", "http://example.com", "http://localhost:8082/", "http://localhost:8082?", "http://localhost:8082#", "http://u@localhost:8082"} {
		if _, err := New(raw); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("accepted non-local or ambiguous origin %q: %v", raw, err)
		}
	}
	fixture, err := New("http://localhost:8082")
	if err != nil {
		t.Fatal(err)
	}
	cfg := fixture.Config()
	if cfg.ConfidentialSecretHashes[0] != sha256.Sum256([]byte(fixture.ConfidentialSecret())) {
		t.Fatal("confidential fixture credential mismatch")
	}
	cfg.ConfidentialSecretHashes[0] = [32]byte{}
	if fixture.Config().ConfidentialSecretHashes[0] == ([32]byte{}) {
		t.Fatal("config getter exposed mutable secret list")
	}
	client := fixture.Client()
	client.RedirectURIs[0] = "http://elsewhere/callback"
	resolved, err := fixture.Resolve(t.Context(), client.ID)
	if err != nil || resolved.RedirectURIs[0] != "http://localhost:8082/oauth-demo/callback" {
		t.Fatalf("mutable redirect metadata: %+v %v", resolved, err)
	}
	if _, err := fixture.Resolve(t.Context(), client.ID+"?another"); !errors.Is(err, oauth2.ErrInvalidClient) {
		t.Fatalf("fixture accepted a different client ID: %v", err)
	}
}

package authentication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk"
)

// assuranceOAuth2Repository is recorderOAuth2Repository with the optional
// delegated-assurance capability; probe is what ProbeSchema reports.
type assuranceOAuth2Repository struct {
	recorderOAuth2Repository
	probe error
}

func (assuranceOAuth2Repository) CreateCodeWithAuthentication(context.Context, oauth2.Code, string) error {
	return nil
}

func (assuranceOAuth2Repository) GetCodeWithAuthentication(context.Context, string) (oauth2.Code, error) {
	return oauth2.Code{}, oauth2.ErrInvalidGrant
}

func (r assuranceOAuth2Repository) ProbeSchema(context.Context) error { return r.probe }

// TestDelegatedAssuranceAtNew pins OAuth2Config.DelegatedAssurance through New:
// a store without oauth2.CodeAuthenticationRepository refuses with
// ErrDelegatedAssuranceRequires, an unmigrated one with
// ErrDelegatedAssuranceSchemaMissing; a migrated one constructs; and the
// auth: oauth2 boot line names the posture, on or off.
func TestDelegatedAssuranceAtNew(t *testing.T) {
	build := func(repo oauth2.Repository, delegated bool) (string, error) {
		var buf bytes.Buffer
		cfg := browserBaseConfig()
		repos := testRepositories(Repositories{SecurityEvents: stubSecurityEvents{}, OAuth2: repo, SessionManagement: recorderSessionManagement{}})
		_, err := New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
			WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
			WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}),
			WithBrowser(BrowserConfig{Views: recorderViews{}}),
			WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
			WithOAuth2(OAuth2Config{
				Server: oauth2.Config{Issuer: "https://issuer.example", MCPResource: "https://mcp.example", APIResource: "https://api.example",
					ConfidentialClientID: "mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte("a high entropy confidential test secret"))}},
				Clients: recorderClients{}, Views: recorderOAuthViews{}, DelegatedAssurance: delegated,
			}))
		return buf.String(), err
	}
	if _, err := build(recorderOAuth2Repository{}, true); !errors.Is(err, ErrDelegatedAssuranceRequires) {
		t.Fatalf("unwired store = %v, want ErrDelegatedAssuranceRequires", err)
	}
	if _, err := build(assuranceOAuth2Repository{probe: fmt.Errorf("no column: %w", sdk.ErrNotFound)}, true); !errors.Is(err, ErrDelegatedAssuranceSchemaMissing) {
		t.Fatalf("unmigrated store = %v, want ErrDelegatedAssuranceSchemaMissing", err)
	}
	logs, err := build(assuranceOAuth2Repository{}, true)
	if err != nil || strings.Count(logs, `msg="auth: oauth2" delegated_assurance=on`) != 1 {
		t.Fatalf("migrated store: err = %v, logs:\n%s", err, logs)
	}
	logs, err = build(recorderOAuth2Repository{}, false)
	if err != nil || strings.Count(logs, `msg="auth: oauth2" delegated_assurance=off`) != 1 {
		t.Fatalf("flag off: err = %v, logs:\n%s", err, logs)
	}
}

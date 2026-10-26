package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	audit "github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	relationships "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// errDemoAdminOutsideDevelopment refuses AUTH_DEMO_ADMIN_EMAIL outside development:
// whoever verifies that address as a sign-in email becomes platform admin.
var errDemoAdminOutsideDevelopment = errors.New("auth-cms: AUTH_DEMO_ADMIN_EMAIL is refused outside development — the account that verifies that address becomes platform admin; unset AUTH_DEMO_ADMIN_EMAIL")

// demoAdminEmail reads AUTH_DEMO_ADMIN_EMAIL, refuses it outside development and
// normalizes it as the pocket does (this host injects no IdentifierNormalizer, so
// the pocket uses identifier.DefaultNormalizer). Unset is "" and no error.
func demoAdminEmail(mode environment.Mode, log *slog.Logger) (string, error) {
	raw := environment.GetEnvOrDefault("AUTH_DEMO_ADMIN_EMAIL", "")
	if raw == "" {
		return "", nil
	}
	if mode != environment.ModeDevelopment {
		return "", errDemoAdminOutsideDevelopment
	}
	email, err := identifier.DefaultNormalizer{}.Normalize(string(identifier.KindEmail), raw)
	if err != nil {
		return "", fmt.Errorf("auth-cms: AUTH_DEMO_ADMIN_EMAIL: %w", err)
	}
	log.Warn("AUTH_DEMO_ADMIN_EMAIL set (development only): the account that verifies this address as its sign-in email becomes platform admin", "email", email)
	return email, nil
}

// withDemoAdmin wraps the security-event repository so every recorded event
// checks, until it has granted once, whether the configured address is now a
// verified sign-in email. Every verification and sign-in path records an event,
// so this one seam covers them all; the grant itself is an audited fact written
// through Mutations, never a bypass in the decision path. An empty email
// returns repos unchanged.
func withDemoAdmin(repos auth.Repositories, system *mutations.Service, email string, log *slog.Logger) auth.Repositories {
	if email == "" {
		return repos
	}
	repos.SecurityEvents = &demoAdminGranter{
		SecurityEventRepository: repos.SecurityEvents,
		identifiers:             repos.Identifiers,
		system:                  system,
		email:                   email,
		log:                     log,
	}
	return repos
}

// demoAdminGranter is the security-event repository decorator withDemoAdmin installs.
type demoAdminGranter struct {
	securityevent.SecurityEventRepository
	identifiers identifier.IdentifierRepository
	system      *mutations.Service
	email       string
	log         *slog.Logger

	mu      sync.Mutex
	granted bool
}

// Create appends the row, then runs the one-time grant check.
func (g *demoAdminGranter) Create(ctx context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	stored, err := g.SecurityEventRepository.Create(ctx, evt)
	g.grantOnce(ctx)
	return stored, err
}

func (g *demoAdminGranter) grantOnce(ctx context.Context) {
	// TryLock, not Lock: the grant may itself record an event that re-enters
	// Create, and a concurrent check already covers this moment.
	if !g.mu.TryLock() {
		return
	}
	defer g.mu.Unlock()
	if g.granted {
		return
	}
	login, err := g.identifiers.GetLogin(ctx, string(identifier.KindEmail), g.email)
	if err != nil || login.UserID == "" || login.VerifiedAt.IsZero() || !login.LoginEnabled {
		return
	}
	ctx = audit.WithSource(ctx, audit.Source{System: "demo-admin"})
	if _, err := g.system.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: platformResourceType,
		ResourceID:   platformResourceID,
		Relation:     "admin",
		Subject:      relationships.SubjectRef{Type: "user", ID: login.UserID},
	}); err != nil {
		g.log.WarnContext(ctx, "AUTH_DEMO_ADMIN_EMAIL platform-admin grant failed; retrying on the next event", "error", err)
		return
	}
	g.granted = true
	g.log.WarnContext(ctx, "AUTH_DEMO_ADMIN_EMAIL granted platform:main#admin (development only)", "user_id", login.UserID)
}

package authorization

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type nilRelationshipDependency struct{ relationships.Storer }
type nilRoleDependency struct{ roles.Storer }
type nilAuditReader struct{ audit.Reader }

func TestNewServiceRejectsTypedNilDependencies(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Repositories, *[]Option)
	}{
		{"Repositories.Relationships", func(repos *Repositories, opts *[]Option) {
			repos.Relationships = (*nilRelationshipDependency)(nil)
			*opts = append(*opts, WithRelationshipModel(lifecycleModel()))
		}},
		{"Repositories.Roles", func(repos *Repositories, _ *[]Option) { repos.Roles = (*nilRoleDependency)(nil) }},
		{"Repositories.Mutations", func(repos *Repositories, _ *[]Option) { repos.Mutations = (*stubMutationRepo)(nil) }},
		{"WithGuard", func(_ *Repositories, opts *[]Option) { *opts = append(*opts, WithGuard((*stubGuard)(nil))) }},
		{"Repositories.Audit", func(repos *Repositories, _ *[]Option) { repos.Audit = (*nilAuditReader)(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repos := Repositories{Roles: memory.New().Roles()}
			cfg := []Option{}
			tc.set(&repos, &cfg)
			components, err := New(repos, cfg...)
			if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), tc.name+" is typed nil") {
				t.Fatalf("NewService = %v; want named typed-nil boot failure", err)
			}
			if components.Decisions != nil || components.Roles != nil || components.SystemMutator != nil || components.RelationshipWriter != nil {
				t.Fatal("failed constructor returned usable components")
			}
		})
	}
}

func TestNewServicePreservesOptionalNilAndCapturesDefaultLogger(t *testing.T) {
	logger := slog.Default()
	components, err := New(Repositories{Roles: memory.New().Roles()})
	if err != nil {
		t.Fatalf("roles-only optional nil wiring: %v", err)
	}
	if components.log != logger {
		t.Fatal("both components must capture the constructor's default logger")
	}
	if err := components.Register(pockets.Mount{}); err != nil {
		t.Fatalf("intentional headless Register: %v", err)
	}
	_, err = components.Mutations.AssignRole(context.Background(), actorU1(), mutations.AssignRoleCommand{
		Role: "viewer", Subject: authmodel.PrincipalRef{Type: "user", ID: "u1"},
	})
	if !errors.Is(err, mutations.ErrMutationsNotConfigured) {
		t.Fatalf("unwired actor writes = %v; want read-only posture", err)
	}
}

func TestConstructorLoggerAppliesHeadlesslyAndSurvivesMount(t *testing.T) {
	var configured, mounted bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&configured, nil))
	store := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
	components, err := New(Repositories{Relationships: store.Relationships(), Roles: store.Roles(), Mutations: store.Mutations()}, WithRelationshipModel(lifecycleModel()), WithGuard(&stubGuard{}), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = components.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "owner", Subject: subjU("u1"),
	})
	if err != nil {
		t.Fatalf("headless actor mutation: %v", err)
	}
	configured.Reset()
	if err := components.Register(pockets.Mount{Logger: slog.New(slog.NewTextHandler(&mounted, nil))}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(configured.String(), "level=WARN") || !strings.Contains(configured.String(), "role_routes=false") {
		t.Fatalf("intentional headless mount must log its state without warning: %s", configured.String())
	}
	_, err = components.SystemMutator.TeardownResourceAuthorization(ctx, mutations.TeardownResourceAuthorizationCommand{
		ResourceType: "doc", ResourceID: "d1", Reason: "test resource removed",
	})
	if err != nil {
		t.Fatalf("trusted teardown: %v", err)
	}
	if !strings.Contains(configured.String(), "authorization resource teardown") || !strings.Contains(configured.String(), "test resource removed") {
		t.Fatalf("trusted teardown did not use the constructor logger: %s", configured.String())
	}
	if mounted.Len() != 0 {
		t.Fatalf("Mount.Logger replaced constructor policy: %s", mounted.String())
	}
}

func TestRegisterRejectsTypedNilRouterWhenRoutesConfigured(t *testing.T) {
	store := memory.New()
	components, err := New(Repositories{Roles: store.Roles(), Mutations: store.Mutations()}, WithGuard(&stubGuard{}), WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate}))
	if err != nil {
		t.Fatal(err)
	}
	if err := components.Register(pockets.Mount{Router: (*web.WebHandler)(nil)}); !errors.Is(err, authorizationhttp.ErrRoleRoutesWithoutRouter) {
		t.Fatalf("Register typed nil router = %v", err)
	}
}

func TestPermissionGatesRejectInvalidInputsAtMount(t *testing.T) {
	components := newGPSHost(t, memory.New(), gpsRoleModel())
	for name, mount := range map[string]func(){
		"nil resolver":       func() { components.HTTP.RequirePermission("view", nil) },
		"invalid permission": func() { components.HTTP.RequirePermission("bad\n", authorizationhttp.FixedResource("page", "1")) },
		"invalid fixed ID":   func() { components.HTTP.RequirePermissionFixed("page", "view", "bad\n") },
		"oversized fixed ID": func() {
			components.HTTP.RequirePermissionFixed("page", "view", strings.Repeat("x", authmodel.MaxRefFieldLen+1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid gate must fail before receiving a request")
				}
			}()
			mount()
		})
	}
}

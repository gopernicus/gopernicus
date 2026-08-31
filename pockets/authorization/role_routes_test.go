package authorization

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// allowRoleRouteGuard is the permissive MutationGuard the role-route wiring
// tests use where the guard's DECISION is not what is under test. The route
// tests that care about denial supply their own.
type allowRoleRouteGuard struct{}

func (allowRoleRouteGuard) AuthorizeMutation(context.Context, MutationAttempt, DecisionView) error {
	return nil
}

// passRoleRouteGate is a no-op host gate: it authenticates and authorizes
// nothing, and exists only so a Config carries a NON-NIL RoleRoutesGate.
func passRoleRouteGate(next http.Handler) http.Handler { return next }

// refuseAssignment is a stand-in AssignmentPolicy for the construction matrix.
func refuseAssignment(context.Context, AssignRoleCommand) error {
	return sdk.ErrForbidden
}

// TestNewServiceRoleRoutesConstructionMatrix pins every row of the bundled
// role-administration wiring matrix: each contradictory posture fails
// construction by its own named sentinel, and each legal posture builds.
func TestNewServiceRoleRoutesConstructionMatrix(t *testing.T) {
	tests := []struct {
		name    string
		repos   func(*memstore.Store) Repositories
		cfg     Config
		wantErr error
	}{
		{
			name: "gate without the roles kind",
			repos: func(s *memstore.Store) Repositories {
				return Repositories{Relationships: &relFake{}, Mutations: s.Mutations()}
			},
			cfg: Config{
				RelationshipModel: validModel(),
				Guard:             allowRoleRouteGuard{},
				RoleRoutesGate:    passRoleRouteGate,
			},
			wantErr: ErrRoleRoutesGateWithoutRoles,
		},
		{
			name:    "gate without a guard",
			repos:   func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:     Config{RoleRoutesGate: passRoleRouteGate},
			wantErr: ErrRoleRoutesGateWithoutGuard,
		},
		{
			name:    "assignment policy without the routes",
			repos:   func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:     Config{Guard: allowRoleRouteGuard{}, AssignmentPolicy: refuseAssignment},
			wantErr: ErrAssignmentPolicyWithoutRoutes,
		},
		{
			name:    "unknown list strategy",
			repos:   func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:     Config{Guard: allowRoleRouteGuard{}, RoleRoutesGate: passRoleRouteGate, ListStrategy: "keyset"},
			wantErr: ErrInvalidListStrategy,
		},
		{
			name:  "unknown list strategy is rejected even when orphaned by no gate",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles()} },
			cfg:   Config{ListStrategy: "keyset"},
			// An invalid enum is a typo, never a posture — the orphan rule silences
			// only a VALID unused value.
			wantErr: ErrInvalidListStrategy,
		},
		{
			name:  "gate with roles and a guard",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:   Config{Guard: allowRoleRouteGuard{}, RoleRoutesGate: passRoleRouteGate},
		},
		{
			name:  "gate with an assignment policy and an offset strategy",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg: Config{
				Guard:            allowRoleRouteGuard{},
				RoleRoutesGate:   passRoleRouteGate,
				AssignmentPolicy: refuseAssignment,
				ListStrategy:     crud.StrategyOffset,
			},
		},
		{
			name:  "no gate at all is unchanged",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles(), Mutations: s.Mutations()} },
			cfg:   Config{Guard: allowRoleRouteGuard{}},
		},
		{
			name:  "a valid but unused list strategy is a silent cosmetic orphan",
			repos: func(s *memstore.Store) Repositories { return Repositories{Roles: s.Roles()} },
			cfg:   Config{ListStrategy: crud.StrategyOffset},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			_, err := NewService(tc.repos(store), tc.cfg)
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("NewService error = %v, want %v", err, tc.wantErr)
			case tc.wantErr == nil && err != nil:
				t.Fatalf("NewService: %v", err)
			}
		})
	}
}

// TestServiceCapturesRoleRouteConfig proves the three new Config fields reach
// the Service, so Register has everything the mount needs.
func TestServiceCapturesRoleRouteConfig(t *testing.T) {
	store := memstore.New()
	comps, err := NewService(
		Repositories{Roles: store.Roles(), Mutations: store.Mutations()},
		Config{
			Guard:            allowRoleRouteGuard{},
			RoleRoutesGate:   passRoleRouteGate,
			AssignmentPolicy: refuseAssignment,
			ListStrategy:     crud.StrategyOffset,
		},
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service
	if svc.roleRoutesGate == nil {
		t.Error("roleRoutesGate not captured")
	}
	if svc.assignmentPolicy == nil {
		t.Error("assignmentPolicy not captured")
	}
	if svc.listStrategy != crud.StrategyOffset {
		t.Errorf("listStrategy = %q, want %q", svc.listStrategy, crud.StrategyOffset)
	}
}

// TestValidateListStrategy pins the accepted set directly, including the zero
// value that resolves to cursor at the transport.
func TestValidateListStrategy(t *testing.T) {
	for _, ok := range []crud.Strategy{"", crud.StrategyCursor, crud.StrategyOffset} {
		if err := validateListStrategy(ok); err != nil {
			t.Errorf("validateListStrategy(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []crud.Strategy{"keyset", "CURSOR", "page"} {
		if err := validateListStrategy(bad); !errors.Is(err, ErrInvalidListStrategy) {
			t.Errorf("validateListStrategy(%q) = %v, want ErrInvalidListStrategy", bad, err)
		}
	}
}

// TestRoleRouteSentinelsWrapNoSDKKind pins the construction sentinels as
// BOOT-time faults: they carry no sdk taxonomy kind, so an operator sees a
// startup failure rather than an HTTP status class.
func TestRoleRouteSentinelsWrapNoSDKKind(t *testing.T) {
	sentinels := []error{
		ErrRoleRoutesGateWithoutRoles,
		ErrRoleRoutesGateWithoutGuard,
		ErrAssignmentPolicyWithoutRoutes,
		ErrInvalidListStrategy,
		ErrRoleRoutesWithoutRouter,
	}
	kinds := []error{sdk.ErrInvalidInput, sdk.ErrForbidden, sdk.ErrUnauthorized, sdk.ErrNotFound, sdk.ErrConflict}
	for _, s := range sentinels {
		for _, k := range kinds {
			if errors.Is(s, k) {
				t.Errorf("%v wraps sdk kind %v; construction faults carry none", s, k)
			}
		}
	}
}

// webMiddlewareCompiles keeps the Config field's declared type honest: the gate
// is exactly an sdk web.Middleware, assignable from a plain wrapper.
var _ web.Middleware = passRoleRouteGate

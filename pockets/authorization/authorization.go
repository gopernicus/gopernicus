// Package authorization composes independently usable authorization services.
//
// New returns named Components: relationship checks and tuple reads, role reads,
// permission decisions across both models, guarded mutations, and an optional
// HTTP adapter. Hosts can also construct these services directly from their
// public logic packages and mount the public inbound/http handlers themselves.
//
// Relationships require a schema. Roles may remain opaque assignments, or use a
// RoleModel to participate in permission decisions. Each permission pair belongs
// to exactly one model. Models are immutable after construction, and composed
// services share the same resolved evaluation budgets.
//
// Give request code only the services it needs. RelationshipWriter and
// SystemMutator are separately held trusted capabilities returned at composition;
// ordinary services cannot produce them. Actor-facing writes require a host
// MutationGuard and an atomic MutationRepository. No default policy grants access.
//
// Components.Register mounts bundled role-administration routes only when
// RoleRoutes.Gate is configured. Permission middleware and individually
// mountable handlers belong to inbound/http, whose public constructor validates
// the same route posture and dependencies. The pocket owns no database connection
// or migration lifecycle.
package authorization

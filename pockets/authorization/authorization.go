// Package authorization composes exact roles and optional graph permissions over
// one canonical tuple authority. New returns independently usable read services,
// one decision evaluator, guarded mutations and an optional HTTP adapter.
//
// Repositories.Tuples is required. WithModel supplies optional named expressions
// and explicit relation subject-shape constraints. HasRole checks exact global
// membership; HasRoleIn checks exact resource membership with no fallback.
//
// Keep RelationshipWriter, RoleWriter and SystemMutator at host composition.
// Actor-facing writes require a MutationGuard and serialized MutationRepository.
// Guards read through their callback view; no default policy grants access.
//
// Register mounts role administration only when its host gate is configured.
// Constructors own no connection, migration or background-worker lifecycle.
package authorization

// Package authorization composes exact roles and optional graph permissions over
// one canonical tuple authority. New returns read services, a decision evaluator,
// one principal-free tuple write service and an optional HTTP adapter.
//
// Repositories.Tuples is required. WithModel supplies named expressions and
// explicit subject-shape constraints. Principal access policy belongs to inbound;
// data IntegrityPolicy is enforced atomically by the configured repository.
//
// Register mounts role administration only with a host Gate and WritePolicy.
// Constructors own no connection, migration or background-worker lifecycle.
package authorization

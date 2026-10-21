package externalsubject

import "context"

// Repository persists the external subject mirror. New rows normally arrive
// with their user through user.UserRepository.Provision (InitialCredentials.External);
// Link is the explicit administrative path to an existing account.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for an unknown (provider, subject) → sdk.ErrNotFound.
//   - Link whose (Provider, Subject) is already mapped → sdk.ErrAlreadyExists;
//     Link to a missing or inactive user fails without writing.
//   - ListByUser returns an empty slice, never an error, for an unknown user.
//   - Unlink for an unknown (provider, subject) → sdk.ErrNotFound.
type Repository interface {
	// Get returns the mapping for a provider subject, or sdk.ErrNotFound.
	Get(ctx context.Context, provider, subject string) (ExternalSubject, error)
	// Link maps a provider subject to an existing ACTIVE user. It never adopts,
	// retires identifiers or revokes sessions.
	Link(ctx context.Context, s ExternalSubject) (ExternalSubject, error)
	// ListByUser returns every mapping owned by userID.
	ListByUser(ctx context.Context, userID string) ([]ExternalSubject, error)
	// Unlink removes a mapping, or returns sdk.ErrNotFound.
	Unlink(ctx context.Context, provider, subject string) error
}

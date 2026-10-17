// Package secrets assembles the vault and rotation services over one
// repository and one wrapper. Hosts may construct the logic packages directly.
package secrets

import (
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/secrets/logic/rotation"
	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/sdk"
)

// Repositories supplies the persistence the pocket needs.
type Repositories struct {
	Secrets vault.Repository
}

// Components holds the built services. Both share the repository and wrapper.
type Components struct {
	Vault    *vault.Service
	Rotation *rotation.Service
}

// New validates the repository and the wrapper and builds both services. It
// starts nothing and registers no routes.
func New(repos Repositories, opts ...Option) (*Components, error) {
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("secrets: nil constructor option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	v, err := vault.NewService(repos.Secrets, cfg.wrapper, vault.WithClock(cfg.clock))
	if err != nil {
		return nil, err
	}
	r, err := rotation.NewService(repos.Secrets, cfg.wrapper, rotation.WithClock(cfg.clock))
	if err != nil {
		return nil, err
	}
	return &Components{Vault: v, Rotation: r}, nil
}

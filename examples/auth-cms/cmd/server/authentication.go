package main

import (
	"log/slog"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// authenticationConfig is this demo host's staged wiring and environment input.
// The pocket accepts required inputs and named feature options; embedding its
// policy records lets the host parse the same AUTH_* environment keys together.
// RuntimeMode carries no env tag: main resolves it once with LoadEnvForMode.
type authenticationConfig struct {
	TokenSigner  cryptids.JWTSigner
	RuntimeMode  environment.Mode
	DeliveryMode delivery.Mode `env:"AUTH_DELIVERY_MODE"`
	IDs          sdk.IDGenerator
	Logger       *slog.Logger
	auth.PasswordConfig
	auth.SessionsConfig
	auth.IdentityConfig
	auth.AbuseProtectionConfig
	auth.DeliveryConfig
	auth.MessagesConfig
	auth.OAuthConfig
	auth.PasswordlessConfig
	auth.LinksConfig
	auth.BrowserConfig
	auth.InvitationsConfig
	auth.AdministrationConfig
	auth.MaintenanceConfig
	// Passkeys is a named field, not embedded: PasskeyConfig.Views would collide
	// with BrowserConfig.Views.
	Passkeys auth.PasskeyConfig
	// TOTP is named for the same reason.
	TOTP auth.TOTPConfig
}

func (c authenticationConfig) options() []auth.Option {
	return []auth.Option{
		auth.WithPassword(c.PasswordConfig),
		auth.WithSessions(c.SessionsConfig),
		auth.WithIdentity(c.IdentityConfig),
		auth.WithAbuseProtection(c.AbuseProtectionConfig),
		auth.WithDelivery(c.DeliveryConfig),
		auth.WithMessages(c.MessagesConfig),
		auth.WithOAuth(c.OAuthConfig),
		auth.WithPasswordless(c.PasswordlessConfig),
		auth.WithLinks(c.LinksConfig),
		auth.WithBrowser(c.BrowserConfig),
		auth.WithInvitations(c.InvitationsConfig),
		auth.WithAdministration(c.AdministrationConfig),
		auth.WithMaintenance(c.MaintenanceConfig),
		auth.WithPasskeys(c.Passkeys),
		auth.WithTOTP(c.TOTP),
		auth.WithIDs(c.IDs),
		auth.WithLogger(c.Logger),
	}
}

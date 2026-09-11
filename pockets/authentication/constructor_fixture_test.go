package authentication

func newFixture(repos Repositories, cfg constructorConfig) (*Components, error) {
	return New(repos,
		cfg.TokenSigner,
		cfg.RuntimeMode,
		cfg.DeliveryMode,
		WithPassword(PasswordConfig{Hasher: cfg.Hasher, ValidatePassword: cfg.ValidatePassword, CompromisedPasswordChecker: cfg.CompromisedPasswordChecker, CompromisedPasswordFailOpen: cfg.CompromisedPasswordFailOpen, PasswordFlowsDisabled: cfg.PasswordFlowsDisabled, RequireVerifiedEmail: cfg.RequireVerifiedEmail}),
		WithSessions(SessionsConfig{AccessTokenTTL: cfg.AccessTokenTTL, RefreshTTL: cfg.RefreshTTL}),
		WithIdentity(IdentityConfig{ChallengeProtector: cfg.ChallengeProtector, IdentifierNormalizer: cfg.IdentifierNormalizer, IdentifierKeyer: cfg.IdentifierKeyer, CredentialPolicy: cfg.CredentialPolicy}),
		WithAbuseProtection(AbuseProtectionConfig{AuthenticationLimits: cfg.AuthenticationLimits, RateLimiter: cfg.RateLimiter}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer, MailFrom: cfg.MailFrom, BodySenders: cfg.BodySenders, DeliveryEncrypter: cfg.DeliveryEncrypter, DeliveryDispatcher: cfg.DeliveryDispatcher, DeliveryEventsEmitter: cfg.DeliveryEventsEmitter, DeliveryJobsAcknowledged: cfg.DeliveryJobsAcknowledged, DeliveryEphemeralAcknowledged: cfg.DeliveryEphemeralAcknowledged, InProcessDelivery: cfg.InProcessDelivery}),
		WithMessages(MessagesConfig{EmailContentTemplates: cfg.EmailContentTemplates, EmailLayouts: cfg.EmailLayouts, EmailBranding: cfg.EmailBranding, DeliveryData: cfg.DeliveryData, EmailSubjects: cfg.EmailSubjects, SMSBodies: cfg.SMSBodies}),
		WithOAuth(OAuthConfig{Providers: cfg.Providers, TokenEncrypter: cfg.TokenEncrypter, OAuthCallbackBase: cfg.OAuthCallbackBase, OAuthNativeRedirectURIs: cfg.OAuthNativeRedirectURIs, TrustOAuthEmail: cfg.TrustOAuthEmail}),
		WithPasswordless(PasswordlessConfig{Passwordless: cfg.Passwordless, PasswordlessProvisionOnRedeem: cfg.PasswordlessProvisionOnRedeem}),
		WithLinks(LinksConfig{PublicAuthBaseURL: cfg.PublicAuthBaseURL, PasswordResetURL: cfg.PasswordResetURL, OAuthLinkBaseURL: cfg.OAuthLinkBaseURL, RedirectAllowlist: cfg.RedirectAllowlist}),
		WithBrowser(BrowserConfig{SessionCookie: cfg.SessionCookie, RefreshCookiePath: cfg.RefreshCookiePath, AllowedOrigins: cfg.AllowedOrigins, BrowserLoginPath: cfg.BrowserLoginPath, BundledRouteAuth: cfg.BundledRouteAuth, Views: cfg.Views, HTMLPolicy: cfg.HTMLPolicy}),
		WithInvitations(InvitationsConfig{Granter: cfg.Granter, InviteCheck: cfg.InviteCheck, MemberCheck: cfg.MemberCheck}),
		WithAdministration(AdministrationConfig{MachineRoutesGate: cfg.MachineRoutesGate, UserAdminCheck: cfg.UserAdminCheck, ListStrategy: cfg.ListStrategy}),
		WithIDs(cfg.IDs),
		WithLogger(cfg.Logger),
	)
}

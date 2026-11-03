package authentication

// fixtureMailFrom is the From address newFixture supplies when a case leaves
// MailFrom empty, so the cases written before New required one keep testing
// their own dimension; delivery_role_test.go calls New directly to cover it.
const fixtureMailFrom = "no-reply@example.com"

func newFixture(repos Repositories, cfg constructorConfig) (*Components, error) {
	if cfg.MailFrom == "" {
		cfg.MailFrom = fixtureMailFrom
	}
	opts := []Option{
		WithPassword(PasswordConfig{Hasher: cfg.Hasher, ValidatePassword: cfg.ValidatePassword, CompromisedPasswordChecker: cfg.CompromisedPasswordChecker, CompromisedPasswordFailOpen: cfg.CompromisedPasswordFailOpen, PasswordFlowsDisabled: cfg.PasswordFlowsDisabled, RequireVerifiedEmail: cfg.RequireVerifiedEmail}),
		WithSessions(SessionsConfig{AccessTokenTTL: cfg.AccessTokenTTL, RefreshTTL: cfg.RefreshTTL, IdleTimeout: cfg.IdleTimeout, Issuer: cfg.TokenIssuer, Audience: cfg.TokenAudience}),
		WithIdentity(IdentityConfig{ChallengeProtector: cfg.ChallengeProtector, IdentifierNormalizer: cfg.IdentifierNormalizer, IdentifierKeyer: cfg.IdentifierKeyer, CredentialPolicy: cfg.CredentialPolicy, SingleFactorAcknowledged: cfg.SingleFactorAcknowledged}),
		WithAbuseProtection(AbuseProtectionConfig{AuthenticationLimits: cfg.AuthenticationLimits, PublicRouteLimits: cfg.PublicRouteLimits, RateLimiter: cfg.RateLimiter, PublicRouteLimiterFailOpen: cfg.PublicRouteLimiterFailOpen}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer, MailFrom: cfg.MailFrom, BodySenders: cfg.BodySenders, DeliveryEncrypter: cfg.DeliveryEncrypter, DeliveryDispatcher: cfg.DeliveryDispatcher, DeliveryEventsEmitter: cfg.DeliveryEventsEmitter, DeliveryJobsAcknowledged: cfg.DeliveryJobsAcknowledged, DeliveryEphemeralAcknowledged: cfg.DeliveryEphemeralAcknowledged, OpportunisticTLSAcknowledged: cfg.OpportunisticTLSAcknowledged, InProcessDelivery: cfg.InProcessDelivery, Role: cfg.DeliveryRole, JobsDelivery: cfg.JobsDelivery}),
		WithMessages(MessagesConfig{EmailContentTemplates: cfg.EmailContentTemplates, EmailLayouts: cfg.EmailLayouts, EmailBranding: cfg.EmailBranding, DeliveryData: cfg.DeliveryData, EmailSubjects: cfg.EmailSubjects, SMSBodies: cfg.SMSBodies}),
		WithOAuth(OAuthConfig{Providers: cfg.Providers, TokenEncrypter: cfg.TokenEncrypter, OAuthCallbackBase: cfg.OAuthCallbackBase, OAuthNativeRedirectURIs: cfg.OAuthNativeRedirectURIs, TrustOAuthEmail: cfg.TrustOAuthEmail}),
		WithPasswordless(PasswordlessConfig{Passwordless: cfg.Passwordless, PasswordlessProvisionOnRedeem: cfg.PasswordlessProvisionOnRedeem}),
		WithLinks(LinksConfig{PublicAuthBaseURL: cfg.PublicAuthBaseURL, PasswordResetURL: cfg.PasswordResetURL, OAuthLinkBaseURL: cfg.OAuthLinkBaseURL, InvitationURL: cfg.InvitationURL, MemberAddedURL: cfg.MemberAddedURL, InvitationTokenInQuery: cfg.InvitationTokenInQuery, RedirectAllowlist: cfg.RedirectAllowlist}),
		WithBrowser(BrowserConfig{SessionCookie: cfg.SessionCookie, CookieDomainAcknowledged: cfg.CookieDomainAcknowledged, RefreshCookiePath: cfg.RefreshCookiePath, AllowedOrigins: cfg.AllowedOrigins, OriginPolicy: cfg.OriginPolicy, BrowserLoginPath: cfg.BrowserLoginPath, BundledRouteAuth: cfg.BundledRouteAuth, BundledRouteAuthLivenessFloorRelaxed: cfg.LivenessFloorRelaxed, Views: cfg.Views, HTMLPolicy: cfg.HTMLPolicy}),
		WithInvitations(InvitationsConfig{Granter: cfg.Granter, InviteCheck: cfg.InviteCheck, ResourcePermissions: cfg.ResourcePermissions, Can: cfg.Can, MemberCheck: cfg.MemberCheck, Fencer: cfg.Fencer}),
		WithAdministration(AdministrationConfig{MachineRoutesGate: cfg.MachineRoutesGate, UserAdminCheck: cfg.UserAdminCheck, ListStrategy: cfg.ListStrategy, SecurityEventsRoute: cfg.SecurityEventsRoute}),
		WithIDs(cfg.IDs),
		WithLogger(cfg.Logger),
	}
	if cfg.AuditDisabledAcknowledged {
		opts = append(opts, WithAuditDisabledAcknowledged())
	}
	return New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, opts...)
}

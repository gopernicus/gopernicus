package delivery

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// coreTemplates are the feature's default email content templates, registered at
// email.LayerCore. Hosts override individual templates at email.LayerApp through
// Deps.AppTemplates (design §6.2). The registry walks the "templates" subdirectory
// and exposes each file as "<namespace>:<name>".
//
//go:embed templates/*.html
var coreTemplates embed.FS

// Namespace is the email TemplateRegistry namespace the feature registers its
// content templates under; a rendered template is addressed as
// "authentication:<name>". It is exported so a host can build a LayerApp
// TemplateOverride that targets a bundled core template by that name (design
// §6.2); the public authentication.EmailContentNamespace aliases it.
const Namespace = "authentication"

// namespace is the internal spelling kept for the existing call sites.
const namespace = Namespace

// Delivery purposes. A purpose is the template selector shared by both the email
// (LayerCore templates) and the body-only SMS rails; it is deliberately decoupled
// from a challenge purpose so one delivery template can serve several challenge
// kinds (§6.2 enumerates the core set).
const (
	// PurposeRegistrationVerification carries the registration email-verification code.
	PurposeRegistrationVerification = "registration_verification"
	// PurposePasswordReset carries the password-reset token.
	PurposePasswordReset = "password_reset"
	// PurposeOAuthPendingLink carries the pending-OAuth-link confirmation token.
	PurposeOAuthPendingLink = "oauth_pending_link"
	// PurposeMagicLink carries a single-use passwordless sign-in link.
	PurposeMagicLink = "magic_link"
	// PurposeLoginCode carries a passwordless one-time sign-in code (the OTP login
	// rail, design §4.3). It is a distinct purpose from PurposeSensitiveCode so the
	// opaque-start worker routes an OTP login start onto its own initializer and the
	// login code carries a sign-in-worded template rather than a sensitive-op one.
	PurposeLoginCode = "login_code"
	// PurposeSensitiveCode carries a step-up / sensitive-operation confirmation code.
	PurposeSensitiveCode = "sensitive_code"
	// PurposeIdentifierChangeProof carries the ownership-proof code for adding or
	// changing an identifier.
	PurposeIdentifierChangeProof = "identifier_change_proof"
	// PurposeIdentifierChangeNotice carries the old-address security notice sent
	// when an identifier is changed (no secret).
	PurposeIdentifierChangeNotice = "identifier_change_notice"
	// PurposeInvitation carries a resource-invitation secret/link.
	PurposeInvitation = "invitation"
	// PurposeMemberAdded carries the you-were-added notice for a direct add.
	PurposeMemberAdded = "member_added"
)

// Stable, kind-taggable construction/render errors. Each wraps an sdk error kind
// so the transport maps it and callers match with errors.Is — never string
// parsing.
var (
	// ErrMailerRequired is returned by NewRouter when no email Sender is supplied:
	// email is always-on via the required Mailer (§6.1), so the router cannot exist
	// without it.
	ErrMailerRequired = fmt.Errorf("delivery: email sender is required: %w", sdk.ErrInvalidInput)
	// ErrRouterRequired is returned by NewJobsProcessor when no Router is supplied: the
	// delivery processor renders and sends through the kind-aware router, so it cannot
	// exist without it.
	ErrRouterRequired = fmt.Errorf("delivery: router is required: %w", sdk.ErrInvalidInput)
	// ErrUnknownPurpose is returned by Render for a purpose with no registered
	// template.
	ErrUnknownPurpose = fmt.Errorf("delivery: unknown purpose: %w", sdk.ErrInvalidInput)
	// ErrKindUnsupported is returned by Render when a purpose has no template for
	// the requested kind (e.g. an email-only purpose rendered for SMS), and by
	// Deliver when no transport is wired for a non-email kind (deny-by-absence).
	ErrKindUnsupported = fmt.Errorf("delivery: kind not supported: %w", sdk.ErrInvalidInput)
)

// DeliveryError tags a transport failure with the delivery kind so a host's
// worker diagnostics can classify the failure by kind without parsing strings. It
// wraps the underlying transport error, so errors.Is/Unwrap pass through to the
// cause (including context.Canceled / context.DeadlineExceeded when a notifier
// honors cancellation).
type DeliveryError struct {
	Kind string
	Err  error
}

// Error renders a kind-tagged, secret-free failure summary. The underlying error
// is a transport error only — never a rendered body or secret, which live in the
// sealed Envelope and never reach this path.
func (e *DeliveryError) Error() string {
	return fmt.Sprintf("delivery via %q failed: %v", e.Kind, e.Err)
}

// Unwrap exposes the transport cause for errors.Is/As.
func (e *DeliveryError) Unwrap() error { return e.Err }

// spec binds one purpose to its email content template, its subject line, and its
// optional body-only SMS template. An empty sms means the purpose is email-only —
// rendering it for a non-email kind is ErrKindUnsupported.
type spec struct {
	template string
	subject  string
	sms      string
	layout   email.LayoutType
}

// specs is the single source of truth for the core delivery set (§6.2/§6.3). Email
// content is the registered LayerCore template; the SMS body is a short in-core
// plain-text template (email-layout HTML is never rendered into an SMS body).
var specs = map[string]spec{
	PurposeRegistrationVerification: {template: "verification", subject: "Verify your email", layout: email.LayoutTransactional},
	PurposePasswordReset:            {template: "password_reset", subject: "Reset your password", layout: email.LayoutTransactional},
	PurposeOAuthPendingLink:         {template: "oauth_pending_link", subject: "Confirm linking your {{.ProviderName}} account", layout: email.LayoutTransactional},
	PurposeMagicLink:                {template: "magic_link", subject: "Your sign-in link", sms: "Sign in: {{.Link}}", layout: email.LayoutTransactional},
	PurposeLoginCode:                {template: "login_code", subject: "Your sign-in code", sms: "Your sign-in code is {{.Secret}}", layout: email.LayoutTransactional},
	PurposeSensitiveCode:            {template: "sensitive_code", subject: "Your verification code", sms: "Your verification code is {{.Secret}}", layout: email.LayoutTransactional},
	PurposeIdentifierChangeProof:    {template: "identifier_change_proof", subject: "Confirm your {{.IdentifierKind}}", sms: "Your confirmation code is {{.Secret}}", layout: email.LayoutTransactional},
	PurposeIdentifierChangeNotice:   {template: "identifier_change_notice", subject: "Your {{.IdentifierKind}} was changed", sms: "The {{.IdentifierKind}} on your account was changed. If this was not you, secure your account.", layout: email.LayoutTransactional},
	PurposeInvitation:               {template: "invitation", subject: "You have an invitation", sms: "You were invited to {{.ResourceType}} {{.ResourceID}} as {{.Relation}}: {{.Link}}", layout: email.LayoutTransactional},
	PurposeMemberAdded:              {template: "member_added", subject: "You were added", sms: "You were added to {{.ResourceType}} {{.ResourceID}} as {{.Relation}}: {{.Link}}", layout: email.LayoutTransactional},
}

// Request is a single delivery instruction handed to Render. Kind is the address
// kind (identity.KindEmail selects the email rail; any other kind selects the
// body-only rail). Purpose selects the template. Destination is the resolved
// address; ResolutionInput is the normalized identifier the worker carried in so
// it need not re-resolve on retry. Secret is the rendered OTP/token/link, made
// available to templates as {{.Secret}} and kept separately so it can be scrubbed
// from diagnostics. Data supplies the remaining template fields (ProviderName,
// Link, ResourceType, …).
type Request struct {
	Kind            string
	Purpose         string
	Destination     string
	ResolutionInput string
	Secret          string
	Data            map[string]any
}

// Deps are the collaborators NewRouter needs. Mailer is required (email is
// always-on); Notifiers is the host's wired delivery set keyed by kind (an
// email-kind entry bridges email through notify instead of the Mailer directly);
// AppTemplates registers host content overrides at email.LayerApp.
type Deps struct {
	Mailer       email.Sender
	MailFrom     string
	Notifiers    map[string]notify.Notifier
	AppTemplates []TemplateOverride
	Logger       *slog.Logger
}

// TemplateOverride registers a host's email content templates at email.LayerApp so
// a host can override any LayerCore default without editing the feature (design
// §6.2). The embed.FS is walked from its "templates" subdirectory, exactly like
// the feature's own core set.
type TemplateOverride struct {
	Namespace string
	FS        embed.FS
}

// Router is the constructor-injected, kind-aware renderer/router shared by authsvc
// and invitationsvc (design §6.1). It owns the deny-by-absence kind policy: email
// renders through the email TemplateRegistry and delivers through the Mailer or a
// bridging email-kind notifier; every other kind renders a body-only message and
// delivers through its wired notifier. Render produces an encrypted-job-ready
// Envelope (it never sends); Deliver performs one context-honoring send through
// the kind's transport.
type Router struct {
	emailer   *email.Emailer
	mailer    email.Sender
	mailFrom  string
	notifiers map[string]notify.Notifier
	subjects  *template.Template
	smsBodies *template.Template
	logger    *slog.Logger
}

// NewRouter builds a Router. A nil Mailer is ErrMailerRequired. It registers the
// feature's LayerCore email templates plus any host LayerApp overrides, and parses
// the in-core subject and SMS body templates once.
func NewRouter(d Deps) (*Router, error) {
	if d.Mailer == nil {
		return nil, ErrMailerRequired
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}

	opts := []email.Option{email.WithContentTemplates(namespace, coreTemplates, email.LayerCore)}
	for _, o := range d.AppTemplates {
		opts = append(opts, email.WithContentTemplates(o.Namespace, o.FS, email.LayerApp))
	}
	emailer, err := email.New(d.Mailer, d.MailFrom, opts...)
	if err != nil {
		return nil, fmt.Errorf("delivery: build emailer: %w", err)
	}

	subjects := template.New("subjects")
	smsBodies := template.New("sms")
	for purpose, sp := range specs {
		if _, err := subjects.New("subject_" + purpose).Parse(sp.subject); err != nil {
			return nil, fmt.Errorf("delivery: parse subject template %q: %w", purpose, err)
		}
		if sp.sms != "" {
			if _, err := smsBodies.New("sms_" + purpose).Parse(sp.sms); err != nil {
				return nil, fmt.Errorf("delivery: parse sms template %q: %w", purpose, err)
			}
		}
	}

	return &Router{
		emailer:   emailer,
		mailer:    d.Mailer,
		mailFrom:  d.MailFrom,
		notifiers: d.Notifiers,
		subjects:  subjects,
		smsBodies: smsBodies,
		logger:    logger,
	}, nil
}

// Render renders req into the plaintext Envelope a delivery job seals into its
// encrypted payload. It never sends (design §6.1.1: request handlers enqueue; the
// worker delivers). For the email kind it renders the LayerCore/LayerApp content
// template wrapped in its layout into HTML and text; for every other kind it
// renders the body-only SMS template — an email-only purpose rendered for a
// non-email kind is ErrKindUnsupported. An unknown purpose is ErrUnknownPurpose.
func (r *Router) Render(ctx context.Context, req Request) (Envelope, error) {
	if err := ctx.Err(); err != nil {
		return Envelope{}, err
	}
	sp, ok := specs[req.Purpose]
	if !ok {
		return Envelope{}, fmt.Errorf("%w: %q", ErrUnknownPurpose, req.Purpose)
	}

	data := make(map[string]any, len(req.Data)+1)
	for k, v := range req.Data {
		data[k] = v
	}
	if _, exists := data["Secret"]; !exists && req.Secret != "" {
		data["Secret"] = req.Secret
	}

	subject, err := r.renderNamed(r.subjects, "subject_"+req.Purpose, data)
	if err != nil {
		return Envelope{}, fmt.Errorf("delivery: render subject %q: %w", req.Purpose, err)
	}

	env := Envelope{
		Destination:     req.Destination,
		ResolutionInput: req.ResolutionInput,
		Secret:          req.Secret,
	}

	if req.Kind == identity.KindEmail {
		data["Subject"] = subject
		html, text, err := r.emailer.Render(namespace+":"+sp.template, data, email.WithLayout(sp.layout))
		if err != nil {
			return Envelope{}, fmt.Errorf("delivery: render email %q: %w", req.Purpose, err)
		}
		env.Subject = subject
		env.Body = text
		env.HTML = html
		return env, nil
	}

	// Non-email kinds ride the body-only rail (design §6.3): no HTML layout, and no
	// subject — an SMS has none, and the Envelope leaves Subject empty for it.
	if sp.sms == "" {
		return Envelope{}, fmt.Errorf("%w: purpose %q has no %s template", ErrKindUnsupported, req.Purpose, req.Kind)
	}
	body, err := r.renderNamed(r.smsBodies, "sms_"+req.Purpose, data)
	if err != nil {
		return Envelope{}, fmt.Errorf("delivery: render sms %q: %w", req.Purpose, err)
	}
	env.Body = body
	return env, nil
}

// Supports reports whether a transport is wired for kind (design §6.3,
// deny-by-absence). Email is always supported (the Mailer is required); every
// other kind is supported only when a notifier of that kind is wired. A start
// site consults it to reject an add/change targeting an unsupported kind
// (ErrKindNotSupported) BEFORE it creates any flow state or delivers a secret.
func (r *Router) Supports(kind string) bool {
	if kind == identity.KindEmail {
		return true
	}
	_, ok := r.notifiers[kind]
	return ok
}

// Deliver performs one send of an already-rendered Envelope through the transport
// for kind, honoring ctx cancellation (the worker bounds ctx with a provider
// deadline; this method simply passes it through and reports a canceled context
// promptly as a kind-tagged DeliveryError). The email kind delivers through a
// wired email-kind notifier when present (the bridge policy, §6.1) and otherwise
// through the Mailer; every other kind requires a wired notifier of that kind
// (deny-by-absence → ErrKindUnsupported). A transport failure is a DeliveryError
// tagging the kind; the wrapped cause carries no secret.
func (r *Router) Deliver(ctx context.Context, kind string, env Envelope) error {
	if err := ctx.Err(); err != nil {
		return &DeliveryError{Kind: kind, Err: err}
	}

	if kind == identity.KindEmail {
		if n, ok := r.notifiers[identity.KindEmail]; ok {
			return r.notify(ctx, n, kind, env)
		}
		msg := email.Message{
			From:    r.mailFrom,
			To:      []string{env.Destination},
			Subject: env.Subject,
			Text:    env.Body,
			HTML:    env.HTML,
		}
		if err := r.mailer.Send(ctx, msg); err != nil {
			return &DeliveryError{Kind: kind, Err: err}
		}
		return nil
	}

	n, ok := r.notifiers[kind]
	if !ok {
		return fmt.Errorf("%w: no transport wired for kind %q", ErrKindUnsupported, kind)
	}
	return r.notify(ctx, n, kind, env)
}

// notify delivers env through a notify.Notifier, wrapping a transport failure in a
// kind-tagged DeliveryError.
func (r *Router) notify(ctx context.Context, n notify.Notifier, kind string, env Envelope) error {
	if err := n.Notify(ctx, identity.Address{Kind: kind, Value: env.Destination}, notify.Message{Subject: env.Subject, Body: env.Body}); err != nil {
		return &DeliveryError{Kind: kind, Err: err}
	}
	return nil
}

// renderNamed executes one named subtemplate of root against data.
func (r *Router) renderNamed(root *template.Template, name string, data map[string]any) (string, error) {
	var buf strings.Builder
	if err := root.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

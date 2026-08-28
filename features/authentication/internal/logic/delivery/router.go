package delivery

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"maps"
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

// defaultLayoutDir is the embed.FS subdirectory a LayoutOverride is walked from
// when it names none, mirroring the "templates" default RegisterTemplates applies
// to content overrides.
const defaultLayoutDir = "layouts"

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
	// ErrOverrideInvalid is returned by NewRouter when a Deps.Subjects or
	// Deps.SMSBodies entry cannot be honored: an unknown purpose key, an
	// empty/whitespace-only source, an SMS override for a purpose whose core spec has
	// no SMS rail (an override customizes an existing rail; it never enables a new
	// kind), or a source that fails to parse. The wrapped message names which.
	ErrOverrideInvalid = fmt.Errorf("delivery: invalid subject/sms override: %w", sdk.ErrInvalidInput)
	// ErrDataHookReserved is returned by Render when the host DataHook returns a
	// framework-owned reserved field (Secret, Link, or Subject). The render aborts
	// before any envelope is built, so no host-supplied value can ever replace the
	// rendered credential, the accept link, or the subject.
	ErrDataHookReserved = fmt.Errorf("delivery: data hook returned a reserved field: %w", sdk.ErrInvalidInput)
	// ErrSubjectInvalid is returned by Render when the rendered email subject is
	// empty (after trimming whitespace) or contains a CR or LF — a header-injection
	// hazard. It is checked before an envelope is built.
	ErrSubjectInvalid = fmt.Errorf("delivery: rendered email subject is empty or contains a line break: %w", sdk.ErrInvalidInput)
)

// Reserved template data fields. They are framework-owned: the renderer inserts
// Secret and Subject only after a DataHook succeeds, Link is caller-built, and a
// DataHook may neither add nor replace any of them (ErrDataHookReserved).
const (
	fieldSecret  = "Secret"
	fieldLink    = "Link"
	fieldSubject = "Subject"
)

// DataHook enriches the caller-built, secret-free data for one delivery render.
// purpose is the delivery purpose (Purpose*). data is a fresh defensive copy of
// the caller's fields (a nested map[string]string such as the invitation
// Metadata is copied too); it never contains Secret or Subject, and may contain
// Link for read-only context. The returned map holds additions and replacements
// that are merged BEFORE Secret and Subject are inserted; Secret, Link, and
// Subject are reserved and rejected if returned (ErrDataHookReserved). A nil
// return adds nothing.
//
// The hook may be called concurrently and must not retain or mutate its input
// after returning. An error aborts the render before any envelope is built; how
// that surfaces depends on the caller that renders — see the auth package's
// Config.DeliveryData godoc for the synchronous, best-effort, and worker-side
// cases.
type DataHook func(ctx context.Context, purpose string, data map[string]any) (map[string]any, error)

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
	PurposeInvitation:               {template: "invitation", subject: "You have an invitation", sms: "You were invited to {{.ResourceType}} {{or .ResourceName .ResourceID}} as {{.Relation}}: {{.Link}}", layout: email.LayoutTransactional},
	PurposeMemberAdded:              {template: "member_added", subject: "You were added", sms: "You were added to {{.ResourceType}} {{or .ResourceName .ResourceID}} as {{.Relation}}: {{.Link}}", layout: email.LayoutTransactional},
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
// AppTemplates registers host content overrides at email.LayerApp, and AppLayouts
// registers host LAYOUT overrides at the same layer.
type Deps struct {
	Mailer       email.Sender
	MailFrom     string
	Notifiers    map[string]notify.Notifier
	AppTemplates []TemplateOverride
	// AppLayouts registers host email layouts at email.LayerApp — the highest
	// layer, and the higher layer wins — so a host layout named for a layout type
	// resolves ahead of the sdk's bundled default. Empty (the zero value) → the
	// sdk layouts render exactly as before.
	AppLayouts []LayoutOverride
	// Branding fills the shared email layouts' {{.Brand.*}} values; nil keeps
	// the layouts' own fallback ("Your Company").
	Branding *email.Branding
	// DataHook is the host's per-render data enrichment (nil → the caller-built
	// data renders as-is). It runs once per Render, for every purpose and kind,
	// before the subject, body, and SMS templates execute.
	DataHook DataHook
	// Subjects overrides the in-core email subject template per purpose (key =
	// Purpose*, value = text/template source). Parsed at construction with
	// missing-key errors enabled, so a data-contract mistake fails the render
	// rather than shipping "<no value>". Unknown purpose keys and empty sources
	// are ErrOverrideInvalid.
	Subjects map[string]string
	// SMSBodies overrides the in-core body-only SMS template per purpose, with the
	// same parsing rules as Subjects. A purpose whose core spec has no SMS rail
	// cannot be given one here (ErrOverrideInvalid): an override customizes an
	// existing rail, it never enables a new kind.
	SMSBodies map[string]string
	Logger    *slog.Logger
}

// TemplateOverride registers a host's email content templates at email.LayerApp so
// a host can override any LayerCore default without editing the feature (design
// §6.2). The embed.FS is walked from its "templates" subdirectory, exactly like
// the feature's own core set.
type TemplateOverride struct {
	Namespace string
	FS        embed.FS
}

// LayoutOverride registers a host's email LAYOUTS at email.LayerApp — the sibling
// of TemplateOverride for the frame rather than the body (design §6.2). Every
// delivery purpose renders with email.LayoutTransactional, so a host that ships
// "transactional.html" (and optionally "transactional.txt") brands ALL auth mail;
// the file's base name is the layout type it replaces and the file's own
// {{define "layout:<name>"}} / {{define "layout:<name>.text"}} block names it, per
// the sdk's bundled layouts. Dir is the embed.FS subdirectory to walk; empty →
// "layouts". Zero entries leave the sdk defaults untouched. Resolution picks the
// winning layer's html/text PAIR, not file by file: an override shipping only
// ".html" renders the text half with NO layout at all rather than the sdk's, so
// ship both halves.
type LayoutOverride struct {
	FS  embed.FS
	Dir string
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
	// subjects and smsBodies hold one parsed template per purpose — the host
	// override when Deps supplied one, else the in-core spec. Each is a standalone
	// template so an override's missingkey=error option never leaks onto the core
	// set, whose rendered output stays byte-for-byte what it was.
	subjects  map[string]*template.Template
	smsBodies map[string]*template.Template
	dataHook  DataHook
	logger    *slog.Logger
}

// NewRouter builds a Router. A nil Mailer is ErrMailerRequired. It registers the
// feature's LayerCore email templates plus any host LayerApp content and layout
// overrides, and parses the in-core subject and SMS body templates once.
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
	for _, o := range d.AppLayouts {
		dir := o.Dir
		if dir == "" {
			dir = defaultLayoutDir
		}
		opts = append(opts, email.WithLayouts(o.FS, dir, email.LayerApp))
	}
	if d.Branding != nil {
		opts = append(opts, email.WithBranding(d.Branding))
	}
	emailer, err := email.New(d.Mailer, d.MailFrom, opts...)
	if err != nil {
		return nil, fmt.Errorf("delivery: build emailer: %w", err)
	}

	subjects := make(map[string]*template.Template, len(specs))
	smsBodies := make(map[string]*template.Template, len(specs))
	for purpose, sp := range specs {
		t, err := template.New("subject_" + purpose).Parse(sp.subject)
		if err != nil {
			return nil, fmt.Errorf("delivery: parse subject template %q: %w", purpose, err)
		}
		subjects[purpose] = t
		if sp.sms != "" {
			t, err := template.New("sms_" + purpose).Parse(sp.sms)
			if err != nil {
				return nil, fmt.Errorf("delivery: parse sms template %q: %w", purpose, err)
			}
			smsBodies[purpose] = t
		}
	}
	for purpose, src := range d.Subjects {
		t, err := parseOverride("subject", purpose, src)
		if err != nil {
			return nil, err
		}
		subjects[purpose] = t
	}
	for purpose, src := range d.SMSBodies {
		if sp, ok := specs[purpose]; ok && sp.sms == "" {
			return nil, fmt.Errorf("%w: sms override for %q, an email-only purpose", ErrOverrideInvalid, purpose)
		}
		t, err := parseOverride("sms", purpose, src)
		if err != nil {
			return nil, err
		}
		smsBodies[purpose] = t
	}

	return &Router{
		emailer:   emailer,
		mailer:    d.Mailer,
		mailFrom:  d.MailFrom,
		notifiers: d.Notifiers,
		subjects:  subjects,
		smsBodies: smsBodies,
		dataHook:  d.DataHook,
		logger:    logger,
	}, nil
}

// parseOverride validates and parses one host subject/SMS override source for
// purpose: the purpose must be a known delivery purpose, the source must not be
// empty or whitespace-only, and it must parse. Missing-key errors are enabled so
// a data-contract mistake fails the render loudly instead of shipping "<no
// value>". Every rejection is ErrOverrideInvalid with a message naming the cause.
func parseOverride(rail, purpose, src string) (*template.Template, error) {
	if _, ok := specs[purpose]; !ok {
		return nil, fmt.Errorf("%w: %s override for unknown purpose %q", ErrOverrideInvalid, rail, purpose)
	}
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("%w: %s override for %q is empty", ErrOverrideInvalid, rail, purpose)
	}
	t, err := template.New(rail + "_" + purpose).Option("missingkey=error").Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%w: parse %s override for %q: %v", ErrOverrideInvalid, rail, purpose, err)
	}
	return t, nil
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

	// The working map starts as the caller's fields minus the reserved Secret and
	// Subject: those are framework-owned and inserted below, AFTER the host hook,
	// so the rendered credential is always the one the sealed Envelope carries.
	data := make(map[string]any, len(req.Data)+2)
	for k, v := range req.Data {
		if k == fieldSecret || k == fieldSubject {
			continue
		}
		data[k] = v
	}
	if r.dataHook != nil {
		// The hook sees its own defensive copy (nested string maps included) so a
		// mutation of the input can neither reach this render nor a concurrent one;
		// only the RETURNED additions/replacements merge, and Link survives because
		// the hook can only ever return a non-reserved field.
		extra, err := r.dataHook(ctx, req.Purpose, cloneData(data))
		if err != nil {
			return Envelope{}, fmt.Errorf("delivery: data hook %q: %w", req.Purpose, err)
		}
		for k, v := range extra {
			if k == fieldSecret || k == fieldLink || k == fieldSubject {
				return Envelope{}, fmt.Errorf("%w: %q for purpose %q", ErrDataHookReserved, k, req.Purpose)
			}
			data[k] = v
		}
	}
	if req.Secret != "" {
		data[fieldSecret] = req.Secret
	}

	env := Envelope{
		Destination:     req.Destination,
		ResolutionInput: req.ResolutionInput,
		Secret:          req.Secret,
	}

	if req.Kind == identity.KindEmail {
		subject, err := renderTemplate(r.subjects[req.Purpose], data)
		if err != nil {
			return Envelope{}, fmt.Errorf("delivery: render subject %q: %w", req.Purpose, err)
		}
		if strings.TrimSpace(subject) == "" || strings.ContainsAny(subject, "\r\n") {
			return Envelope{}, fmt.Errorf("%w: purpose %q", ErrSubjectInvalid, req.Purpose)
		}
		data[fieldSubject] = subject
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
	smsTmpl, ok := r.smsBodies[req.Purpose]
	if !ok {
		return Envelope{}, fmt.Errorf("%w: purpose %q has no %s template", ErrKindUnsupported, req.Purpose, req.Kind)
	}
	body, err := renderTemplate(smsTmpl, data)
	if err != nil {
		return Envelope{}, fmt.Errorf("delivery: render sms %q: %w", req.Purpose, err)
	}
	env.Body = body
	return env, nil
}

// cloneData is the defensive copy handed to the DataHook: a fresh top-level map,
// with any nested map[string]string (the invitation Metadata) copied as well so
// the hook cannot reach the caller's or this render's state through it.
func cloneData(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for k, v := range data {
		if m, ok := v.(map[string]string); ok {
			v = maps.Clone(m)
		}
		out[k] = v
	}
	return out
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

// renderTemplate executes one parsed subject/SMS template against data.
func renderTemplate(t *template.Template, data map[string]any) (string, error) {
	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

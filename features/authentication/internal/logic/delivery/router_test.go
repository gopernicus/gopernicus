package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/testoverride"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// stubSender captures the last email.Message and can be primed to fail.
type stubSender struct {
	last   email.Message
	called bool
	err    error
}

func (s *stubSender) Send(_ context.Context, msg email.Message) error {
	s.called = true
	s.last = msg
	return s.err
}

// stubNotifier captures the last notify.Message for one kind. When honorCancel is
// set, Notify blocks until the context is done and returns its error, modeling a
// transport that honors cancellation.
type stubNotifier struct {
	kind        string
	last        notify.Message
	lastTo      identity.Address
	called      bool
	err         error
	honorCancel bool
}

func (n *stubNotifier) Kind() string { return n.kind }

func (n *stubNotifier) Notify(ctx context.Context, to identity.Address, msg notify.Message) error {
	if n.honorCancel {
		<-ctx.Done()
		return ctx.Err()
	}
	n.called = true
	n.last = msg
	n.lastTo = to
	return n.err
}

// newRouter builds a Router over a stub sender plus the given notifiers.
func newRouter(t *testing.T, sender email.Sender, notifiers map[string]notify.Notifier, overrides ...TemplateOverride) *Router {
	t.Helper()
	r, err := NewRouter(Deps{
		Mailer:       sender,
		MailFrom:     "no-reply@example.test",
		Notifiers:    notifiers,
		AppTemplates: overrides,
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

// A nil Mailer is rejected loudly: email is always-on via the required Mailer.
func TestNewRouterRequiresMailer(t *testing.T) {
	if _, err := NewRouter(Deps{}); !errors.Is(err, ErrMailerRequired) {
		t.Fatalf("NewRouter(nil mailer) err=%v, want ErrMailerRequired", err)
	}
	if !errors.Is(ErrMailerRequired, sdk.ErrInvalidInput) {
		t.Fatalf("ErrMailerRequired must wrap sdk.ErrInvalidInput")
	}
}

// An email render produces subject, text, and HTML carrying the secret, with the
// resolution input and secret carried on the envelope for the worker.
func TestRenderEmailVerification(t *testing.T) {
	r := newRouter(t, &stubSender{}, nil)
	env, err := r.Render(context.Background(), Request{
		Kind:            identity.KindEmail,
		Purpose:         PurposeRegistrationVerification,
		Destination:     "user@example.test",
		ResolutionInput: "user@example.test",
		Secret:          "123456",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if env.Subject != "Verify your email" {
		t.Fatalf("subject = %q", env.Subject)
	}
	if !strings.Contains(env.Body, "123456") {
		t.Fatalf("text body missing code: %q", env.Body)
	}
	if !strings.Contains(env.HTML, "123456") {
		t.Fatalf("html body missing code: %q", env.HTML)
	}
	if env.Destination != "user@example.test" || env.ResolutionInput != "user@example.test" || env.Secret != "123456" {
		t.Fatalf("envelope carriage wrong: %+v", env)
	}
}

// A subject template interpolates request data (the pending-link provider name).
func TestRenderEmailSubjectInterpolation(t *testing.T) {
	r := newRouter(t, &stubSender{}, nil)
	env, err := r.Render(context.Background(), Request{
		Kind:        identity.KindEmail,
		Purpose:     PurposeOAuthPendingLink,
		Destination: "user@example.test",
		Secret:      "tok",
		Data:        map[string]any{"ProviderName": "GitHub"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if env.Subject != "Confirm linking your GitHub account" {
		t.Fatalf("subject = %q", env.Subject)
	}
}

// A host LayerApp override wins over the LayerCore default for the same template.
func TestRenderEmailAppOverride(t *testing.T) {
	r := newRouter(t, &stubSender{}, nil, TemplateOverride{Namespace: namespace, FS: testoverride.FS})
	env, err := r.Render(context.Background(), Request{
		Kind:        identity.KindEmail,
		Purpose:     PurposeRegistrationVerification,
		Destination: "user@example.test",
		Secret:      "999000",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(env.HTML, "APP OVERRIDE") {
		t.Fatalf("LayerApp override did not win: %q", env.HTML)
	}
	if !strings.Contains(env.HTML, "999000") {
		t.Fatalf("override missing secret: %q", env.HTML)
	}
}

// An SMS render is body-only: the link is in the body, and there is no subject or
// HTML (no email layout in an SMS body).
func TestRenderSMSMagicLink(t *testing.T) {
	r := newRouter(t, &stubSender{}, map[string]notify.Notifier{identity.KindPhone: &stubNotifier{kind: identity.KindPhone}})
	env, err := r.Render(context.Background(), Request{
		Kind:            identity.KindPhone,
		Purpose:         PurposeMagicLink,
		Destination:     "+15550001111",
		ResolutionInput: "+15550001111",
		Secret:          "TOK",
		Data:            map[string]any{"Link": "https://app.example.test/redeem?t=TOK"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if env.Subject != "" || env.HTML != "" {
		t.Fatalf("SMS envelope must be body-only, got subject=%q html=%q", env.Subject, env.HTML)
	}
	if !strings.Contains(env.Body, "https://app.example.test/redeem?t=TOK") {
		t.Fatalf("SMS body missing link: %q", env.Body)
	}
}

// An email-only purpose rendered for a non-email kind is ErrKindUnsupported.
func TestRenderSMSUnsupportedPurpose(t *testing.T) {
	r := newRouter(t, &stubSender{}, map[string]notify.Notifier{identity.KindPhone: &stubNotifier{kind: identity.KindPhone}})
	_, err := r.Render(context.Background(), Request{
		Kind:        identity.KindPhone,
		Purpose:     PurposePasswordReset,
		Destination: "+15550001111",
	})
	if !errors.Is(err, ErrKindUnsupported) {
		t.Fatalf("Render(email-only for SMS) err=%v, want ErrKindUnsupported", err)
	}
}

// An unknown purpose is ErrUnknownPurpose.
func TestRenderUnknownPurpose(t *testing.T) {
	r := newRouter(t, &stubSender{}, nil)
	_, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: "nope"})
	if !errors.Is(err, ErrUnknownPurpose) {
		t.Fatalf("Render(unknown) err=%v, want ErrUnknownPurpose", err)
	}
}

// Email delivery through the Mailer carries subject, text, and HTML to the
// resolved destination.
func TestDeliverEmailViaMailer(t *testing.T) {
	sender := &stubSender{}
	r := newRouter(t, sender, nil)
	env := Envelope{Destination: "user@example.test", Subject: "Verify your email", Body: "code 123456", HTML: "<p>code 123456</p>"}
	if err := r.Deliver(context.Background(), identity.KindEmail, env); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !sender.called {
		t.Fatal("mailer was not called")
	}
	if sender.last.From != "no-reply@example.test" || len(sender.last.To) != 1 || sender.last.To[0] != "user@example.test" {
		t.Fatalf("mail addressing wrong: %+v", sender.last)
	}
	if sender.last.Subject != "Verify your email" || sender.last.Text != "code 123456" || sender.last.HTML != "<p>code 123456</p>" {
		t.Fatalf("mail content wrong: %+v", sender.last)
	}
}

// A wired email-kind notifier bridges email delivery instead of the Mailer.
func TestDeliverEmailBridgePrefersNotifier(t *testing.T) {
	sender := &stubSender{}
	bridge := &stubNotifier{kind: identity.KindEmail}
	r := newRouter(t, sender, map[string]notify.Notifier{identity.KindEmail: bridge})
	env := Envelope{Destination: "user@example.test", Subject: "Verify your email", Body: "code"}
	if err := r.Deliver(context.Background(), identity.KindEmail, env); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if sender.called {
		t.Fatal("mailer was used despite a wired email-kind notifier")
	}
	if !bridge.called || bridge.lastTo.Value != "user@example.test" || bridge.last.Body != "code" {
		t.Fatalf("bridge not used correctly: called=%v to=%+v msg=%+v", bridge.called, bridge.lastTo, bridge.last)
	}
}

// A non-email kind delivers through its wired notifier.
func TestDeliverSMSViaNotifier(t *testing.T) {
	phone := &stubNotifier{kind: identity.KindPhone}
	r := newRouter(t, &stubSender{}, map[string]notify.Notifier{identity.KindPhone: phone})
	env := Envelope{Destination: "+15550001111", Body: "Sign in: https://x/y"}
	if err := r.Deliver(context.Background(), identity.KindPhone, env); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !phone.called || phone.lastTo.Kind != identity.KindPhone || phone.last.Body != "Sign in: https://x/y" {
		t.Fatalf("notifier not used correctly: %+v %+v", phone.lastTo, phone.last)
	}
}

// A non-email kind with no wired notifier is ErrKindUnsupported (deny-by-absence).
func TestDeliverSMSNoNotifier(t *testing.T) {
	r := newRouter(t, &stubSender{}, nil)
	err := r.Deliver(context.Background(), identity.KindPhone, Envelope{Destination: "+15550001111"})
	if !errors.Is(err, ErrKindUnsupported) {
		t.Fatalf("Deliver(phone, no notifier) err=%v, want ErrKindUnsupported", err)
	}
}

// A transport failure surfaces as a kind-tagged DeliveryError whose cause is the
// underlying sender error (errors.Is passes through).
func TestDeliverTransportErrorIsKindTagged(t *testing.T) {
	sentinel := errors.New("smtp down")
	sender := &stubSender{err: sentinel}
	r := newRouter(t, sender, nil)
	err := r.Deliver(context.Background(), identity.KindEmail, Envelope{Destination: "user@example.test", Subject: "s", Body: "b"})
	var de *DeliveryError
	if !errors.As(err, &de) {
		t.Fatalf("err=%v, want *DeliveryError", err)
	}
	if de.Kind != identity.KindEmail {
		t.Fatalf("DeliveryError.Kind = %q, want %q", de.Kind, identity.KindEmail)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("DeliveryError does not wrap the transport error: %v", err)
	}
}

// Deliver honors context cancellation: a canceled context yields a kind-tagged
// DeliveryError wrapping context.Canceled without touching the transport.
func TestDeliverHonorsCanceledContext(t *testing.T) {
	sender := &stubSender{}
	r := newRouter(t, sender, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.Deliver(ctx, identity.KindEmail, Envelope{Destination: "user@example.test", Subject: "s", Body: "b"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Deliver(canceled) err=%v, want context.Canceled", err)
	}
	if sender.called {
		t.Fatal("transport was called despite a canceled context")
	}
	var de *DeliveryError
	if !errors.As(err, &de) || de.Kind != identity.KindEmail {
		t.Fatalf("cancellation error not kind-tagged: %v", err)
	}
}

// A notifier that honors cancellation propagates the context error out of Deliver
// as a kind-tagged DeliveryError (the cancellation contract for real transports).
func TestDeliverNotifierHonorsCancellation(t *testing.T) {
	phone := &stubNotifier{kind: identity.KindPhone, honorCancel: true}
	r := newRouter(t, &stubSender{}, map[string]notify.Notifier{identity.KindPhone: phone})
	ctx, cancel := context.WithCancel(context.Background())
	go cancel()
	err := r.Deliver(ctx, identity.KindPhone, Envelope{Destination: "+15550001111", Body: "b"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Deliver err=%v, want context.Canceled", err)
	}
}

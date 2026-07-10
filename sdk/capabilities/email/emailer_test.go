package email

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// mockSender records the last Message it was asked to send and can be
// programmed to fail.
type mockSender struct {
	sendFunc func(ctx context.Context, msg Message) error
	last     Message
	called   bool
}

func (m *mockSender) Send(ctx context.Context, msg Message) error {
	m.called = true
	m.last = msg
	if m.sendFunc != nil {
		return m.sendFunc(ctx, msg)
	}
	return nil
}

func newTestEmailer(t *testing.T, sender Sender, opts ...Option) *Emailer {
	t.Helper()
	e, err := New(sender, "default@example.com", opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return e
}

func TestRenderAndSend_SetsBothBodiesAndDefaultFrom(t *testing.T) {
	sender := &mockSender{}
	e := newTestEmailer(t, sender)
	registerContent(t, e.templates, "test:welcome", LayerApp, `<h1>Welcome {{.Name}}</h1>`)

	err := e.RenderAndSend(context.Background(), SendRequest{
		To:       "recipient@example.com",
		Subject:  "Welcome",
		Template: "test:welcome",
		Data:     map[string]any{"Name": "Alice"},
	})
	if err != nil {
		t.Fatalf("RenderAndSend() error = %v", err)
	}
	if !sender.called {
		t.Fatal("sender was not called")
	}

	msg := sender.last
	if msg.From != "default@example.com" {
		t.Errorf("From = %q, want default@example.com", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != "recipient@example.com" {
		t.Errorf("To = %v, want [recipient@example.com]", msg.To)
	}
	if msg.Subject != "Welcome" {
		t.Errorf("Subject = %q, want Welcome", msg.Subject)
	}
	if !strings.Contains(msg.HTML, "Welcome Alice") {
		t.Errorf("HTML does not contain rendered content: %q", msg.HTML)
	}
	if strings.TrimSpace(msg.Text) == "" {
		t.Error("Text body is empty, want the stripped-HTML fallback")
	}
	if !strings.Contains(msg.Text, "Welcome Alice") {
		t.Errorf("Text does not contain rendered content: %q", msg.Text)
	}
}

func TestRenderAndSend_WithLayoutOption(t *testing.T) {
	sender := &mockSender{}
	e := newTestEmailer(t, sender)
	registerContent(t, e.templates, "test:promo", LayerApp, `<p>Big sale</p>`)

	err := e.RenderAndSend(context.Background(), SendRequest{
		To:       "recipient@example.com",
		Subject:  "Sale",
		Template: "test:promo",
	}, WithLayout(LayoutMarketing))
	if err != nil {
		t.Fatalf("RenderAndSend() error = %v", err)
	}
	if !strings.Contains(sender.last.HTML, "subscribed to our mailing list") {
		t.Errorf("marketing layout not applied: %q", sender.last.HTML)
	}
}

func TestRenderAndSend_MissingToFailsValidation(t *testing.T) {
	sender := &mockSender{}
	e := newTestEmailer(t, sender)
	registerContent(t, e.templates, "test:welcome", LayerApp, `<p>Hi</p>`)

	err := e.RenderAndSend(context.Background(), SendRequest{
		Subject:  "Hi",
		Template: "test:welcome",
	})
	if err == nil {
		t.Fatal("RenderAndSend() should fail validation for missing To")
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("error %v does not wrap sdk.ErrInvalidInput", err)
	}
	if sender.called {
		t.Error("sender must not be called for an invalid message")
	}
}

func TestRenderAndSend_MissingTemplate(t *testing.T) {
	sender := &mockSender{}
	e := newTestEmailer(t, sender)

	err := e.RenderAndSend(context.Background(), SendRequest{
		To:       "recipient@example.com",
		Subject:  "Hi",
		Template: "nope:missing",
	})
	if err == nil {
		t.Fatal("RenderAndSend() should error for a missing template")
	}
	if sender.called {
		t.Error("sender must not be called when rendering fails")
	}
}

func TestRenderAndSend_SenderErrorWrappedAndLogged(t *testing.T) {
	sentinel := errors.New("delivery failed")
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	sender := &mockSender{
		sendFunc: func(ctx context.Context, msg Message) error { return sentinel },
	}
	e := newTestEmailer(t, sender, WithLogger(log))
	registerContent(t, e.templates, "test:welcome", LayerApp, `<p>Hi</p>`)

	err := e.RenderAndSend(context.Background(), SendRequest{
		To:       "recipient@example.com",
		Subject:  "Hi",
		Template: "test:welcome",
	})
	if err == nil {
		t.Fatal("RenderAndSend() should return the sender error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error %v does not wrap the sender error", err)
	}
	if !strings.Contains(err.Error(), "send email") {
		t.Errorf("error = %q, want to contain 'send email'", err.Error())
	}
	if !strings.Contains(buf.String(), "failed to send email") {
		t.Errorf("failure was not logged: %q", buf.String())
	}
}

func TestRender_DoesNotSend(t *testing.T) {
	sender := &mockSender{}
	e := newTestEmailer(t, sender)
	registerContent(t, e.templates, "test:welcome", LayerApp, `<h1>Welcome {{.Name}}</h1>`)

	html, text, err := e.Render("test:welcome", map[string]any{"Name": "Bob"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if sender.called {
		t.Error("Render() must not send")
	}
	if !strings.Contains(html, "Welcome Bob") {
		t.Errorf("html does not contain content: %q", html)
	}
	if !strings.Contains(text, "Welcome Bob") {
		t.Errorf("text does not contain content: %q", text)
	}
}

func TestWithContentTemplatesOption(t *testing.T) {
	sender := &mockSender{}
	e := newTestEmailer(t, sender, WithContentTemplates("layouts", infraLayoutTemplates, LayerApp))

	if _, err := e.templates.ResolveContent("layouts:transactional"); err != nil {
		t.Errorf("WithContentTemplates did not register templates: %v", err)
	}
}

func TestWithBrandingOption(t *testing.T) {
	sender := &mockSender{}
	e := newTestEmailer(t, sender, WithBranding(&Branding{Name: "Acme"}))
	registerContent(t, e.templates, "test:x", LayerApp, `<p>hi</p>`)

	html, _, err := e.Render("test:x", nil, WithLayout(LayoutMarketing))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(html, "Acme") {
		t.Errorf("branding name not applied to layout: %q", html)
	}
}

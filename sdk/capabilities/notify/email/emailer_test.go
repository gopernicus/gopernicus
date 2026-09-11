package email

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gopernicus/gopernicus/sdk"
)

type mockSender struct {
	sendFunc func(context.Context, Message) error
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

func TestEmailerRendersSubjectAndBothBodies(t *testing.T) {
	sender := &mockSender{}
	layouts := fstest.MapFS{"layouts/transactional.html": {Data: []byte("<title>{{.Subject}}</title>{{.Content}}")}}
	e, err := New(sender, "from@example.test", WithContentTemplates("test", templateFiles("<p>Hello {{.Name}}</p>", "Hello {{.Name}}"), LayerApp), WithLayouts(layouts, "layouts", LayerApp))
	if err != nil {
		t.Fatal(err)
	}
	err = e.RenderAndSend(context.Background(), SendRequest{To: "to@example.test", Subject: "Welcome", Template: "test:body", Data: struct{ Name string }{"Alice"}})
	if err != nil {
		t.Fatal(err)
	}
	if sender.last.From != "from@example.test" || sender.last.Subject != "Welcome" || !strings.Contains(sender.last.HTML, "<title>Welcome</title>") || !strings.Contains(sender.last.Text, "Hello Alice") {
		t.Fatalf("email representation lost: %+v", sender.last)
	}
}
func TestEmailerValidationCancellationAndFailure(t *testing.T) {
	if _, err := New(nil, "from@example.test"); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil sender: %v", err)
	}
	sender := &mockSender{}
	e, err := New(sender, "from@example.test", WithContentTemplates("test", templateFiles("HTML", "text"), LayerApp))
	if err != nil {
		t.Fatal(err)
	}
	req := SendRequest{To: "to@example.test", Subject: "Subject", Template: "test:body"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.RenderAndSend(ctx, req); !errors.Is(err, context.Canceled) || sender.called {
		t.Fatalf("canceled send: %v", err)
	}
	req.To = ""
	if err := e.RenderAndSend(context.Background(), req); !errors.Is(err, sdk.ErrInvalidInput) || sender.called {
		t.Fatalf("invalid recipient: %v", err)
	}
	req.To = "to@example.test"
	failure := errors.New("transport failure")
	sender.sendFunc = func(context.Context, Message) error { return failure }
	if err := e.RenderAndSend(context.Background(), req); !errors.Is(err, failure) {
		t.Fatalf("provider cause: %v", err)
	}
}
func TestEmailerRenderDoesNotSend(t *testing.T) {
	sender := &mockSender{}
	e, err := New(sender, "from@example.test", WithContentTemplates("test", templateFiles("HTML", "text"), LayerApp))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Render(RenderRequest{Template: "test:body"}); err != nil || sender.called {
		t.Fatalf("render sent or failed: %v", err)
	}
}

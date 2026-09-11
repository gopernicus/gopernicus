package email

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

func TestSelectedEmailPreservesRichContentAndRecipientSnapshot(t *testing.T) {
	sender := &mockSender{}
	message := validMessage()
	message.HTML = "<p>Rich email</p>"
	selected := NewDelivery(sender, message)
	message.To[0] = "changed@example.test"
	chatCalls := 0
	chat := notify.DeliveryFunc(func(context.Context) error { chatCalls++; return nil })
	if err := notify.Send(context.Background(), selected, chat); err != nil {
		t.Fatal(err)
	}
	if sender.last.To[0] != "recipient@example.com" || sender.last.HTML != message.HTML || sender.last.Text != message.Text || chatCalls != 1 {
		t.Fatalf("selected content changed: message=%+v chat=%d", sender.last, chatCalls)
	}
	sender.last.To[0] = "provider-mutated@example.test"
	if err := notify.Send(context.Background(), selected); err != nil {
		t.Fatal(err)
	}
	if sender.last.To[0] != "recipient@example.com" || chatCalls != 1 {
		t.Fatalf("repeat selection changed or fired chat: to=%v chat=%d", sender.last.To, chatCalls)
	}
}

func TestPreparedDeliveryPreservesTransportPosture(t *testing.T) {
	for _, sender := range []Sender{nil, &mockSender{}, NewConsole(nil), NewSMTP(SMTPConfig{Host: "localhost", Port: "25"})} {
		before := notify.InspectTransport(sender)
		after := notify.InspectTransport(NewDelivery(sender, validMessage()))
		if before != after {
			t.Fatalf("posture changed: before=%+v after=%+v", before, after)
		}
		_, err := notify.CheckTransport(environment.ModeProduction, NewDelivery(sender, validMessage()))
		if errors.Is(err, notify.ErrInsecureTransport) == before.ProductionCapable() {
			t.Fatalf("incorrect production decision: before=%+v err=%v", before, err)
		}
	}
}

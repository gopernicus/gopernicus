package notify_test

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

// This example transport prints synthetic calls instead of delivering mail.
type exampleMailer struct{}

func (exampleMailer) Send(_ context.Context, message email.Message) error {
	fmt.Println("email:", message.Subject)
	return nil
}

func ExampleSend() {
	ctx := context.Background()
	emailSender := exampleMailer{}
	outageEmail := email.Message{From: "status@example.test", To: []string{"operator@example.test"}, Subject: "Service outage", Text: "The service is unavailable.", HTML: "<p>The service is unavailable.</p>"}
	// A host can adapt its existing Slack client with its own typed message.
	slackDelivery := notify.DeliveryFunc(func(context.Context) error {
		fmt.Println("Slack: Service outage")
		return nil
	})
	if err := notify.Send(ctx, email.NewDelivery(emailSender, outageEmail), slackDelivery); err != nil {
		panic(err)
	}

	resetEmail := email.Message{From: "accounts@example.test", To: []string{"user@example.test"}, Subject: "Reset your password", Text: "Open your reset link.", HTML: "<p>Open your reset link.</p>"}
	if err := notify.Send(ctx, email.NewDelivery(emailSender, resetEmail)); err != nil {
		panic(err)
	}
	// Output:
	// email: Service outage
	// Slack: Service outage
	// email: Reset your password
}

// Package notify defines the notification interface for the SDK.
// Infrastructure adapters (emailer, Slack, SMS) implement this interface.
// Domain code depends on Notifier, never on specific delivery channels.
package notify

import "context"

// Notifier sends notifications through any channel.
// Implementations include email, Slack, SMS, etc.
type Notifier interface {
	Notify(ctx context.Context, notification Notification) error
}

// Notification represents a message to be delivered.
// It is deliberately simple — for channel-specific features (HTML body,
// attachments, threads), use the infrastructure adapter directly.
type Notification struct {
	// Recipient identifies who receives the notification.
	// The format depends on the channel (email address, Slack user ID, phone number).
	Recipient string

	// Subject is the notification subject or title.
	Subject string

	// Body is the plain text content of the notification.
	Body string

	// Metadata holds channel-specific key-value pairs.
	// Adapters can use this for features beyond the common fields.
	Metadata map[string]string
}

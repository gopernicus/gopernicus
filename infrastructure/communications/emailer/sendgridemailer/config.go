package sendgridemailer

// Config holds SendGrid client configuration.
type Config struct {
	APIKey    string
	FromEmail string
	FromName  string
}

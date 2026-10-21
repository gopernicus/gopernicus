// Package externalsubject is the local mirror of identities verified by an
// external identity provider: which local user a provider subject maps to. It is
// a table of its own, distinct from oauthaccount: mirror rows are never login
// methods, never user-unlinkable, and hold no provider tokens.
package externalsubject

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// ExternalSubject maps (Provider, Subject) to a local user. Provider is the
// host-chosen provider NAME, not the token issuer: one provider can issue one
// subject under several issuers (an ID token and a session cookie, a custom and
// a tenant domain), and every one of them must reach the same user.
type ExternalSubject struct {
	Provider string
	Subject  string
	UserID   string
	// Issuer is the last iss that produced this row. It is informational.
	Issuer string
	// Email is the normalized address at provisioning. It is informational.
	Email       string
	FirstSeenAt time.Time
}

// New builds an ExternalSubject as of now. A blank provider or subject wraps
// sdk.ErrInvalidInput. userID may be blank when the row is provisioned together
// with its user, which the store then links.
func New(provider, subject, userID, issuer, email string, now time.Time) (ExternalSubject, error) {
	if strings.TrimSpace(provider) == "" || provider != strings.TrimSpace(provider) {
		return ExternalSubject{}, fmt.Errorf("external subject provider is required: %w", sdk.ErrInvalidInput)
	}
	if subject == "" {
		return ExternalSubject{}, fmt.Errorf("external subject is required: %w", sdk.ErrInvalidInput)
	}
	return ExternalSubject{
		Provider:    provider,
		Subject:     subject,
		UserID:      userID,
		Issuer:      issuer,
		Email:       email,
		FirstSeenAt: now.UTC(),
	}, nil
}

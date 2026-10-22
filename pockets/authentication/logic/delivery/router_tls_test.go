package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// starttlsSender declares opportunistic STARTTLS for both delivery rails.
type starttlsSender struct{}

func (starttlsSender) Send(context.Context, email.Message) error { return nil }
func (starttlsSender) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityStartTLS}
}

type starttlsBodySender struct{}

func (starttlsBodySender) Send(context.Context, string, string) error { return nil }
func (starttlsBodySender) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityStartTLS}
}

type tlsSender struct{}

func (tlsSender) Send(context.Context, email.Message) error { return nil }
func (tlsSender) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityTLS}
}

// TestValidateTransportsRequiresMandatoryTLS proves the router's own production
// verdict refuses opportunistic STARTTLS on either rail unless the router was
// built with WithOpportunisticTLSAcknowledged, and that development accepts it.
func TestValidateTransportsRequiresMandatoryTLS(t *testing.T) {
	build := func(mailer email.Sender, opts ...RouterOption) *Router {
		t.Helper()
		r, err := NewRouter(mailer, opts...)
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}
		return r
	}
	phone := WithBodySenders(map[string]BodySender{"phone": starttlsBodySender{}})

	for name, r := range map[string]*Router{
		"starttls mailer":      build(starttlsSender{}),
		"starttls body sender": build(tlsSender{}, phone),
	} {
		if err := r.ValidateTransports(environment.ModeProduction); !errors.Is(err, notify.ErrOpportunisticTLS) {
			t.Errorf("%s in production: err = %v, want notify.ErrOpportunisticTLS", name, err)
		}
		if err := r.ValidateTransports(environment.ModeDevelopment); err != nil {
			t.Errorf("%s in development: %v", name, err)
		}
	}
	acknowledged := build(starttlsSender{}, phone, WithOpportunisticTLSAcknowledged())
	if err := acknowledged.ValidateTransports(environment.ModeProduction); err != nil {
		t.Errorf("acknowledged STARTTLS in production: %v", err)
	}
}

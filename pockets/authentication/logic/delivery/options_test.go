package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

func TestTemplateOptionOwnsInputsAndSupportsConcurrentReuse(t *testing.T) {
	subjects := map[string]string{PurposeRegistrationVerification: "Original subject"}
	branding := &email.Branding{Name: "Original brand"}
	option := WithTemplates(TemplatesConfig{Subjects: subjects, Branding: branding})
	subjects[PurposeRegistrationVerification] = "Changed subject"
	branding.Name = "Changed brand"
	for range 8 {
		t.Run("reuse", func(t *testing.T) {
			t.Parallel()
			router, err := NewRouter(&stubSender{}, option)
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := router.Render(context.Background(), Request{Kind: sdk.AddressKindEmail, Purpose: PurposeRegistrationVerification, Destination: "user@example.com", Secret: "123456"})
			if err != nil {
				t.Fatal(err)
			}
			if envelope.Subject != "Original subject" || !strings.Contains(envelope.HTML, "Original brand") || strings.Contains(envelope.HTML, "Changed brand") {
				t.Fatalf("host mutation affected render: %+v", envelope)
			}
		})
	}
}

func TestConstructorsRejectNilOptions(t *testing.T) {
	for name, construct := range map[string]func() error{
		"router":    func() error { _, err := NewRouter(nil, nil); return err },
		"processor": func() error { _, err := NewJobsProcessor(nil, nil, nil); return err },
		"queue":     func() error { _, err := NewInProcessQueue(nil); return err },
		"runtime":   func() error { _, err := NewInProcessRuntime(nil, nil, nil); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := construct(); !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "nil option") {
				t.Fatalf("nil option: %v", err)
			}
		})
	}
}

package cms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestContact_FormSubmitThanks(t *testing.T) {
	var got struct{ name, email, message string }
	svc := &fakeContactSvc{
		submitFn: func(ctx context.Context, name, email, message string) (messaging.Inquiry, error) {
			got.name, got.email, got.message = name, email, message
			return messaging.Inquiry{ID: "iq1", Name: name}, nil
		},
	}

	// Form renders.
	rec := httptest.NewRecorder()
	contactRouter(svc).ServeHTTP(rec, httptest.NewRequest("GET", "/contact", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="message"`) {
		t.Fatalf("form: status=%d", rec.Code)
	}

	// Submit → thank-you, service got the values.
	form := url.Values{"name": {"Alice"}, "email": {"a@b.com"}, "message": {"Hi there"}}
	req := httptest.NewRequest("POST", "/contact", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	contactRouter(svc).ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Thanks") {
		t.Fatalf("submit: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got.name != "Alice" || got.email != "a@b.com" || got.message != "Hi there" {
		t.Errorf("service got wrong values: %+v", got)
	}
}

func TestContact_ValidationRerenders(t *testing.T) {
	svc := &fakeContactSvc{
		submitFn: func(ctx context.Context, name, email, message string) (messaging.Inquiry, error) {
			return messaging.Inquiry{}, sdk.ErrInvalidInput
		},
	}
	form := url.Values{"name": {""}, "email": {"bad"}, "message": {""}}
	req := httptest.NewRequest("POST", "/contact", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	contactRouter(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `name="message"`) {
		t.Fatalf("validation: expected 400 + form re-render, got %d", rec.Code)
	}
}

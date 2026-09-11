package sendgrid

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func newTestSender(t *testing.T, cfg Config) *Sender {
	t.Helper()
	sender, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return sender
}

func TestNewRejectsInvalidConfigurationWithoutIO(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("constructor performed I/O")
		return nil, nil
	})}
	for name, cfg := range map[string]Config{
		"missing key":        {},
		"blank key":          {APIKey: " \t "},
		"key line break":     {APIKey: "secret\r\nvalue"},
		"name line break":    {APIKey: "secret", FromName: "name\nvalue"},
		"invalid origin":     {APIKey: "secret", Host: "file:///tmp/send"},
		"origin path":        {APIKey: "secret", Host: "https://example.test/mail"},
		"origin credentials": {APIKey: "secret", Host: "https://secret:password@example.test"},
		"origin query":       {APIKey: "secret", Host: "https://example.test?key=secret"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg.HTTPClient = client
			sender, err := New(cfg)
			if sender != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("sender=%v error=%v", sender, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("diagnostic exposed credentials")
			}
		})
	}
}

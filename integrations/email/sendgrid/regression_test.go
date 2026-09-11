package sendgrid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type expectedSubjectKey struct{}

func TestConcurrentSendsOwnTheirRequestBodies(t *testing.T) {
	var received atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload sendPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		expected := r.Context().Value(expectedSubjectKey{}).(string)
		if payload.Subject != expected || payload.Personalizations[0].To[0].Email != expected+"@example.test" {
			t.Errorf("request body belongs to another send: subject=%q expected=%q", payload.Subject, expected)
		}
		received.Add(1)
		return &http.Response{StatusCode: 202, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	sender := newTestSender(t, Config{APIKey: "synthetic", Host: "https://example.invalid", HTTPClient: client})
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for n := 0; n < 32; n++ {
				id := fmt.Sprintf("worker-%d-%d", worker, n)
				err := sender.Send(context.WithValue(context.Background(), expectedSubjectKey{}, id), email.Message{From: "from@example.test", To: []string{id + "@example.test"}, Subject: id, Text: "text", HTML: "<p>HTML</p>"})
				if err != nil {
					t.Error(err)
				}
			}
		}(worker)
	}
	close(start)
	wg.Wait()
	if received.Load() != 512 {
		t.Fatalf("requests=%d", received.Load())
	}
}

func TestRedirectDoesNotForwardBearerOrMessage(t *testing.T) {
	calls := 0
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > 1 {
			t.Error("redirect followed")
		}
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"http://example.invalid/v3/mail/send"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	sender := newTestSender(t, Config{APIKey: "synthetic", Host: "https://example.invalid", HTTPClient: client})
	err := sender.Send(context.Background(), email.Message{From: "from@example.test", To: []string{"to@example.test"}, Subject: "Subject", Text: "text"})
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != 307 || calls != 1 {
		t.Fatalf("redirect status=%v calls=%d", err, calls)
	}
	if client.CheckRedirect(nil, nil) != nil {
		t.Fatal("host client policy was mutated")
	}
}

type unreadBody struct {
	t      *testing.T
	closed bool
}

func (b *unreadBody) Read([]byte) (int, error) {
	b.t.Error("unused provider body read")
	return 0, io.EOF
}
func (b *unreadBody) Close() error { b.closed = true; return nil }

func TestProviderStatusIsSafeAndBodyIsClosedWithoutReading(t *testing.T) {
	for _, status := range []int{202, 400} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := &unreadBody{t: t}
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: body, Request: r}, nil
			})}
			sender := newTestSender(t, Config{APIKey: "synthetic", HTTPClient: client})
			err := sender.Send(context.Background(), email.Message{From: "from@example.test", To: []string{"to@example.test"}, Subject: "private-subject", Text: "private-body"})
			if !body.closed {
				t.Fatal("response body not closed")
			}
			if status == 202 && err != nil {
				t.Fatal(err)
			}
			if status == 400 && (!errors.Is(err, sdk.ErrInvalidInput) || strings.Contains(err.Error(), "private")) {
				t.Fatalf("provider error: %v", err)
			}
		})
	}
}

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestRunBootsWithOAuth2AndExternalDemos boots the real run() wiring with
// AUTH_OAUTH2_DEMO and AUTH_EXTERNAL_DEMO both on. The OAuth2 authorize page is
// a Browser() set that also admits external tokens once a provider is wired, so
// the host must name its login path or the adapter panics at construction.
func TestRunBootsWithOAuth2AndExternalDemos(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	t.Setenv("HOST", "localhost")
	t.Setenv("PORT", strconv.Itoa(port))
	t.Setenv("AUTH_OAUTH2_DEMO", "1")
	t.Setenv("AUTH_EXTERNAL_DEMO", "1")
	t.Setenv("AUTH_DELIVERY_MODE", "")
	origin := fmt.Sprintf("http://localhost:%d", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- fmt.Errorf("run panicked: %v", p)
			}
		}()
		done <- run(ctx, quietLog(), environment.ModeDevelopment)
	}()

	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("run returned before serving: %v", err)
		default:
		}
		resp, err := client.Get(origin + "/oauth-demo/client.json")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("host did not serve within 20s: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	resp, err := client.Get(origin + "/auth/oauth2/authorize")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/auth/login") {
		t.Fatalf("anonymous authorize = %d %q, want 303 to /auth/login", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = client.Get(origin + "/demo/oauth2/mfa")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /demo/oauth2/mfa = %d, want 401", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not stop within 30s")
	}
}

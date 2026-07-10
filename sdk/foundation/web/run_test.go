package web_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/logging"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// TestRun_GracefulShutdown verifies Run serves requests, drains an in-flight
// request when ctx is cancelled, and returns nil after a clean shutdown.
func TestRun_GracefulShutdown(t *testing.T) {
	// Find a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release // hold the request open to exercise draining
		io.WriteString(w, "done")
	})

	cfg := web.ServerConfig{
		Host:            "127.0.0.1",
		Port:            itoa(addr.Port),
		ShutdownTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- web.Run(ctx, handler, cfg, logging.NewNoop()) }()

	// Give ListenAndServe a moment, then fire an in-flight request.
	time.Sleep(150 * time.Millisecond)
	respErr := make(chan error, 1)
	respBody := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + cfg.Address() + "/")
		if err != nil {
			respErr <- err
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		respBody <- string(b)
	}()

	<-started      // request is now in-flight inside the handler
	cancel()       // trigger graceful shutdown
	close(release) // let the in-flight handler finish

	select {
	case b := <-respBody:
		if b != "done" {
			t.Errorf("in-flight response body = %q, want done", b)
		}
	case err := <-respErr:
		t.Fatalf("in-flight request failed during drain: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not complete")
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

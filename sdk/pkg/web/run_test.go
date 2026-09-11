package web_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/web"
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
		Port:            strconv.Itoa(addr.Port),
		ShutdownTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() { runErr <- web.Run(ctx, handler, cfg, log) }()

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

func TestRun_ShutdownTimeoutClosesActiveRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	defer close(release)
	handlerContext := make(chan error, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "partial response")
		http.NewResponseController(w).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
		handlerContext <- r.Context().Err()
	})
	cfg := web.ServerConfig{
		Host:            "127.0.0.1",
		Port:            strconv.Itoa(port),
		ShutdownTimeout: 50 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- web.Run(ctx, handler, cfg, slog.New(slog.DiscardHandler)) }()

	// Run owns its listener; wait for it to bind before sending the request.
	readyDeadline := time.NewTimer(3 * time.Second)
	defer readyDeadline.Stop()
	retry := time.NewTicker(10 * time.Millisecond)
	defer retry.Stop()
	for {
		connection, err := net.DialTimeout("tcp", cfg.Address(), 100*time.Millisecond)
		if err == nil {
			connection.Close()
			break
		}
		select {
		case err := <-runErr:
			t.Fatalf("Run stopped before accepting requests: %v", err)
		case <-readyDeadline.C:
			t.Fatal("Run did not start listening")
		case <-retry.C:
		}
	}

	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + cfg.Address() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	clientErr := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(response.Body)
		clientErr <- err
	}()
	cancel()

	select {
	case err := <-runErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run error = %v, want shutdown deadline", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after shutdown deadline")
	}
	select {
	case err := <-handlerContext:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handler context error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown deadline left the active request context alive")
	}
	select {
	case err := <-clientErr:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("client read error = %v, want interrupted chunked response", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown deadline left the client connection open")
	}
}

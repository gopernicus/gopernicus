package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func TestRunReturnsOnHTTPListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOST", host)
	t.Setenv("PORT", port)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("occupied HTTP port must fail")
		}
		if ctx.Err() != nil {
			t.Fatal("HTTP failure waited for external cancellation")
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("HTTP failure left the jobs runtime running")
	}
}

func TestRuntimeFailureStopsHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := make(chan struct{})
	stopped := make(chan struct{})
	failure := errors.New("fatal jobs failure")
	err := runHTTPAndJobs(ctx, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil
	}, func(context.Context) error {
		<-started
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("run = %v, want runtime failure", err)
	}
	if ctx.Err() != nil {
		t.Fatal("runtime failure waited for external cancellation")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("returned without waiting for HTTP shutdown")
	}
}

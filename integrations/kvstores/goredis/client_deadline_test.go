package goredis

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestOpenHonorsDeadlineAfterConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var connections []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, conn)
			mu.Unlock()
			// Accept commands but send no replies, unlike a refused connection.
			go func() { _, _ = io.Copy(io.Discard, conn) }()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range connections {
			_ = conn.Close()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	client, err := Open(ctx, Config{
		Addr: listener.Addr().String(), MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	})
	if client != nil {
		_ = client.Close()
		t.Fatal("Open returned a client without a successful ping")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Open error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("Open ignored caller deadline and waited %v for socket timeout", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(connections) == 0 {
		t.Fatal("test never reached an established connection")
	}
}

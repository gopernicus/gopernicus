package email

import (
	"context"
	"errors"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// Each test owns one bounded loopback session; no external mail is delivered.
func smtpSession(t *testing.T, handle func(*textproto.Conn)) SMTPConfig {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		handle(textproto.NewConn(conn))
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("SMTP session did not stop")
		}
	})
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return SMTPConfig{Host: host, Port: port}
}

func smtpReadDATA(t *testing.T, conn *textproto.Conn) []byte {
	t.Helper()
	if err := conn.PrintfLine("220 owned test sink"); err != nil {
		t.Error(err)
		return nil
	}
	for {
		line, err := conn.ReadLine()
		if err != nil {
			t.Error(err)
			return nil
		}
		switch {
		case strings.HasPrefix(line, "EHLO "), strings.HasPrefix(line, "HELO "), strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
			if err := conn.PrintfLine("250 accepted"); err != nil {
				t.Error(err)
				return nil
			}
		case line == "DATA":
			if err := conn.PrintfLine("354 send data"); err != nil {
				t.Error(err)
				return nil
			}
			data, err := conn.ReadDotBytes()
			if err != nil {
				t.Error(err)
			}
			return data
		default:
			t.Errorf("unexpected SMTP command: %q", line)
			return nil
		}
	}
}

func TestSMTPCancelDuringGreetingAndDATA(t *testing.T) {
	for _, stage := range []string{"greeting", "DATA"} {
		t.Run(stage, func(t *testing.T) {
			ready := make(chan struct{})
			cfg := smtpSession(t, func(conn *textproto.Conn) {
				if stage == "DATA" {
					_ = smtpReadDATA(t, conn)
				}
				close(ready)
				_, _ = conn.ReadLine() // exits when cancellation closes the connection
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			returned := make(chan error, 1)
			go func() { returned <- NewSMTP(cfg).Send(ctx, validMessage()) }()
			select {
			case <-ready:
			case <-time.After(3 * time.Second):
				t.Fatal("SMTP stage not reached")
			}
			cancel()
			select {
			case err := <-returned:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled SMTP stayed blocked")
			}
		})
	}
}

func TestSMTPAcknowledgementSurvivesFailedQuit(t *testing.T) {
	wire := make(chan []byte, 1)
	cfg := smtpSession(t, func(conn *textproto.Conn) { wire <- smtpReadDATA(t, conn); _ = conn.PrintfLine("250 accepted") })
	if err := NewSMTP(cfg).Send(context.Background(), validMessage()); err != nil {
		t.Fatalf("accepted DATA reported failed: %v", err)
	}
	if len(<-wire) == 0 {
		t.Fatal("SMTP received no message")
	}
}

func TestSMTPConfiguredDeadline(t *testing.T) {
	cfg := smtpSession(t, func(conn *textproto.Conn) { _, _ = conn.ReadLine() })
	cfg.Timeout = 50 * time.Millisecond
	if err := NewSMTP(cfg).Send(context.Background(), validMessage()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout cause lost: %v", err)
	}
}

func TestSMTPRejectsCanceledAndInjectedMessagesBeforeDial(t *testing.T) {
	sender := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: "1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(ctx, validMessage()); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled send: %v", err)
	}
	message := validMessage()
	message.Subject = "Subject\r\nX-Injected: value"
	if err := sender.Send(context.Background(), message); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("header injection accepted: %v", err)
	}
}

package goredis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestLive_OpenAuthenticatesAsTheNamedUser proves Config.Username reaches the
// server: a managed Redis or Valkey whose credential belongs to an ACL user
// other than "default" refuses a password-only AUTH with WRONGPASS, and that is
// what Open sent before the field existed. The test makes such a user, opens as
// it (by field, then by URL) and asks the server who it is talking to; then
// opens without the name and shows the server sees "default". It does not provoke the WRONGPASS itself:
// that needs the default user locked, which would break every other live test
// sharing this server. Needs a server that allows ACL SETUSER (a stock redis:7
// does; REDIS_TEST_ADDR is the package's live-test switch).
func TestLive_OpenAuthenticatesAsTheNamedUser(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR not set — ACL user authentication not verified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	admin, err := Open(ctx, Config{Host: addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	user := fmt.Sprintf("goredis-test-%d", time.Now().UnixNano())
	const password = "goredis-test-password"
	if err := admin.Do(ctx, "ACL", "SETUSER", user, "on", ">"+password, "~*", "+@all").Err(); err != nil {
		t.Fatalf("ACL SETUSER: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = admin.Do(cleanupCtx, "ACL", "DELUSER", user).Err()
	})

	named, err := Open(ctx, Config{Host: addr, Username: user, Password: password})
	if err != nil {
		t.Fatalf("Open() as %q error = %v", user, err)
	}
	t.Cleanup(func() { _ = named.Close() })
	got, err := named.Do(ctx, "ACL", "WHOAMI").Text()
	if err != nil {
		t.Fatalf("ACL WHOAMI: %v", err)
	}
	if got != user {
		t.Errorf("ACL WHOAMI = %q, want %q", got, user)
	}

	byURL, err := Open(ctx, Config{URL: "redis://" + user + ":" + password + "@" + addr, Host: "ignored.invalid"})
	if err != nil {
		t.Fatalf("Open() by URL as %q error = %v", user, err)
	}
	t.Cleanup(func() { _ = byURL.Close() })
	got, err = byURL.Do(ctx, "ACL", "WHOAMI").Text()
	if err != nil {
		t.Fatalf("ACL WHOAMI: %v", err)
	}
	if got != user {
		t.Errorf("ACL WHOAMI by URL = %q, want %q", got, user)
	}

	unnamed, err := Open(ctx, Config{Host: addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unnamed.Close() })
	got, err = unnamed.Do(ctx, "ACL", "WHOAMI").Text()
	if err != nil {
		t.Fatalf("ACL WHOAMI: %v", err)
	}
	if got != "default" {
		t.Errorf("ACL WHOAMI with no Username = %q, want %q", got, "default")
	}
}

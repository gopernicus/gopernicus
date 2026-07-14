package delivery

import (
	"strings"
	"sync"
	"time"

	"errors"
)

// errBoom is a generic transport/crypto sentinel for the failure paths.
var errBoom = errors.New("boom")

// fakeClock is a mutex-guarded manual clock so lease expiry, retry backoff, and
// purge retention are deterministic under -race.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeEncrypter is a reversible, non-secret test encrypter. encErr/decErr force the
// encryption-failure paths.
type fakeEncrypter struct {
	encErr error
	decErr error
}

func (f fakeEncrypter) Encrypt(plaintext string) (string, error) {
	if f.encErr != nil {
		return "", f.encErr
	}
	return "enc:" + plaintext, nil
}

func (f fakeEncrypter) Decrypt(ciphertext string) (string, error) {
	if f.decErr != nil {
		return "", f.decErr
	}
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

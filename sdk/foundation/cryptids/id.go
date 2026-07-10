// Identifier generation: the GenerateFunc port, the IDGenerator consumers
// hold, the stdlib nanoid default, and the Database delegation strategy.
// (The package doc lives in cryptids.go.)
//
// Configuration is front-loaded: NanoID validates its alphabet and size once
// and returns a ready GenerateFunc, so generation-time errors are only real
// runtime failures (crypto/rand, an integration's backend), never bad
// config. The zero-value IDGenerator is ready to use and emits the default
// nanoid shape: DefaultLength characters over Alphabet, ~119.8 bits — the
// same strength class as a v4 UUID.
//
// Delegating to the database instead is the explicit Database strategy: it
// yields "" so the entity reaches the store with an empty ID, and the store
// omits the id column and lets the database generate the key (serial int,
// DEFAULT gen_random_uuid(), a stored nanoid function, …), reading it back
// with RETURNING. Empty-ID-means-database-generates is the store-boundary
// convention; Database is what makes the emptiness intentional.

package cryptids

import (
	"crypto/rand"
	"errors"
	"fmt"
)

// Generation defaults.
const (
	// DefaultLength is the number of Alphabet characters the default nanoid
	// generator emits.
	DefaultLength = 21
	// Alphabet is the confusion-free set the default generator draws from:
	// no vowels (avoids accidental words) and no O/I/o/i (visual confusion
	// with 0/1), 52 unique bytes. This fixes the original gopernicus
	// alphabet, which carried uppercase Z twice and no lowercase z.
	Alphabet = "bcdfghjklmnpqrstvwxyzBCDFGHJKLMNPQRSTVWXYZ0123456789"
)

// bufferMultiplier sizes the random byte buffer relative to the requested ID
// length; a larger buffer reduces crypto/rand calls at slightly more memory.
const bufferMultiplier = 1.6

// defaultNanoID backs the zero-value IDGenerator. The error is structurally
// impossible (the constants are validated by test), hence discarded.
var defaultNanoID, _ = NanoID("", 0)

// GenerateFunc is the port: a configured, zero-argument ID generator. NanoID
// builds stdlib ones; integration modules provide their own. Whatever the
// strategy, the result is a string — the decision is made where the func is
// constructed, once.
type GenerateFunc func() (string, error)

// Database is the explicit delegate-to-the-database strategy: it yields ""
// so the store sees an empty ID, omits the id column on insert, and the
// database generates the key (see the package doc for the convention). The
// bundled feature stores implement this — their id_defaults migrations supply
// the schema defaults, and their conformance suites prove the round-trip. A
// host-owned store must honor the empty-ID convention before Database is
// wired, or inserts will carry an empty key.
var Database GenerateFunc = func() (string, error) { return "", nil }

// IDGenerator is what consumers hold. The zero value is ready to use and
// generates the default nanoid shape; construct with any GenerateFunc to
// choose a different strategy.
type IDGenerator struct {
	Func GenerateFunc // nil → the default nanoid generator
}

// NewGenerator wraps any GenerateFunc — NanoID's, UUID's, or an
// integration's. A nil fn yields the default nanoid generator (equivalent to
// the zero value).
func NewGenerator(fn GenerateFunc) IDGenerator {
	return IDGenerator{Func: fn}
}

// Generate returns one ID from the configured strategy.
func (g IDGenerator) Generate() (string, error) {
	fn := g.Func
	if fn == nil {
		fn = defaultNanoID
	}
	return fn()
}

// MustGenerate is Generate for call sites that cannot propagate an error
// (entity constructors). It panics on failure — with the stdlib generators
// that means only a crypto/rand failure, never bad configuration, because
// NanoID validated at construction.
func (g IDGenerator) MustGenerate() string {
	s, err := g.Generate()
	if err != nil {
		panic("cryptids: generate failed: " + err.Error())
	}
	return s
}

// NanoID returns a GenerateFunc emitting size characters drawn uniformly from
// alphabet via mask-based rejection sampling. Zero values mean the defaults:
// "" → Alphabet, 0 → DefaultLength. Validation happens here, once — the
// returned func cannot fail on configuration.
//
// The name, shape, and algorithm are ai/nanoid's, by way of its Go port:
// https://github.com/ai/nanoid and https://github.com/matoous/go-nanoid.
// This is a stdlib reimplementation (via the original gopernicus cryptids)
// rather than an import only because the sdk carries no third-party
// dependencies — credit belongs to those projects. A host that prefers the
// real library wires it through the same GenerateFunc port from an
// integration module.
func NanoID(alphabet string, size int) (GenerateFunc, error) {
	if alphabet == "" {
		alphabet = Alphabet
	}
	if size == 0 {
		size = DefaultLength
	}
	if len(alphabet) < 2 {
		return nil, errors.New("cryptids: alphabet must contain at least 2 characters")
	}
	if err := uniqueBytes(alphabet); err != nil {
		return nil, err
	}
	if size < 1 {
		return nil, errors.New("cryptids: size must be at least 1")
	}

	// Mask to the closest power of 2 >= alphabet length; indexes beyond the
	// alphabet are rejected, which is what keeps the draw uniform.
	mask := 1
	for mask < len(alphabet) {
		mask = (mask << 1) | 1
	}
	step := int(float64(size) * bufferMultiplier)
	step = max(step, size)

	return func() (string, error) {
		id := make([]byte, size)
		buf := make([]byte, step)
		idIndex := 0
		for idIndex < size {
			if _, err := rand.Read(buf); err != nil {
				return "", err
			}
			for i := 0; i < len(buf) && idIndex < size; i++ {
				alphabetIndex := int(buf[i]) & mask
				if alphabetIndex >= len(alphabet) {
					continue
				}
				id[idIndex] = alphabet[alphabetIndex]
				idIndex++
			}
		}
		return string(id), nil
	}, nil
}

func uniqueBytes(alphabet string) error {
	var seen [256]bool
	for i := 0; i < len(alphabet); i++ {
		if seen[alphabet[i]] {
			return fmt.Errorf("cryptids: alphabet has duplicate byte %q", alphabet[i])
		}
		seen[alphabet[i]] = true
	}
	return nil
}

package sdk

import (
	"crypto/rand"
	"errors"
	"fmt"
)

const (
	// DefaultIDLength is the number of characters emitted by the default ID generator.
	DefaultIDLength = 21
	// DefaultIDAlphabet contains the default generator's 52 unique ASCII characters.
	// It excludes vowels and O/I/o/i to reduce accidental words and visual confusion.
	DefaultIDAlphabet = "bcdfghjklmnpqrstvwxyzBCDFGHJKLMNPQRSTVWXYZ0123456789"
)

// bufferMultiplier sizes the random byte buffer relative to the requested ID
// length; a larger buffer reduces crypto/rand calls at slightly more memory.
const bufferMultiplier = 1.6

// defaultNanoID backs the zero-value IDGenerator. The error is structurally
// impossible (the constants are validated by test), hence discarded.
var defaultNanoID, _ = NanoID("", 0)

// IDGenerateFunc generates an identifier using a configured strategy. NanoID
// builds a random generator; integrations and hosts may supply other strategies.
type IDGenerateFunc func() (string, error)

// DatabaseID returns an empty identifier to delegate generation to the database.
// The receiving store must omit the ID column on insert and read back the
// generated key. Only use this strategy with a store and schema that support
// that convention; otherwise an insert may store an empty key.
func DatabaseID() (string, error) { return "", nil }

// IDGenerator generates identifiers using Func. Its zero value emits NanoIDs
// containing DefaultIDLength characters drawn from DefaultIDAlphabet.
type IDGenerator struct {
	Func IDGenerateFunc // nil → the default nanoid generator
}

// NewIDGenerator wraps fn. A nil fn selects the default NanoID generator,
// equivalent to the zero-value IDGenerator.
func NewIDGenerator(fn IDGenerateFunc) IDGenerator {
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
		panic("id: generate failed: " + err.Error())
	}
	return s
}

// NanoID returns an IDGenerateFunc emitting size characters drawn uniformly from
// an ASCII alphabet via mask-based rejection sampling. An empty alphabet selects
// DefaultIDAlphabet; a zero size selects DefaultIDLength. The alphabet must contain
// at least two ASCII characters, with no duplicates, and size must be positive
// after defaults apply. Configuration is validated before returning the generator.
//
// The name, shape, and algorithm are ai/nanoid's, by way of its Go port:
// https://github.com/ai/nanoid and https://github.com/matoous/go-nanoid.
// This is a stdlib reimplementation (via the original gopernicus cryptids)
// rather than an import only because the sdk carries no third-party
// dependencies — credit belongs to those projects. A host that prefers the
// real library wires it through the same IDGenerateFunc port from an
// integration module.
func NanoID(alphabet string, size int) (IDGenerateFunc, error) {
	if alphabet == "" {
		alphabet = DefaultIDAlphabet
	}
	if size == 0 {
		size = DefaultIDLength
	}
	if len(alphabet) < 2 {
		return nil, errors.New("id: alphabet must contain at least 2 characters")
	}
	if err := uniqueBytes(alphabet); err != nil {
		return nil, err
	}
	if size < 1 {
		return nil, errors.New("id: size must be at least 1")
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
		if alphabet[i] > 127 {
			return errors.New("id: alphabet must contain only ASCII characters")
		}
		if seen[alphabet[i]] {
			return fmt.Errorf("id: alphabet has duplicate byte %q", alphabet[i])
		}
		seen[alphabet[i]] = true
	}
	return nil
}

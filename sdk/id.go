package sdk

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
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
// containing DefaultIDLength characters drawn from DefaultIDAlphabet. A layer
// that resolves a class default through Or treats the zero value as "the class
// default" instead; to force NanoIDs there, pass NewIDGenerator with a
// generator from NanoID("", 0).
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

// Or returns g when it is configured (Func != nil); otherwise a generator over
// fn. The zero IDGenerator therefore means "the class default wherever Or is
// used" — a host that wants NanoID for a class that defaults to UUIDv7 passes
// sdk.NewIDGenerator(nano) with nano from sdk.NanoID("", 0), not the zero value.
func (g IDGenerator) Or(fn IDGenerateFunc) IDGenerator {
	if g.Func != nil {
		return g
	}
	return IDGenerator{Func: fn}
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

// UUIDv7 returns an IDGenerateFunc emitting RFC 9562 version-7 UUIDs in the
// canonical lowercase 8-4-4-4-12 text form: a 48-bit Unix-millisecond timestamp,
// a 12-bit sub-millisecond fraction (RFC 9562 §6.2 method 3), then 62 random
// bits. Mints from ONE returned generator never sort before earlier mints from
// the same generator; no ordering is promised across generators or processes.
func UUIDv7() IDGenerateFunc {
	var (
		mu   sync.Mutex
		last uint64
	)
	return func() (string, error) {
		now := time.Now()
		frac := uint64(now.Nanosecond()%1_000_000) * 4096 / 1_000_000
		v := uint64(now.UnixMilli())<<12 | frac

		// Clamping the combined 60-bit value lets a full fraction carry into
		// the milliseconds, so a burst or a clock step back stays ordered.
		mu.Lock()
		if v <= last {
			v = last + 1
		}
		last = v
		mu.Unlock()

		var b [16]byte
		ms := v >> 12
		b[0] = byte(ms >> 40)
		b[1] = byte(ms >> 32)
		b[2] = byte(ms >> 24)
		b[3] = byte(ms >> 16)
		b[4] = byte(ms >> 8)
		b[5] = byte(ms)
		b[6] = 0x70 | byte(v>>8)&0x0f
		b[7] = byte(v)
		// crypto/rand.Read never returns an error and always fills b (Go 1.24+).
		rand.Read(b[8:])
		b[8] = 0x80 | b[8]&0x3f

		var out [36]byte
		hex.Encode(out[0:8], b[0:4])
		out[8] = '-'
		hex.Encode(out[9:13], b[4:6])
		out[13] = '-'
		hex.Encode(out[14:18], b[6:8])
		out[18] = '-'
		hex.Encode(out[19:23], b[8:10])
		out[23] = '-'
		hex.Encode(out[24:36], b[10:16])
		return string(out[:]), nil
	}
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

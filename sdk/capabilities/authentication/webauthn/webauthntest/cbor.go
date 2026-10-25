package webauthntest

import (
	"bytes"
	"fmt"
	"slices"
)

// CBOR major types (RFC 8949 §3.1).
const (
	majorUnsigned byte = 0
	majorNegative byte = 1
	majorBytes    byte = 2
	majorText     byte = 3
	majorMap      byte = 5
	majorSimple   byte = 7
)

// cborPair is one encoded map entry.
type cborPair struct {
	key, value []byte
}

// The encoder writes the CTAP2 canonical form WebAuthn requires of
// authenticators: shortest-form heads, definite lengths, no tags or floats, and
// map keys sorted by major type, then encoded length, then bytewise.

func cborHead(major byte, n uint64) []byte {
	m := major << 5
	switch {
	case n < 24:
		return []byte{m | byte(n)}
	case n <= 0xff:
		return []byte{m | 24, byte(n)}
	case n <= 0xffff:
		return []byte{m | 25, byte(n >> 8), byte(n)}
	case n <= 0xffffffff:
		return []byte{m | 26, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
	return []byte{m | 27, byte(n >> 56), byte(n >> 48), byte(n >> 40), byte(n >> 32), byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
}

func cborUint(n uint64) []byte { return cborHead(majorUnsigned, n) }

func cborInt(n int64) []byte {
	if n >= 0 {
		return cborHead(majorUnsigned, uint64(n))
	}
	return cborHead(majorNegative, uint64(-(n + 1)))
}

func cborBytes(b []byte) []byte { return append(cborHead(majorBytes, uint64(len(b))), b...) }

func cborText(s string) []byte { return append(cborHead(majorText, uint64(len(s))), s...) }

func cborBool(v bool) []byte {
	if v {
		return []byte{majorSimple<<5 | 21}
	}
	return []byte{majorSimple<<5 | 20}
}

func cborMap(pairs ...cborPair) []byte {
	sorted := slices.Clone(pairs)
	slices.SortFunc(sorted, func(a, b cborPair) int {
		if a.key[0]>>5 != b.key[0]>>5 {
			return int(a.key[0]>>5) - int(b.key[0]>>5)
		}
		if len(a.key) != len(b.key) {
			return len(a.key) - len(b.key)
		}
		return bytes.Compare(a.key, b.key)
	})
	out := cborHead(majorMap, uint64(len(sorted)))
	for _, p := range sorted {
		out = append(out, p.key...)
		out = append(out, p.value...)
	}
	return out
}

// cborValue encodes the value kinds an extension output carries.
func cborValue(v any) ([]byte, error) {
	switch v := v.(type) {
	case bool:
		return cborBool(v), nil
	case int:
		return cborInt(int64(v)), nil
	case int64:
		return cborInt(v), nil
	case uint:
		return cborUint(uint64(v)), nil
	case uint32:
		return cborUint(uint64(v)), nil
	case uint64:
		return cborUint(v), nil
	case string:
		return cborText(v), nil
	case []byte:
		return cborBytes(v), nil
	case map[string]any:
		pairs := make([]cborPair, 0, len(v))
		for k, inner := range v {
			value, err := cborValue(inner)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			pairs = append(pairs, cborPair{cborText(k), value})
		}
		return cborMap(pairs...), nil
	}
	return nil, fmt.Errorf("webauthntest: cannot CBOR-encode %T", v)
}

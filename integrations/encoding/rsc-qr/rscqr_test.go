package rscqr_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"rsc.io/qr"

	"github.com/gopernicus/gopernicus/sdk"

	rscqr "github.com/gopernicus/gopernicus/integrations/encoding/rsc-qr"
)

const (
	otpauthURI = "otpauth://totp/Gopernicus%20CMS:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Gopernicus%20CMS&algorithm=SHA1&digits=6&period=30"

	// quietZone is the light border the views/goth renderer draws; Modules
	// leaves it out.
	quietZone = 4

	// formatM0 is the 15-bit format information for level M, mask 0, after
	// the BCH code and the 101010000010010 XOR mask (ISO/IEC 18004 Annex C).
	formatM0 = 0b101010000010010
)

var matrix = []struct {
	name   string
	text   string
	size   int
	sha256 string
}{
	{"numeric", "1", 21, "b15e403473f4e2e1a772c55ef5b6d47fa550935b3e8adca6d674735ebd7a13dd"},
	{"alphanumeric", "HELLO WORLD", 21, "4c1240bbfb84568fca60ed756358070c8a58cda10a44492afe598922fc3a385b"},
	{"otpauth", otpauthURI, 49, "800a29ead4ec3a07b84573c3656cb4f0f5e9f25bd6010f917f3e5ed2c708b73e"},
	{"byte capacity", strings.Repeat("a", 2331), 177, "30456687d1b06fbc5b9363dd6dcd769233fa4e688a5f817245dd74e01dfc6bbf"},
}

func TestModulesMatrix(t *testing.T) {
	for _, tc := range matrix {
		t.Run(tc.name, func(t *testing.T) {
			m, err := rscqr.Modules(tc.text)
			if err != nil {
				t.Fatalf("Modules: %v", err)
			}
			if len(m) != tc.size {
				t.Fatalf("rows = %d, want %d", len(m), tc.size)
			}
			for y, row := range m {
				if len(row) != tc.size {
					t.Fatalf("row %d has %d modules, want %d", y, len(row), tc.size)
				}
			}
			if got := digest(m); got != tc.sha256 {
				t.Errorf("matrix sha256 = %s, want %s", got, tc.sha256)
			}
			assertFinders(t, m)
			assertTiming(t, m)
			version := (tc.size - 17) / 4
			if !m[4*version+9][8] {
				t.Error("dark module at (8, 4v+9) is light")
			}
			if got := formatBits(m); got != formatM0 {
				t.Errorf("format information = %015b, want %015b (level M, mask 0)", got, formatM0)
			}
		})
	}
}

func TestModulesLevelM(t *testing.T) {
	m, err := rscqr.Modules(otpauthURI)
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	for _, tc := range []struct {
		level qr.Level
		same  bool
	}{{qr.M, true}, {qr.L, false}} {
		code, err := qr.Encode(otpauthURI, tc.level)
		if err != nil {
			t.Fatalf("qr.Encode: %v", err)
		}
		if got := equalCode(m, code); got != tc.same {
			t.Errorf("matrix equals qr.Encode at level %d = %v, want %v", tc.level, got, tc.same)
		}
	}
}

// TestModulesQuietZone pins the contract the renderer relies on: the matrix
// starts at the finder pattern (no border of its own), and padding it with a
// 4-module light border reproduces the code exactly as rsc.io/qr defines it at
// every offset, outside included.
func TestModulesQuietZone(t *testing.T) {
	m, err := rscqr.Modules(otpauthURI)
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	size := len(m)
	if !m[0][0] || !m[0][size-1] || !m[size-1][0] {
		t.Fatal("matrix corners are light: Modules must not include a quiet zone")
	}

	padded := make([][]bool, size+2*quietZone)
	for y := range padded {
		padded[y] = make([]bool, size+2*quietZone)
	}
	for y, row := range m {
		copy(padded[y+quietZone][quietZone:], row)
	}

	code, err := qr.Encode(otpauthURI, qr.M)
	if err != nil {
		t.Fatalf("qr.Encode: %v", err)
	}
	for y := range padded {
		for x := range padded[y] {
			if want := code.Black(x-quietZone, y-quietZone); padded[y][x] != want {
				t.Fatalf("padded (%d,%d) = %v, want %v", x, y, padded[y][x], want)
			}
		}
	}
	for i := range padded {
		for _, p := range [][2]int{{i, 0}, {i, quietZone - 1}, {0, i}, {quietZone - 1, i},
			{i, len(padded) - 1}, {len(padded) - 1, i}} {
			if padded[p[1]][p[0]] {
				t.Fatalf("quiet zone module (%d,%d) is dark", p[0], p[1])
			}
		}
	}
}

func TestModulesRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"over byte capacity", strings.Repeat("a", 2332)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := rscqr.Modules(tc.text)
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("error = %v, want sdk.ErrInvalidInput", err)
			}
			if m != nil {
				t.Errorf("matrix = %d rows, want nil", len(m))
			}
		})
	}
}

func digest(m [][]bool) string {
	h := sha256.New()
	for _, row := range m {
		for _, dark := range row {
			if dark {
				h.Write([]byte{1})
			} else {
				h.Write([]byte{0})
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func equalCode(m [][]bool, code *qr.Code) bool {
	if len(m) != code.Size {
		return false
	}
	for y, row := range m {
		for x, dark := range row {
			if dark != code.Black(x, y) {
				return false
			}
		}
	}
	return true
}

// assertFinders checks the three 7x7 finder patterns (dark ring, light ring,
// dark 3x3 core) and their light separators.
func assertFinders(t *testing.T, m [][]bool) {
	t.Helper()
	size := len(m)
	for _, origin := range [][2]int{{0, 0}, {size - 7, 0}, {0, size - 7}} {
		for dy := 0; dy < 7; dy++ {
			for dx := 0; dx < 7; dx++ {
				ring := max(abs(dx-3), abs(dy-3))
				want := ring != 2
				if got := m[origin[1]+dy][origin[0]+dx]; got != want {
					t.Fatalf("finder at (%d,%d): module (%d,%d) = %v, want %v", origin[0], origin[1], dx, dy, got, want)
				}
			}
		}
	}
	for i := 0; i < 8; i++ {
		for _, p := range [][2]int{
			{7, i}, {i, 7}, // top-left
			{size - 8, i}, {size - 1 - i, 7}, // top-right
			{7, size - 1 - i}, {i, size - 8}, // bottom-left
		} {
			if m[p[1]][p[0]] {
				t.Fatalf("separator module (%d,%d) is dark", p[0], p[1])
			}
		}
	}
}

func assertTiming(t *testing.T, m [][]bool) {
	t.Helper()
	for i := 8; i < len(m)-8; i++ {
		want := i%2 == 0
		if m[6][i] != want || m[i][6] != want {
			t.Fatalf("timing module %d: row %v column %v, want %v", i, m[6][i], m[i][6], want)
		}
	}
}

// formatBits reads the copy of the format information around the top-left
// finder, most significant bit first.
func formatBits(m [][]bool) int {
	cells := [][2]int{
		{0, 8}, {1, 8}, {2, 8}, {3, 8}, {4, 8}, {5, 8}, {7, 8}, {8, 8},
		{8, 7}, {8, 5}, {8, 4}, {8, 3}, {8, 2}, {8, 1}, {8, 0},
	}
	v := 0
	for _, c := range cells {
		v <<= 1
		if m[c[1]][c[0]] {
			v |= 1
		}
	}
	return v
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

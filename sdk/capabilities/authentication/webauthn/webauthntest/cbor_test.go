package webauthntest

import (
	"encoding/hex"
	"math"
	"testing"
)

// TestCBORVectors pins the encoder to RFC 8949 Appendix A for every kind it writes.
func TestCBORVectors(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want string
	}{
		{"0", cborUint(0), "00"},
		{"1", cborUint(1), "01"},
		{"10", cborUint(10), "0a"},
		{"23", cborUint(23), "17"},
		{"24", cborUint(24), "1818"},
		{"25", cborUint(25), "1819"},
		{"100", cborUint(100), "1864"},
		{"1000", cborUint(1000), "1903e8"},
		{"1000000", cborUint(1000000), "1a000f4240"},
		{"1000000000000", cborUint(1000000000000), "1b000000e8d4a51000"},
		{"max uint64", cborUint(math.MaxUint64), "1bffffffffffffffff"},
		{"int 100", cborInt(100), "1864"},
		{"-1", cborInt(-1), "20"},
		{"-10", cborInt(-10), "29"},
		{"-100", cborInt(-100), "3863"},
		{"-1000", cborInt(-1000), "3903e7"},
		{"min int64", cborInt(math.MinInt64), "3b7fffffffffffffff"},
		{"false", cborBool(false), "f4"},
		{"true", cborBool(true), "f5"},
		{"h''", cborBytes(nil), "40"},
		{"h'01020304'", cborBytes([]byte{1, 2, 3, 4}), "4401020304"},
		{`""`, cborText(""), "60"},
		{`"a"`, cborText("a"), "6161"},
		{`"IETF"`, cborText("IETF"), "6449455446"},
		{`"ü"`, cborText("ü"), "62c3bc"},
		{"{}", cborMap(), "a0"},
		{"{1: 2, 3: 4}", cborMap(cborPair{cborInt(3), cborInt(4)}, cborPair{cborInt(1), cborInt(2)}), "a201020304"},
		{`{"a".."e"}`, cborMap(
			cborPair{cborText("e"), cborText("E")}, cborPair{cborText("c"), cborText("C")},
			cborPair{cborText("a"), cborText("A")}, cborPair{cborText("d"), cborText("D")},
			cborPair{cborText("b"), cborText("B")},
		), "a56161614161626142616361436164614461656145"},
	}
	for _, tt := range tests {
		if got := hex.EncodeToString(tt.got); got != tt.want {
			t.Errorf("%s = %s, want %s", tt.name, got, tt.want)
		}
	}
}

// TestCBORCanonicalOrder pins CTAP2 key order: major type, then length, then bytes.
func TestCBORCanonicalOrder(t *testing.T) {
	cose := cborMap(
		cborPair{cborInt(-3), cborInt(0)}, cborPair{cborInt(-1), cborInt(0)},
		cborPair{cborInt(3), cborInt(0)}, cborPair{cborInt(-2), cborInt(0)},
		cborPair{cborInt(1), cborInt(0)},
	)
	if got, want := hex.EncodeToString(cose), "a5"+"0100"+"0300"+"2000"+"2100"+"2200"; got != want {
		t.Errorf("COSE key order = %s, want 1, 3, -1, -2, -3: %s", got, want)
	}
	attestation := cborMap(
		cborPair{cborText("authData"), cborBytes(nil)},
		cborPair{cborText("attStmt"), cborMap()},
		cborPair{cborText("fmt"), cborText("none")},
	)
	want := "a3" + "63666d74" + "646e6f6e65" + "6761747453746d74" + "a0" + "68617574684461746140"
	if got := hex.EncodeToString(attestation); got != want {
		t.Errorf("attestation object order = %s, want fmt, attStmt, authData: %s", got, want)
	}
	if got, want := hex.EncodeToString(cborMap(cborPair{cborText("aa"), cborInt(0)}, cborPair{cborInt(24), cborInt(0)})), "a2"+"181800"+"62616100"; got != want {
		t.Errorf("mixed-major order = %s, want the integer key first: %s", got, want)
	}
}

func TestCBORValue(t *testing.T) {
	got, err := cborValue(map[string]any{
		"credProtect": uint64(2), "b": true, "i": -1, "s": "x", "bin": []byte{1}, "u32": uint32(24), "nested": map[string]any{"k": int64(1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "a7" + "6162f5" + "616920" + "61736178" + "6362696e4101" + "63753332" + "1818" + "666e6573746564a1616b01" + "6b6372656450726f7465637402"
	if hex.EncodeToString(got) != want {
		t.Errorf("cborValue = %x, want %s", got, want)
	}
	if _, err := cborValue(map[string]any{"f": 1.5}); err == nil {
		t.Error("cborValue(float) = nil error, want a refusal")
	}
}

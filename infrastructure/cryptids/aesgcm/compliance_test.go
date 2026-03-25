package aesgcm_test

import (
	"crypto/rand"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids/aesgcm"
	"github.com/gopernicus/gopernicus/infrastructure/cryptids/cryptidstest"
)

func TestEncrypterCompliance(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	enc, err := aesgcm.New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cryptidstest.RunEncrypterSuite(t, enc)
}

func TestNewKeyValidation(t *testing.T) {
	tests := []struct {
		name    string
		keyLen  int
		wantErr bool
	}{
		{"16 bytes", 16, true},
		{"24 bytes", 24, true},
		{"32 bytes", 32, false},
		{"64 bytes", 64, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keyLen)
			_, err := aesgcm.New(key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New(%d bytes): err=%v, wantErr=%v", tt.keyLen, err, tt.wantErr)
			}
		})
	}
}

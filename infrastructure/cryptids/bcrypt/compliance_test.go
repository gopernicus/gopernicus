package bcrypt_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids/bcrypt"
	"github.com/gopernicus/gopernicus/infrastructure/cryptids/cryptidstest"
)

func TestHasherCompliance(t *testing.T) {
	hasher := bcrypt.NewHasher(10)
	cryptidstest.RunHasherSuite(t, hasher)
}

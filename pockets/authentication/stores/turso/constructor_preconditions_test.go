package turso

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestRawStoreDatabasePrecondition(t *testing.T) {
	constructors := []struct {
		name     string
		database func(*tursodb.DB) *tursodb.DB
	}{
		{"NewAPIKeyStore", func(db *tursodb.DB) *tursodb.DB { return NewAPIKeyStore(db).db }},
		{"NewActiveSessionStore", func(db *tursodb.DB) *tursodb.DB { return NewActiveSessionStore(db).db }},
		{"NewAuthGrantStore", func(db *tursodb.DB) *tursodb.DB { return NewAuthGrantStore(db).db }},
		{"NewChallengeStore", func(db *tursodb.DB) *tursodb.DB { return NewChallengeStore(db).db }},
		{"NewContactChangeStore", func(db *tursodb.DB) *tursodb.DB { return NewContactChangeStore(db).db }},
		{"NewCredentialMutationStore", func(db *tursodb.DB) *tursodb.DB { return NewCredentialMutationStore(db).db }},
		{"NewIdentifierStore", func(db *tursodb.DB) *tursodb.DB { return NewIdentifierStore(db).db }},
		{"NewInvitationStore", func(db *tursodb.DB) *tursodb.DB { return NewInvitationStore(db).db }},
		{"NewOAuthAccountStore", func(db *tursodb.DB) *tursodb.DB { return NewOAuthAccountStore(db).db }},
		{"NewOAuthStateStore", func(db *tursodb.DB) *tursodb.DB { return NewOAuthStateStore(db).db }},
		{"NewPasswordResetStore", func(db *tursodb.DB) *tursodb.DB { return NewPasswordResetStore(db).db }},
		{"NewPasswordStore", func(db *tursodb.DB) *tursodb.DB { return NewPasswordStore(db).db }},
		{"NewPasswordlessStore", func(db *tursodb.DB) *tursodb.DB { return NewPasswordlessStore(db).db }},
		{"NewSecurityEventStore", func(db *tursodb.DB) *tursodb.DB { return NewSecurityEventStore(db).db }},
		{"NewServiceAccountStore", func(db *tursodb.DB) *tursodb.DB { return NewServiceAccountStore(db).db }},
		{"NewSessionStore", func(db *tursodb.DB) *tursodb.DB { return NewSessionStore(db).db }},
		{"NewUserAdminStore", func(db *tursodb.DB) *tursodb.DB { return NewUserAdminStore(db).db }},
		{"NewUserStore", func(db *tursodb.DB) *tursodb.DB { return NewUserStore(db).db }},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			// A zero wrapper cannot perform I/O. Construction only borrows it.
			db := &tursodb.DB{}
			if got := constructor.database(db); got != db {
				t.Fatal("constructor did not retain the supplied database")
			}
			defer func() {
				recovered := recover()
				if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "nil database") {
					t.Fatalf("panic = %v; want nil database diagnostic", recovered)
				}
			}()
			constructor.database(nil)
		})
	}
}

func TestRepositoriesRejectNilDatabase(t *testing.T) {
	if _, err := Repositories(t.Context(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Repositories nil database: %v", err)
	}
}

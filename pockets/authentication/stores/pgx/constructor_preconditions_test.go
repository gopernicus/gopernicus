package pgx

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestRawStoreDatabasePrecondition(t *testing.T) {
	constructors := []struct {
		name     string
		database func(*pgxdb.DB) *pgxdb.DB
	}{
		{"NewAPIKeyStore", func(db *pgxdb.DB) *pgxdb.DB { return NewAPIKeyStore(db).db }},
		{"NewActiveSessionStore", func(db *pgxdb.DB) *pgxdb.DB { return NewActiveSessionStore(db).db }},
		{"NewAuthGrantStore", func(db *pgxdb.DB) *pgxdb.DB { return NewAuthGrantStore(db).db }},
		{"NewChallengeStore", func(db *pgxdb.DB) *pgxdb.DB { return NewChallengeStore(db).db }},
		{"NewContactChangeStore", func(db *pgxdb.DB) *pgxdb.DB { return NewContactChangeStore(db).db }},
		{"NewCredentialMutationStore", func(db *pgxdb.DB) *pgxdb.DB { return NewCredentialMutationStore(db).db }},
		{"NewIdentifierStore", func(db *pgxdb.DB) *pgxdb.DB { return NewIdentifierStore(db).db }},
		{"NewInvitationStore", func(db *pgxdb.DB) *pgxdb.DB { return NewInvitationStore(db).db }},
		{"NewOAuthAccountStore", func(db *pgxdb.DB) *pgxdb.DB { return NewOAuthAccountStore(db).db }},
		{"NewOAuthStateStore", func(db *pgxdb.DB) *pgxdb.DB { return NewOAuthStateStore(db).db }},
		{"NewPasswordResetStore", func(db *pgxdb.DB) *pgxdb.DB { return NewPasswordResetStore(db).db }},
		{"NewPasswordStore", func(db *pgxdb.DB) *pgxdb.DB { return NewPasswordStore(db).db }},
		{"NewPasswordlessStore", func(db *pgxdb.DB) *pgxdb.DB { return NewPasswordlessStore(db).db }},
		{"NewSecurityEventStore", func(db *pgxdb.DB) *pgxdb.DB { return NewSecurityEventStore(db).db }},
		{"NewServiceAccountStore", func(db *pgxdb.DB) *pgxdb.DB { return NewServiceAccountStore(db).db }},
		{"NewSessionStore", func(db *pgxdb.DB) *pgxdb.DB { return NewSessionStore(db).db }},
		{"NewUserAdminStore", func(db *pgxdb.DB) *pgxdb.DB { return NewUserAdminStore(db).db }},
		{"NewUserStore", func(db *pgxdb.DB) *pgxdb.DB { return NewUserStore(db).db }},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			// A zero wrapper cannot perform I/O. Construction only borrows it.
			db := &pgxdb.DB{}
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

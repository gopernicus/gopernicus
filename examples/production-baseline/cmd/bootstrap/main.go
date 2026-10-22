// Command bootstrap grants the global admin role to one subject: the first
// platform admin, who then administers roles through the server's role routes.
// It writes through the authorization pocket over the same recording pgx store
// and under the same runtime mode as the server, so the grant lands in the
// change history with System "bootstrap". A raw SQL insert would leave no audit
// row and is not supported.
//
// The subject id is the local user id the authentication pocket provisioned at
// the person's first provider login: the user_id of their
// external_subject_provisioned security event, or of their external_subjects
// row. Rerunning is safe: an existing grant answers no_change and records
// nothing.
//
//	APP_RUNTIME_MODE=production go run ./cmd/bootstrap -subject-type user -subject-id <id>
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	authorizationpgx "github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"
)

// adminRole is the global role the server's role routes require.
const adminRole = "admin"

// source attributes every bootstrap grant in the change history.
var source = audit.Source{System: "bootstrap", Reason: "first platform admin"}

func main() {
	subjectType := flag.String("subject-type", "user", "the subject's principal type")
	subjectID := flag.String("subject-id", "", "the local user id provisioned at the subject's first provider login (required)")
	flag.Parse()

	// The mode comes from the process environment only, as for the server:
	// .env is read in development and never opened in production.
	mode, err := environment.LoadEnvForMode("APP_RUNTIME_MODE")
	if err != nil {
		slog.Error("load environment", "error", err)
		os.Exit(1)
	}

	logOpts := logging.Options{Format: "text"}
	if err := environment.ParseEnvTags("", &logOpts); err != nil {
		slog.Error("configure logging", "error", err)
		os.Exit(1)
	}
	log := logging.New(logOpts)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := run(ctx, log, mode, sdk.Principal{Type: *subjectType, ID: *subjectID}, os.Stdout); err != nil {
		log.Error("bootstrap", "error", err)
		os.Exit(1)
	}
}

// run opens the database under mode — production refuses a connection whose
// TLS does not verify the server — and grants the admin role to subject.
func run(ctx context.Context, log *slog.Logger, mode environment.Mode, subject sdk.Principal, out io.Writer) error {
	if subject.ID == "" {
		return errors.New("bootstrap: -subject-id is required")
	}
	var dbCfg pgxdb.Config
	if err := environment.ParseEnvTags("", &dbCfg); err != nil {
		return err
	}
	dbCfg.RuntimeMode = mode
	dbCfg.Logger = log
	db, err := pgxdb.Open(ctx, dbCfg)
	if err != nil {
		return err
	}
	defer db.Close()

	repos, err := authorizationpgx.Repositories(ctx, db,
		authorizationpgx.WithAudit(), authorizationpgx.WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()))
	if err != nil {
		return err
	}
	components, err := newAuthorization(mode, log, repos)
	if err != nil {
		return err
	}
	return grantAdmin(ctx, components, subject, out)
}

// newAuthorization builds the authorization pocket with the server's posture:
// production requires the store to record changes and the audit reader to be
// wired. A global role assignment is shape-free, so the server's resource
// model is not needed here.
func newAuthorization(mode environment.Mode, log *slog.Logger, repos authorization.Repositories) (authorization.Components, error) {
	return authorization.New(repos, authorization.WithRuntimeMode(mode), authorization.WithLogger(log))
}

// grantAdmin assigns the global admin role to subject and prints the outcome:
// applied, with the change-history event that records the grant, or
// no_change when the subject already holds it.
func grantAdmin(ctx context.Context, components authorization.Components, subject sdk.Principal, out io.Writer) error {
	result, err := components.Mutations.AssignRole(audit.WithSource(ctx, source), mutations.AssignRoleCommand{
		Subject: subject,
		Role:    adminRole,
		Scope:   tuples.Global(),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: global %s for %s %s\n", result.Outcome, adminRole, subject.Type, subject.ID)
	if result.Outcome != mutations.OutcomeApplied {
		return nil
	}
	page, err := components.Audit.List(ctx, audit.Filter{SubjectType: subject.Type, SubjectID: subject.ID}, list.Request{})
	if err != nil {
		return err
	}
	for _, record := range page.Items {
		change := record.Change
		if record.Source.System == source.System && change.Action == audit.ActionAdded &&
			change.Tuple.Relation == adminRole && change.Tuple.Scope == tuples.Global() {
			fmt.Fprintf(out, "audit event %s at %s\n", record.EventID, record.OccurredAt.Format(time.RFC3339))
			return nil
		}
	}
	return errors.New("bootstrap: the grant applied but the change history holds no bootstrap record for it")
}

package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	authorizationmemory "github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// TestGrantAdminIsAuditedAndIdempotent runs the bootstrap twice over the
// recording memory store in production: the first run grants global admin and
// records it with System "bootstrap"; the second answers no_change and records
// nothing.
func TestGrantAdminIsAuditedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	store := authorizationmemory.New(authorizationmemory.WithAudit(), authorizationmemory.WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()))
	components, err := newAuthorization(environment.ModeProduction, slog.New(slog.NewTextHandler(io.Discard, nil)), authorization.Repositories{
		Tuples: store.Tuples(), Mutations: store.Mutations(), Operations: store.Operations(), Audit: store.Audit(),
	})
	if err != nil {
		t.Fatalf("newAuthorization: %v", err)
	}
	subject := sdk.Principal{Type: "user", ID: "user-1"}
	history := func() []audit.Record {
		t.Helper()
		page, err := components.Audit.List(ctx, audit.Filter{SubjectType: subject.Type, SubjectID: subject.ID}, list.Request{})
		if err != nil {
			t.Fatalf("audit list: %v", err)
		}
		return page.Items
	}

	var out strings.Builder
	if err := grantAdmin(ctx, components, subject, &out); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !strings.HasPrefix(out.String(), "applied:") || !strings.Contains(out.String(), "audit event ") {
		t.Fatalf("first run printed %q, want applied with an audit event", out.String())
	}
	admin, err := components.Roles.HasRole(ctx, subject, adminRole)
	if err != nil || !admin {
		t.Fatalf("HasRole(admin) = %v, %v; want true", admin, err)
	}
	records := history()
	if len(records) != 1 {
		t.Fatalf("change history holds %d records, want 1", len(records))
	}
	if got := records[0].Source; got.System != "bootstrap" || got.Reason != "first platform admin" {
		t.Fatalf("record source = %+v, want System bootstrap, Reason first platform admin", got)
	}
	if !strings.Contains(out.String(), records[0].EventID) {
		t.Fatalf("first run printed %q, want event id %s", out.String(), records[0].EventID)
	}

	out.Reset()
	if err := grantAdmin(ctx, components, subject, &out); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if !strings.HasPrefix(out.String(), "no_change:") || strings.Contains(out.String(), "audit event") {
		t.Fatalf("rerun printed %q, want no_change and no audit event", out.String())
	}
	if n := len(history()); n != 1 {
		t.Fatalf("rerun left %d records, want 1", n)
	}
}

// TestRunRequiresSubjectID proves a missing -subject-id fails before any
// connection is opened.
func TestRunRequiresSubjectID(t *testing.T) {
	err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), environment.ModeProduction, sdk.Principal{Type: "user"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "-subject-id") {
		t.Fatalf("err = %v, want the -subject-id requirement", err)
	}
}

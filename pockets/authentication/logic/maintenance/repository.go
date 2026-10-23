// Package maintenance is the authentication pocket's retention service: one
// bounded pass that purges the expired and terminal rows of the authentication
// tables and archives-then-purges the security-event rail one verified chunk at
// a time, leaving evidence on the rail for every disposal.
//
// It imports the leaf domain packages (securityevent, challenge) and never the
// authentication service package (guard G36). The pocket root builds it as
// Components.Maintenance; the host runs it — Service.Run in process, or Pass
// from a jobs schedule.
package maintenance

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErrBelowRetentionFloor is returned by Repository.PurgeSecurityEventsBefore
// when before is younger than the database's retention floor (365 days in the
// bundled stores). The store refuses — it never clamps — so the archive and the
// purge always agree on one cutoff. It wraps sdk.ErrInvalidInput.
var ErrBelowRetentionFloor = fmt.Errorf("maintenance: cutoff is younger than the database retention floor: %w", sdk.ErrInvalidInput)

// Repository is the retention port: a cross-aggregate, use-case-shaped
// capability over the authentication tables, like passwordless.Repository. A
// store adopts it whole or not at all.
//
// It is NOT one transaction across methods: every call is its own bounded
// statement (or one short transaction), so a pass interrupted between calls
// leaves every row either kept or disposed of, never half-written. Every
// "eligible" comparison is <=: a row exactly AT before is eligible. Every limit
// bounds one statement; limit <= 0 is sdk.ErrInvalidInput (never "unbounded"),
// and the service never passes more than MaxBatchSize.
//
// It must be backed by the database that holds the rail: the rail purge
// appends its evidence row to the table it purged.
type Repository interface {
	// PurgeExpiredSessions deletes up to limit sessions, of both profiles,
	// whose expires_at <= before, and returns how many it deleted.
	PurgeExpiredSessions(ctx context.Context, before time.Time, limit int) (int, error)
	// PurgeExpiredOAuthStates deletes up to limit OAuth states whose
	// expires_at <= before.
	PurgeExpiredOAuthStates(ctx context.Context, before time.Time, limit int) (int, error)
	// PurgeExpiredGrants deletes up to limit authentication grants whose
	// expires_at <= before.
	PurgeExpiredGrants(ctx context.Context, before time.Time, limit int) (int, error)
	// PurgeExpiredContactChanges deletes up to limit pending contact changes
	// whose expires_at <= before.
	PurgeExpiredContactChanges(ctx context.Context, before time.Time, limit int) (int, error)
	// PurgeInvitations deletes, in one bounded statement, up to limit
	// invitations that are terminal (accepted, declined, cancelled) with
	// updated_at <= before, or pending with expires_at <= before. An accepting
	// row is never purged: a claim never expires.
	PurgeInvitations(ctx context.Context, before time.Time, limit int) (int, error)
	// CountStaleAcceptingInvitations counts the accepting invitations whose
	// updated_at <= before: claims nobody settled. Maintenance reports them and
	// never cancels one.
	CountStaleAcceptingInvitations(ctx context.Context, before time.Time) (int, error)
	// PruneOAuth2 deletes the OAuth2 authorization codes, client metadata and
	// refresh history whose expires_at <= now, up to limit rows per table, and
	// returns the sum. It is the bounded twin of oauth2.Repository.Prune, which
	// the service never calls.
	PruneOAuth2(ctx context.Context, now time.Time, limit int) (int, error)
	// ExportSecurityEvents returns up to limit rail rows with created_at <=
	// before, after the cursor, in ascending (created_at, id) order — EXTENDED
	// past limit to every row tied at the last created_at, so a page always ends
	// on a whole instant — and the cursor of its last row (after, unchanged, for
	// an empty page).
	ExportSecurityEvents(ctx context.Context, before time.Time, after ExportCursor, limit int) ([]ArchivedEvent, ExportCursor, error)
	// PurgeSecurityEventsBefore deletes up to limit rail rows with created_at <=
	// before, oldest first, and appends ONE security_events_purged row
	// recording the call — a zero count included — in the same unit of work. A
	// before younger than the database floor is ErrBelowRetentionFloor with
	// nothing deleted. The floor is measured on the store's own clock.
	PurgeSecurityEventsBefore(ctx context.Context, before time.Time, limit int) (PurgeResult, error)
}

// ExportCursor is a position on the security-event rail in ascending
// (created_at, id) order. The zero value is the start of the rail.
type ExportCursor struct {
	CreatedAt time.Time
	ID        string
}

// ArchivedEvent is one exported rail row: the domain event plus the evidence
// columns no domain type carries. Seq is the store's insertion sequence (nil
// where the store keeps none — turso and memory); RecordedAt is the database's
// own insertion time (nil where the store keeps none — memory).
type ArchivedEvent struct {
	securityevent.SecurityEvent
	Seq        *int64
	RecordedAt *time.Time
}

// PurgeResult is one rail purge call: the cutoff it purged through and how many
// rows it removed.
type PurgeResult struct {
	Cutoff time.Time
	Count  int
}

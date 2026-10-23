// Package authentication assembles the authentication pocket's public use cases,
// optional HTTP adapter and host-owned delivery runtime.
package authentication

import (
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
)

// Components holds the independently usable components built by New. Invitations
// and External are nil when disabled. HTTP registration and delivery execution
// are host-owned. External exposes revocation stats and the heartbeat of the
// engine Authentication already uses.
//
// SecurityEvents is the ONE audit recorder Authentication, External, OAuth2 and
// Invitations record through; it is never nil (without
// Repositories.SecurityEvents it records nothing). A host that bridges events into the rail hands it this recorder
// rather than building another, so AuditWriteFailures counts every lost row.
//
// Maintenance is the retention service over Repositories.Maintenance,
// Challenges and SecurityEvents; it is never nil and starts nothing. The host
// runs it — Maintenance.Run in process, or Maintenance.Pass from a schedule —
// and says so with MaintenanceConfig.MaintenanceRunnerAcknowledged.
type Components struct {
	Authentication *authlogic.Service
	External       *authlogic.ExternalAuthenticator
	OAuth2         *oauth2.Service
	Invitations    *invitations.Service
	HTTP           *authenticationhttp.Adapter
	Delivery       *delivery.Runtime
	SecurityEvents *securityevent.Recorder
	Maintenance    *maintenance.Service
}

// AuditWriteFailures returns how many security-event writes have failed since
// New: rows the audit rail lost while the flows that produced them completed.
func (c *Components) AuditWriteFailures() uint64 {
	return c.SecurityEvents.WriteFailures()
}

// AuditRowsSuppressed returns how many failure rows the per-IP failure-row
// budget has refused since New: unauthenticated failures past the budget that
// wrote no row.
func (c *Components) AuditRowsSuppressed() uint64 {
	return c.SecurityEvents.SuppressedRows()
}

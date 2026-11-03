package delivery

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// ErrRoleInvalid is returned by ParseRole, and by the pocket root's New, for a
// Role other than "both", "enqueue", or "process".
var ErrRoleInvalid = fmt.Errorf(`delivery: role must be "both", "enqueue", or "process": %w`, sdk.ErrInvalidInput)

// Mode is the host's EXPLICIT selection of the outbound-delivery execution
// model (authv3-delivery-refactor §"No automatic production fallback"). It is a
// REQUIRED enum with no default — construction never infers a mode from a non-nil
// collaborator, so a host cannot accidentally ship an ephemeral posture or a jobs
// posture whose runtime is never started. An empty value is ErrModeRequired
// and an unknown value is ErrModeInvalid.
//
//   - ModeJobs: durable delivery. The generic jobs runtime executes the
//     delivery command, with retry/status/fencing surviving restart. Requires the
//     narrow queue capability (Config.DeliveryDispatcher) and Config.DeliveryEncrypter;
//     production additionally requires Config.DeliveryJobsAcknowledged (the host runs
//     the jobs delivery runtime).
//   - ModeInProcess: bounded ephemeral delivery. The same delivery processor
//     runs behind a process-local bounded queue and fixed worker pool; accepted work
//     does NOT survive a restart. Production requires the explicit crash-loss
//     acknowledgment Config.DeliveryEphemeralAcknowledged.
//   - ModeOff: no delivery runtime. Allowed only when no configured auth
//     capability can send — a wired delivery dispatcher makes off contradictory
//     (ErrDeliveryOffButDeliverable), and enabling passwordless under off is
//     ErrPasswordlessDeliveryRequired.
//
// The host owns the runtime lifecycle in every mode: Register starts no worker.
type Mode string

const (
	ModeOff       Mode = "off"
	ModeInProcess Mode = "in_process"
	ModeJobs      Mode = "jobs"
)

// Role is the part of DeliveryMode "jobs" one instance plays when a host splits
// enqueueing from processing across processes. The zero value is RoleBoth.
//
//   - RoleBoth: the instance enqueues and builds the processor. It is the only
//     role every Mode accepts.
//   - RoleEnqueue: the instance enqueues and never sends. It needs no Mailer
//     and no MailFrom, and builds no processor, so Runtime.JobRuntime reports
//     ok=false.
//   - RoleProcess: the instance builds the processor and sends. It needs the
//     Mailer and MailFrom and may run without a token signer.
type Role string

const (
	RoleBoth    Role = "both"
	RoleEnqueue Role = "enqueue"
	RoleProcess Role = "process"
)

// ParseRole maps s to a Role. Empty is RoleBoth; any other unknown value is
// ErrRoleInvalid.
func ParseRole(s string) (Role, error) {
	switch r := Role(s); r {
	case "", RoleBoth:
		return RoleBoth, nil
	case RoleEnqueue, RoleProcess:
		return r, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrRoleInvalid, s)
	}
}

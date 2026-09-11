package delivery

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

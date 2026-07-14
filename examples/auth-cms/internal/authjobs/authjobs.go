// Package authjobs is the host-owned composition adapter (authv3-delivery-refactor
// AV3D-3.1) that runs authentication's encrypted outbound delivery on the generic
// jobs feature. It is the ONE place that imports BOTH features/authentication and
// features/jobs; neither feature core imports the other (constitution rule 6), and
// the composition happens here, in the host, over the two features' stdlib-typed
// seams:
//
//   - Dispatcher maps authentication's DeliveryDispatcher (Submit/Replace/
//     LatestStatus) onto the generic jobs fenced primitives (EnqueueOnce/Replace/
//     LatestStatusByKey). It submits every rail under one job kind
//     (auth.DeliveryJobKind); the rail and purpose travel inside the sealed envelope,
//     not as the queue's routing kind, so the params it drops are already carried
//     durably.
//   - FencedRuntimeConfig maps the generic job handler onto authentication's delivery
//     processor: it registers auth's DeliveryJobRuntime().Handle under the delivery
//     kind and its Discard as the per-kind dead-letter hook, bridging the jobs
//     FencedClaim to the auth DeliveryClaim (payload, attempt, and the lease-fenced
//     checkpoint closure).
//
// Construction order is the adapter's responsibility and the host's: the jobs Service
// is built from the fenced queue, the Dispatcher from that Service, and the auth
// Service from the Dispatcher; only AFTER the auth Service is fully built does the
// host read DeliveryJobRuntime() and hand it here to build the jobs FencedRuntime — so
// no handler can run against a half-built auth Service, and the host starts the
// runtime explicitly (the features start no goroutine).
package authjobs

import (
	"context"
	"encoding/json"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/jobs"
	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// Enqueuer is the narrow slice of the generic jobs Service the Dispatcher needs,
// composed from the sdk keyed-work submission protocol: this bridge requires all
// three segregated capabilities — idempotent admission, atomic replace/supersede,
// and latest-by-key status. *jobs.Service satisfies it.
type Enqueuer interface {
	work.Enqueuer
	work.Replacer
	work.StatusReader
}

// Compile-time proof the adapter satisfies authentication's stdlib-typed delivery
// transport seam without either feature importing the other.
var _ auth.DeliveryDispatcher = (*Dispatcher)(nil)

// Dispatcher bridges auth.DeliveryDispatcher to the generic jobs fenced primitives.
type Dispatcher struct {
	jobs Enqueuer
	kind string
}

// NewDispatcher builds the dispatcher over the generic jobs Service. Every delivery
// command is submitted under auth.DeliveryJobKind (the single kind the one delivery
// handler processes); the per-command rail/purpose ride inside the sealed payload.
func NewDispatcher(j Enqueuer) *Dispatcher {
	return &Dispatcher{jobs: j, kind: auth.DeliveryJobKind}
}

// Submit admits payload under logicalKey exactly once. The kind/purpose params are
// carried inside the sealed envelope, so they are dropped here in favor of the single
// delivery job kind.
func (d *Dispatcher) Submit(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (string, error) {
	return d.jobs.EnqueueOnce(ctx, d.kind, logicalKey, payload)
}

// Replace supersedes the active generation holding logicalKey and admits a fresh one.
func (d *Dispatcher) Replace(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (string, error) {
	return d.jobs.Replace(ctx, d.kind, logicalKey, payload)
}

// LatestStatus returns the generic job lifecycle string for the latest generation
// holding logicalKey; the authentication feature normalizes it into its stable status.
func (d *Dispatcher) LatestStatus(ctx context.Context, logicalKey string) (string, error) {
	st, err := d.jobs.LatestStatusByKey(ctx, logicalKey)
	return string(st), err
}

// FencedRuntimeConfig builds the generic jobs FencedRuntime configuration that runs
// authentication's delivery processor, registering rt.Handle under rt.Kind and rt.Discard
// as the per-kind dead-letter hook. The host passes the returned config to
// jobs.NewFencedRuntime; opts tune sizing/cadence.
//
// The handler maps auth's explicit retry/permanent verdict (AV3D-3.4) onto the generic
// runtime's policy: a permanent-classified handle error (auth.DeliveryErrorPermanent)
// becomes jobs.Permanent so the runtime dead-letters IMMEDIATELY and fires rt.Discard;
// any other error is a transient failure the runtime retries with bounded backoff. The
// execution ID rides into the claim for the best-effort lifecycle observation.
func FencedRuntimeConfig(rt auth.DeliveryJobRuntime, opts ...func(*jobs.FencedRuntimeConfig)) jobs.FencedRuntimeConfig {
	cfg := jobs.FencedRuntimeConfig{
		Handlers: map[string]jobs.FencedHandlerFunc{
			rt.Kind: func(ctx context.Context, claim jobs.FencedClaim) error {
				err := rt.Handle(ctx, auth.DeliveryClaim{
					ExecutionID: claim.ExecutionID,
					Payload:     []byte(claim.Payload),
					Attempt:     claim.Attempt,
					Checkpoint: func(ctx context.Context, sealed []byte) error {
						return claim.Checkpoint(ctx, json.RawMessage(sealed))
					},
				})
				if err == nil {
					return nil
				}
				if auth.DeliveryErrorPermanent(err) {
					return jobs.Permanent(err.Error())
				}
				return err
			},
		},
		DeadLetters: map[string]jobs.DeadLetterFunc{
			rt.Kind: func(ctx context.Context, j job.Job) error {
				return rt.Discard(ctx, j.JobID, []byte(j.Payload))
			},
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// Purger is the narrow slice of the generic jobs Service PurgeTerminal needs: the
// bounded terminal purge. *jobs.Service satisfies it.
type Purger interface {
	PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error)
}

// PurgeTerminal drives the generic jobs terminal-retention purge and emits the auth
// purged lifecycle observation (AV3D-3.4). It is the host-driven bounded cleanup: only
// terminal generations older than before are removed, up to limit, WITHOUT any
// auth-specific SQL — the retention policy is the caller's. The observed count is the
// number removed. A purge error is returned unchanged and no purged event is emitted.
func PurgeTerminal(ctx context.Context, purger Purger, rt auth.DeliveryJobRuntime, before time.Time, limit int) (int, error) {
	n, err := purger.PurgeTerminal(ctx, before, limit)
	if err != nil {
		return n, err
	}
	if rt.Purged != nil {
		rt.Purged(ctx, n)
	}
	return n, nil
}

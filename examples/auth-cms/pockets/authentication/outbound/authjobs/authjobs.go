// Package authjobs is the host-owned composition adapter (authv3-delivery-refactor
// AV3D-3.1) that runs authentication's encrypted outbound delivery on the generic
// jobs pocket. It is the ONE place that imports BOTH pockets/authentication and
// pockets/jobs; neither pocket core imports the other (constitution rule 6), and
// the composition happens here, in the host, over the two pockets' stdlib-typed
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
// runtime explicitly (the pockets start no goroutine).
package authjobs

import (
	"context"
	"encoding/json"
	"time"

	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// Enqueuer is the narrow slice of the generic jobs Service the Dispatcher needs,
// composed from the sdk keyed-work submission protocol: this bridge requires all
// three segregated capabilities — idempotent admission, atomic replace/supersede,
// and latest-by-key status. *queue.Service satisfies it.
type Enqueuer interface {
	work.Enqueuer
	work.Replacer
	work.StatusReader
}

// Compile-time proof the adapter satisfies authentication's stdlib-typed delivery
// transport seam without either pocket importing the other.
var _ delivery.Dispatcher = (*Dispatcher)(nil)

// Dispatcher bridges auth.DeliveryDispatcher to the generic jobs fenced primitives.
type Dispatcher struct {
	jobs Enqueuer
	kind string
}

// NewDispatcher builds the dispatcher over the generic jobs Service. Every delivery
// command is submitted under auth.DeliveryJobKind (the single kind the one delivery
// handler processes); the per-command rail/purpose ride inside the sealed payload.
func NewDispatcher(j Enqueuer) *Dispatcher {
	return &Dispatcher{jobs: j, kind: delivery.JobKind}
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
// holding logicalKey; the authentication pocket normalizes it into its stable status.
func (d *Dispatcher) LatestStatus(ctx context.Context, logicalKey string) (string, error) {
	st, err := d.jobs.LatestStatusByKey(ctx, logicalKey)
	return string(st), err
}

// NewRuntime connects authentication delivery to the generic fenced queue.
// The handler and discard hook share rt.Kind. Construction starts no workers;
// the host runs the returned runtime. Supplied options replace their whole group.
func NewRuntime(svc *job.Service, rt delivery.JobRuntime, opts ...job.FencedRuntimeOption) (*job.FencedRuntime, error) {
	options := []job.FencedRuntimeOption{job.WithDeadLetters(deadLetters(rt))}
	options = append(options, opts...)
	return job.NewFencedRuntime(svc, handlers(rt), options...)
}

// Permanent delivery errors become immediate dead letters; ordinary errors keep
// the generic runtime's bounded retry policy. Checkpoints keep the same lease fence.
func handlers(rt delivery.JobRuntime) map[string]job.FencedHandlerFunc {
	return map[string]job.FencedHandlerFunc{
		rt.Kind: func(ctx context.Context, claim job.FencedClaim) error {
			err := rt.Handle(ctx, delivery.Claim{
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
			if delivery.HandleErrorPermanent(err) {
				return job.Permanent(err.Error())
			}
			return err
		},
	}
}

func deadLetters(rt delivery.JobRuntime) map[string]job.DeadLetterFunc {
	return map[string]job.DeadLetterFunc{
		rt.Kind: func(ctx context.Context, j job.Job) error {
			return rt.Discard(ctx, j.JobID, []byte(j.Payload))
		},
	}
}

// Purger is the narrow slice of the generic jobs Service PurgeTerminal needs: the
// bounded terminal purge. *queue.Service satisfies it.
type Purger interface {
	PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error)
}

// PurgeTerminal drives the generic jobs terminal-retention purge and emits the auth
// purged lifecycle observation (AV3D-3.4). It is the host-driven bounded cleanup: only
// terminal generations older than before are removed, up to limit, WITHOUT any
// auth-specific SQL — the retention policy is the caller's. The observed count is the
// number removed. A purge error is returned unchanged and no purged event is emitted.
func PurgeTerminal(ctx context.Context, purger Purger, rt delivery.JobRuntime, before time.Time, limit int) (int, error) {
	n, err := purger.PurgeTerminal(ctx, before, limit)
	if err != nil {
		return n, err
	}
	if rt.Purged != nil {
		rt.Purged(ctx, n)
	}
	return n, nil
}

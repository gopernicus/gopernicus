package authentication

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// RevokeAllSessionsJobKind is the job kind a host registers
	// RevokeAllSessionsJobHandler under and enqueues a revoke-all run as.
	RevokeAllSessionsJobKind = "authentication.revoke_all_sessions"

	// globalRevocationJobTimeout bounds one revoke-all job run, so a hung
	// store cannot hold a worker indefinitely.
	globalRevocationJobTimeout = 10 * time.Minute
)

// ErrRevokeAllSessionsJobInvalid is returned for a revoke-all job payload that
// is malformed, carries unknown fields, or names no actor. It wraps
// sdk.ErrInvalidInput; a retry cannot fix it.
var ErrRevokeAllSessionsJobInvalid = fmt.Errorf("auth: invalid revoke-all-sessions job payload: %w", sdk.ErrInvalidInput)

// RevokeAllSessionsJob is the payload of a RevokeAllSessionsJobKind job, on
// the wire as {"actor_type": ..., "actor_id": ...}. The actor is the principal
// the run's evidence rows are attributed to: the administrator who triggered
// it, or a system principal such as {Type: "system", ID: "incident-cli"}.
type RevokeAllSessionsJob struct {
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
}

// NewRevokeAllSessionsJob encodes the payload for a RevokeAllSessionsJobKind
// job. An actor without a Type or ID → ErrRevokeAllSessionsJobInvalid, so every
// run is attributed.
func NewRevokeAllSessionsJob(actor Principal) ([]byte, error) {
	if actor.Type == "" || actor.ID == "" {
		return nil, ErrRevokeAllSessionsJobInvalid
	}
	return json.Marshal(RevokeAllSessionsJob{ActorType: actor.Type, ActorID: actor.ID})
}

// RevokeAllSessionsJobHandler returns the handler for RevokeAllSessionsJobKind:
// it decodes the payload (strictly — an unknown field or a missing actor →
// ErrRevokeAllSessionsJobInvalid, with no run and no evidence rows) and runs
// RevokeAllSessions under a ten-minute limit. The run's counts, and any partial
// counts on failure, are in its all_sessions_revoked_global evidence rows; the
// handler returns only the error. Nil Repositories.GlobalSessions →
// ErrGlobalRevocationUnsupported.
//
// The pocket imports no jobs implementation; a host adapts the handler to its
// queue, e.g. for pockets/jobs:
//
//	h := components.Authentication.RevokeAllSessionsJobHandler()
//	handlers[authlogic.RevokeAllSessionsJobKind] = func(ctx context.Context, j queue.Job) error {
//		return h(ctx, j.Payload)
//	}
//
// Enqueue it with one attempt (MaxAttempts: 1): a retry signs the fleet out
// again and writes another evidence pair, so a re-run is an operator's
// deliberate re-enqueue. The caller's own session ends with the rest.
//
// TRUSTED: like RevokeAllSessions it applies NO authorization — whoever can
// enqueue the job owns that decision.
func (s *Service) RevokeAllSessionsJobHandler() func(ctx context.Context, payload []byte) error {
	return func(ctx context.Context, payload []byte) error {
		var job RevokeAllSessionsJob
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&job); err != nil || dec.More() || job.ActorType == "" || job.ActorID == "" {
			return ErrRevokeAllSessionsJobInvalid
		}
		ctx, cancel := context.WithTimeout(ctx, globalRevocationJobTimeout)
		defer cancel()
		_, err := s.RevokeAllSessions(ctx, Principal{Type: job.ActorType, ID: job.ActorID})
		return err
	}
}

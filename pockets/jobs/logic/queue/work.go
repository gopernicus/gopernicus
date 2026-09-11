package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// Compile-time seams: the Service implements the canonical keyed-work submission
// protocol (sdk/capabilities/work) — the jobs pocket is its implementation of
// record. A consuming pocket depends on the sdk ports, never on this package
// (constitution rule 6). The protocol methods are backed by the lease-fenced queue
// (Repositories.FencedQueue).
var (
	_ work.Enqueuer     = (*Service)(nil)
	_ work.Replacer     = (*Service)(nil)
	_ work.StatusReader = (*Service)(nil)
)

// EnqueueOnceInput is the full-fidelity input for EnqueueOnceIn: the work
// protocol's vocabulary plus the pocket's optional TenantID slot. It is the
// struct-input sibling of the frozen positional EnqueueOnce, matching the
// EnqueueJob/EnsureSchedule convention so a later optional field costs no
// signature change.
type EnqueueOnceInput struct {
	Kind       string
	LogicalKey string
	Payload    []byte
	// TenantID is the OPTIONAL host-defined boundary to stamp on the execution
	// (see Job.TenantID). Empty = no tenant.
	TenantID string
}

// ReplaceInput is the full-fidelity input for ReplaceIn — EnqueueOnceInput's
// supersession counterpart.
type ReplaceInput struct {
	Kind       string
	LogicalKey string
	Payload    []byte
	// TenantID is the OPTIONAL host-defined boundary to stamp on the fresh
	// execution (see Job.TenantID). Empty = no tenant.
	TenantID string
}

// EnqueueOnceIn admits in.Payload under in.LogicalKey exactly once (idempotent
// while an active execution holds the key), returning the unique execution ID. It
// is EnqueueOnce with the pocket's tenant slot: the work protocol's vocabulary is
// unchanged, so a consuming pocket that depends on work.Enqueuer keeps using the
// positional form. Payload is opaque bytes the queue never interprets; the Service
// deep-copies it so a later caller mutation cannot alter the admitted work.
func (s *Service) EnqueueOnceIn(ctx context.Context, in EnqueueOnceInput) (string, error) {
	if s.fencedQueue == nil {
		return "", ErrFencedQueueRequired
	}
	if in.Kind == "" {
		return "", fmt.Errorf("jobs: kind is required: %w", sdk.ErrInvalidInput)
	}
	j, err := s.fencedQueue.EnqueueOnce(ctx, Enqueue{
		Kind:       in.Kind,
		TenantID:   in.TenantID,
		LogicalKey: in.LogicalKey,
		Payload:    json.RawMessage(bytes.Clone(in.Payload)),
	})
	if err != nil {
		return "", err
	}
	return j.JobID, nil
}

// ReplaceIn supersedes every active execution holding in.LogicalKey and inserts
// one fresh execution, returning its ID — the user-requested resend with the
// pocket's tenant slot. See EnqueueOnceIn for the payload-copy note.
func (s *Service) ReplaceIn(ctx context.Context, in ReplaceInput) (string, error) {
	if s.fencedQueue == nil {
		return "", ErrFencedQueueRequired
	}
	if in.Kind == "" {
		return "", fmt.Errorf("jobs: kind is required: %w", sdk.ErrInvalidInput)
	}
	j, err := s.fencedQueue.Replace(ctx, Enqueue{
		Kind:       in.Kind,
		TenantID:   in.TenantID,
		LogicalKey: in.LogicalKey,
		Payload:    json.RawMessage(bytes.Clone(in.Payload)),
	})
	if err != nil {
		return "", err
	}
	return j.JobID, nil
}

// EnqueueOnce admits payload under logicalKey exactly once (idempotent while an
// active execution holds the key), returning the unique execution ID. It is the
// producer half of the work.Enqueuer protocol, backed by the fenced queue; its
// signature is FROZEN by that protocol, so it delegates to EnqueueOnceIn with no
// tenant. A caller that needs the tenant slot calls EnqueueOnceIn.
func (s *Service) EnqueueOnce(ctx context.Context, kind, logicalKey string, payload []byte) (string, error) {
	if logicalKey == "" {
		return "", sdk.ErrInvalidInput
	}
	return s.EnqueueOnceIn(ctx, EnqueueOnceInput{Kind: kind, LogicalKey: logicalKey, Payload: payload})
}

// Replace supersedes every active execution holding logicalKey and inserts one
// fresh execution, returning its ID — the user-requested resend. It is the
// work.Replacer protocol; its signature is FROZEN by that protocol, so it
// delegates to ReplaceIn with no tenant.
func (s *Service) Replace(ctx context.Context, kind, logicalKey string, payload []byte) (string, error) {
	if logicalKey == "" {
		return "", sdk.ErrInvalidInput
	}
	return s.ReplaceIn(ctx, ReplaceInput{Kind: kind, LogicalKey: logicalKey, Payload: payload})
}

// LatestStatusByKey returns the lifecycle status of the most-recent execution
// holding logicalKey, or sdk.ErrNotFound when the key names no work. It is the
// work.StatusReader protocol: it reveals only the lifecycle status, never payload,
// destination, or secret, and never leases or mutates.
func (s *Service) LatestStatusByKey(ctx context.Context, logicalKey string) (work.Status, error) {
	if logicalKey == "" {
		return "", sdk.ErrInvalidInput
	}
	if s.fencedQueue == nil {
		return "", ErrFencedQueueRequired
	}
	j, err := s.fencedQueue.GetLatestByKey(ctx, logicalKey)
	if err != nil {
		return "", err
	}
	return j.JobStatus, nil
}

// Checkpoint atomically replaces the payload of the running execution while the
// caller still holds the current lease (executionID + leaseID), preserving job
// identity and status. A stale or superseded lease fails with sdk.ErrConflict. It
// is the executor-side checkpoint an opaque delivery records BEFORE its side effect
// so every retry resends the identical rendered bytes. Executor-side, out of the
// work protocol (D3): a consuming processor redeclares this method structurally.
func (s *Service) Checkpoint(ctx context.Context, executionID, leaseID string, payload json.RawMessage) error {
	if s.fencedQueue == nil {
		return ErrFencedQueueRequired
	}
	return s.fencedQueue.Checkpoint(ctx, executionID, leaseID, payload, time.Now().UTC())
}

// PurgeTerminal deletes up to limit terminal jobs whose TerminalAt is at or before
// before, returning the number removed. It is the bounded-retention seam a host (or a
// composition adapter) drives to bound terminal-status retention WITHOUT any
// consumer-specific SQL: the retention window (before) and batch size (limit) are the
// caller's policy, and only terminal generations are ever removed (AV3D-3.4). A
// positive limit bounds the batch; zero removes nothing and a negative limit
// requests unbounded cleanup. A non-fenced Service returns ErrFencedQueueRequired.
func (s *Service) PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error) {
	if s.fencedQueue == nil {
		return 0, ErrFencedQueueRequired
	}
	return s.fencedQueue.PurgeTerminal(ctx, before, limit)
}

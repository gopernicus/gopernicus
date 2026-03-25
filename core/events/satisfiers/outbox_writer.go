package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/repositories/events/eventoutbox"
	"github.com/gopernicus/gopernicus/infrastructure/events/outbox"
)

var _ outbox.OutboxWriter = (*OutboxWriterSatisfier)(nil)

type eventOutboxRepo interface {
	Create(ctx context.Context, input eventoutbox.CreateEventOutbox) (eventoutbox.EventOutbox, error)
}

// OutboxWriterSatisfier satisfies outbox.OutboxWriter using the generated eventoutbox repository.
type OutboxWriterSatisfier struct {
	repo eventOutboxRepo
}

func NewOutboxWriterSatisfier(repo eventOutboxRepo) *OutboxWriterSatisfier {
	return &OutboxWriterSatisfier{repo: repo}
}

func (s *OutboxWriterSatisfier) Create(ctx context.Context, input outbox.CreateOutboxEvent) (outbox.OutboxEvent, error) {
	scheduledFor := time.Now().UTC()
	if input.ScheduledFor != nil {
		scheduledFor = *input.ScheduledFor
	}

	record, err := s.repo.Create(ctx, eventoutbox.CreateEventOutbox{
		EventID:       input.EventID,
		EventType:     input.EventType,
		CorrelationID: input.CorrelationID,
		TenantID:      input.TenantID,
		AggregateType: input.AggregateType,
		AggregateID:   input.AggregateID,
		OccurredAt:    input.OccurredAt,
		Payload:       input.Payload,
		Priority:      input.Priority,
		MaxRetries:    input.MaxRetries,
		ScheduledFor:  scheduledFor,
	})
	if err != nil {
		return outbox.OutboxEvent{}, err
	}

	return outbox.OutboxEvent{
		EventID:   record.EventID,
		EventType: record.EventType,
		Status:    record.Status,
	}, nil
}

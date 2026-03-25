package satisfiers

import (
	"context"
	"encoding/json"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/securityevents"
)

var _ authentication.SecurityEventRepository = (*SecurityEventSatisfier)(nil)

type securityEventRepo interface {
	Create(ctx context.Context, input securityevents.CreateSecurityEvent) (securityevents.SecurityEvent, error)
}

// SecurityEventSatisfier satisfies authentication.SecurityEventRepository
// using the generated security_events repository.
type SecurityEventSatisfier struct {
	repo securityEventRepo
}

func NewSecurityEventSatisfier(repo securityEventRepo) *SecurityEventSatisfier {
	return &SecurityEventSatisfier{repo: repo}
}

func (s *SecurityEventSatisfier) Create(ctx context.Context, event authentication.SecurityEvent) error {
	var details *json.RawMessage
	if event.Details != nil {
		data, err := json.Marshal(event.Details)
		if err != nil {
			return err
		}
		raw := json.RawMessage(data)
		details = &raw
	}
	_, err := s.repo.Create(ctx, securityevents.CreateSecurityEvent{
		UserID:       strPtr(event.UserID),
		EventType:    event.EventType,
		EventStatus:  event.EventStatus,
		EventDetails: details,
		IpAddress:    strPtr(event.IPAddress),
		UserAgent:    strPtr(event.UserAgent),
	})
	return err
}

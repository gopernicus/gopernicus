package authentication

import "context"

// logSecurityEvent records a security event if security event logging
// is configured. Errors never propagate — they are logged as warnings
// so that auth flows are never disrupted by audit logging failures.
func (a *Authenticator) logSecurityEvent(ctx context.Context, userID, eventType, eventStatus string, details map[string]any) {
	if a.securityEvents == nil {
		return
	}

	ci := clientInfoFromContext(ctx)

	if err := a.securityEvents.Create(ctx, SecurityEvent{
		UserID:      userID,
		EventType:   eventType,
		EventStatus: eventStatus,
		IPAddress:   ci.IPAddress,
		UserAgent:   ci.UserAgent,
		Details:     details,
	}); err != nil {
		a.log.WarnContext(ctx, "failed to log security event",
			"event_type", eventType,
			"event_status", eventStatus,
			"user_id", userID,
			"error", err,
		)
	}
}

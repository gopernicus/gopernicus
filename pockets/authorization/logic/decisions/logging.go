package decisions

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// Logging lives outside retry and snapshot callbacks. A caller-owned bound view
// reports only evaluation completion, never the surrounding transaction's commit.
// The decision observer sees unbound operations only, regardless of log level.
type decisionLog struct {
	logger   *slog.Logger
	observer DecisionObserver
	started  time.Time
	bound    bool
}

func (s *Service) startDecisionLog(ctx context.Context, bound bool) decisionLog {
	if s == nil {
		return decisionLog{}
	}
	l := decisionLog{bound: bound || s.inSnapshot}
	if s.logger != nil && s.logger.Enabled(ctx, slog.LevelDebug) {
		l.logger = s.logger
	}
	if !l.bound {
		l.observer = s.observer
	}
	if !l.active() {
		return decisionLog{}
	}
	l.started = time.Now()
	return l
}

func (l decisionLog) active() bool { return l.logger != nil || l.observer != nil }

func (l decisionLog) check(ctx context.Context, operation string, req authmodel.CheckRequest, result authmodel.CheckResult, err error) {
	if !l.active() {
		return
	}
	outcome := OutcomeDenied
	if result.Allowed {
		outcome = OutcomeAllowed
	}
	event := decisionRequestEvent(req.Principal, req.Permission, req.Resource.Type)
	event.ResourceID = logRef(req.Resource.ID)
	var attrs []slog.Attr
	if l.logger != nil {
		attrs = decisionRequestAttrs(req.Principal, req.Permission, req.Resource.Type)
		if req.Resource.ID != "" {
			attrs = append(attrs, slog.String("resource_id", logRef(req.Resource.ID)))
		}
	}
	l.finish(ctx, operation, outcome, err, event, attrs)
}

func (l decisionLog) batch(ctx context.Context, operation string, requests int, results []authmodel.CheckResult, err error) {
	if !l.active() {
		return
	}
	allowed := 0
	for _, result := range results {
		if result.Allowed {
			allowed++
		}
	}
	event := DecisionEvent{Requests: requests, Allowed: allowed, Denied: len(results) - allowed}
	var attrs []slog.Attr
	if l.logger != nil {
		attrs = []slog.Attr{slog.Int("request_count", requests), slog.Int("result_count", len(results)), slog.Int("allowed_count", allowed), slog.Int("denied_count", len(results)-allowed)}
	}
	l.finish(ctx, operation, OutcomeComplete, err, event, attrs)
}

func (l decisionLog) filter(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string, requests, results int, err error) {
	if !l.active() {
		return
	}
	event := decisionRequestEvent(principal, permission, resourceType)
	event.Requests, event.Allowed = requests, results
	if err == nil {
		event.Denied = requests - results
	}
	var attrs []slog.Attr
	if l.logger != nil {
		attrs = decisionRequestAttrs(principal, permission, resourceType)
		attrs = append(attrs, slog.Int("request_count", requests), slog.Int("result_count", results))
	}
	l.finish(ctx, "FilterAuthorized", OutcomeComplete, err, event, attrs)
}

func (l decisionLog) lookup(ctx context.Context, operation string, principal authmodel.PrincipalRef, permission, resourceType string, limit int, result authmodel.LookupResult, err error) {
	if !l.active() {
		return
	}
	event := decisionRequestEvent(principal, permission, resourceType)
	event.Allowed = len(result.IDs)
	var attrs []slog.Attr
	if l.logger != nil {
		attrs = decisionRequestAttrs(principal, permission, resourceType)
		attrs = append(attrs, slog.Int("page_limit", limit), slog.Int("result_count", len(result.IDs)), slog.Bool("unrestricted", result.Unrestricted), slog.Bool("has_more", result.HasMore))
	}
	l.finish(ctx, operation, OutcomeComplete, err, event, attrs)
}

func (l decisionLog) page(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string, limit, count int, hasMore, scanLimit bool, err error) {
	if !l.active() {
		return
	}
	event := decisionRequestEvent(principal, permission, resourceType)
	event.Allowed = count
	var attrs []slog.Attr
	if l.logger != nil {
		attrs = decisionRequestAttrs(principal, permission, resourceType)
		attrs = append(attrs, slog.Int("page_limit", limit), slog.Int("result_count", count), slog.Bool("has_more", hasMore), slog.Bool("scan_limit_reached", scanLimit))
	}
	l.finish(ctx, "FilterPage", OutcomeComplete, err, event, attrs)
}

func (l decisionLog) finish(ctx context.Context, operation string, outcome DecisionOutcome, err error, event DecisionEvent, attrs []slog.Attr) {
	duration := time.Since(l.started)
	if err != nil {
		outcome = OutcomeError
		event.ErrorKind = decisionErrorKind(err)
	}
	if l.logger != nil {
		if err != nil {
			attrs = append(attrs, slog.String("error_kind", event.ErrorKind))
		}
		attrs = append(attrs, slog.String("operation", operation), slog.String("outcome", string(outcome)), slog.Duration("duration", duration), slog.Bool("bound", l.bound))
		l.logger.LogAttrs(ctx, slog.LevelDebug, "authorization decision", attrs...)
	}
	if l.observer != nil {
		event.Operation, event.Outcome, event.Duration = operation, outcome, duration
		l.observer(ctx, event)
	}
}

func decisionRequestEvent(principal authmodel.PrincipalRef, permission, resourceType string) DecisionEvent {
	return DecisionEvent{Principal: sdk.Principal{Type: logRef(principal.Type), ID: logRef(principal.ID)}, Permission: logRef(permission), ResourceType: logRef(resourceType)}
}

func decisionRequestAttrs(principal authmodel.PrincipalRef, permission, resourceType string) []slog.Attr {
	attrs := []slog.Attr{slog.String("principal_type", logRef(principal.Type)), slog.String("principal_id", logRef(principal.ID))}
	if permission != "" {
		attrs = append(attrs, slog.String("permission", logRef(permission)))
	}
	if resourceType != "" {
		attrs = append(attrs, slog.String("resource_type", logRef(resourceType)))
	}
	return attrs
}

// Invalid input is logged too, so reference bounds must not depend on validation.
func logRef(value string) string {
	if len(value) > tuples.MaxRefFieldLen {
		value = value[:tuples.MaxRefFieldLen]
	}
	return strings.ToValidUTF8(value, "?")
}

func decisionErrorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, authmodel.ErrEvaluationLimit):
		return "evaluation_limit"
	case errors.Is(err, authmodel.ErrEnumerationContended):
		return "enumeration_contended"
	case errors.Is(err, sdk.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, sdk.ErrUnavailable):
		return "unavailable"
	default:
		return "other"
	}
}

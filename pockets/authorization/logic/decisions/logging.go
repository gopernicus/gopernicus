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
type decisionLog struct {
	logger  *slog.Logger
	started time.Time
	bound   bool
}

func (s *Service) startDecisionLog(ctx context.Context, bound bool) decisionLog {
	if s == nil || s.logger == nil || !s.logger.Enabled(ctx, slog.LevelDebug) {
		return decisionLog{}
	}
	return decisionLog{logger: s.logger, started: time.Now(), bound: bound || s.inSnapshot}
}

func (l decisionLog) check(ctx context.Context, operation string, req authmodel.CheckRequest, result authmodel.CheckResult, err error) {
	if l.logger == nil {
		return
	}
	outcome := "denied"
	if result.Allowed {
		outcome = "allowed"
	}
	attrs := decisionRequestAttrs(req.Principal, req.Permission, req.Resource.Type)
	if req.Resource.ID != "" {
		attrs = append(attrs, slog.String("resource_id", logRef(req.Resource.ID)))
	}
	l.finish(ctx, operation, outcome, err, attrs)
}

func (l decisionLog) batch(ctx context.Context, operation string, requests int, results []authmodel.CheckResult, err error) {
	if l.logger == nil {
		return
	}
	allowed := 0
	for _, result := range results {
		if result.Allowed {
			allowed++
		}
	}
	l.finish(ctx, operation, "complete", err, []slog.Attr{slog.Int("request_count", requests), slog.Int("result_count", len(results)), slog.Int("allowed_count", allowed), slog.Int("denied_count", len(results)-allowed)})
}

func (l decisionLog) filter(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string, requests, results int, err error) {
	if l.logger == nil {
		return
	}
	attrs := decisionRequestAttrs(principal, permission, resourceType)
	attrs = append(attrs, slog.Int("request_count", requests), slog.Int("result_count", results))
	l.finish(ctx, "FilterAuthorized", "complete", err, attrs)
}

func (l decisionLog) lookup(ctx context.Context, operation string, principal authmodel.PrincipalRef, permission, resourceType string, limit int, result authmodel.LookupResult, err error) {
	if l.logger == nil {
		return
	}
	attrs := decisionRequestAttrs(principal, permission, resourceType)
	attrs = append(attrs, slog.Int("page_limit", limit), slog.Int("result_count", len(result.IDs)), slog.Bool("unrestricted", result.Unrestricted), slog.Bool("has_more", result.HasMore))
	l.finish(ctx, operation, "complete", err, attrs)
}

func (l decisionLog) page(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string, limit, count int, hasMore, scanLimit bool, err error) {
	if l.logger == nil {
		return
	}
	attrs := decisionRequestAttrs(principal, permission, resourceType)
	attrs = append(attrs, slog.Int("page_limit", limit), slog.Int("result_count", count), slog.Bool("has_more", hasMore), slog.Bool("scan_limit_reached", scanLimit))
	l.finish(ctx, "FilterPage", "complete", err, attrs)
}

func (l decisionLog) finish(ctx context.Context, operation, outcome string, err error, attrs []slog.Attr) {
	if err != nil {
		outcome = "error"
		attrs = append(attrs, slog.String("error_kind", decisionErrorKind(err)))
	}
	attrs = append(attrs, slog.String("operation", operation), slog.String("outcome", outcome), slog.Duration("duration", time.Since(l.started)), slog.Bool("bound", l.bound))
	l.logger.LogAttrs(ctx, slog.LevelDebug, "authorization decision", attrs...)
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

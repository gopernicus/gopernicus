package goredis

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/capabilities/tracing"
)

var (
	_ redis.Hook = (*loggingHook)(nil)
	_ redis.Hook = (*tracingHook)(nil)
)

// loggingHook logs command errors always and, when slowThreshold is positive,
// commands slower than it. Instrumentation only: it never alters the reply or
// the error it observes.
type loggingHook struct {
	log           *slog.Logger
	slowThreshold time.Duration
}

// LoggingOption configures a LoggingHook.
type LoggingOption func(*loggingHook)

// WithSlowThreshold makes the hook log any command slower than d at Warn level.
// A zero or negative threshold (the default) disables slow-command logging;
// command errors are logged regardless of it.
func WithSlowThreshold(d time.Duration) LoggingOption {
	return func(h *loggingHook) { h.slowThreshold = d }
}

// LoggingHook returns a redis.Hook that logs command errors always (the
// redis.Nil cache-miss sentinel is not an error) and, when a slow threshold is
// configured via WithSlowThreshold, commands slower than it. Install it with
// WithLogging or hand it to rdb.AddHook directly. A nil logger falls back to
// slog.Default(). It returns the redis.Hook interface go-redis's AddHook
// consumes.
func LoggingHook(log *slog.Logger, opts ...LoggingOption) redis.Hook {
	if log == nil {
		log = slog.Default()
	}
	h := &loggingHook{log: log}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// DialHook passes through: connection dialing is not logged.
func (h *loggingHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *loggingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		h.observe(ctx, cmd.Name(), start, err)
		return err
	}
}

func (h *loggingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		h.observe(ctx, "pipeline("+strconv.Itoa(len(cmds))+")", start, err)
		return err
	}
}

// observe emits the error log (always, excluding the redis.Nil cache-miss
// sentinel) or the slow-command log when a positive threshold is set.
func (h *loggingHook) observe(ctx context.Context, command string, start time.Time, err error) {
	elapsed := time.Since(start)
	if err != nil && !errors.Is(err, redis.Nil) {
		h.log.LogAttrs(ctx, slog.LevelError, "redis command failed",
			slog.String("command", command),
			slog.Duration("elapsed", elapsed),
			slog.String("error", err.Error()),
		)
		return
	}
	if h.slowThreshold > 0 && elapsed >= h.slowThreshold {
		h.log.LogAttrs(ctx, slog.LevelWarn, "redis slow command",
			slog.String("command", command),
			slog.Duration("elapsed", elapsed),
		)
	}
}

// tracingHook spans each command against the sdk/capabilities/tracing port. It records only
// the command name — never argument values — so span metadata never leaks the
// keys or values a command carries.
type tracingHook struct {
	tracer tracing.Tracer
}

// TracingHook returns a redis.Hook that runs each command (and pipeline) inside
// a span started from tracer, the sdk/capabilities/tracing port (stdlib-only; an OpenTelemetry
// exporter is the deferred tracing integration). A nil tracer yields a hook
// backed by tracing.Noop, so installation is always safe. It returns the
// redis.Hook interface go-redis's AddHook consumes.
func TracingHook(tracer tracing.Tracer) redis.Hook {
	if tracer == nil {
		tracer = tracing.Noop{}
	}
	return &tracingHook{tracer: tracer}
}

// DialHook passes through: connection dialing is not traced.
func (h *tracingHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *tracingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		ctx, span := h.tracer.StartSpan(ctx, "redis."+cmd.Name())
		defer span.Finish()
		span.SetAttributes(
			tracing.StringAttribute("db.system", "redis"),
			tracing.StringAttribute("db.operation", cmd.Name()),
		)
		err := next(ctx, cmd)
		if err != nil && !errors.Is(err, redis.Nil) {
			span.RecordError(err)
		}
		return err
	}
}

func (h *tracingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		ctx, span := h.tracer.StartSpan(ctx, "redis.pipeline")
		defer span.Finish()
		span.SetAttributes(
			tracing.StringAttribute("db.system", "redis"),
			tracing.StringAttribute("db.operation", "pipeline"),
			tracing.StringAttribute("db.redis.pipeline_length", strconv.Itoa(len(cmds))),
		)
		err := next(ctx, cmds)
		if err != nil {
			span.RecordError(err)
		}
		return err
	}
}

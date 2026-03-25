package goredisdb

import (
	"context"
	"net"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/telemetry"
)

// OTelHook implements redis.Hook for OpenTelemetry tracing.
// Each Redis command becomes a child span of the current trace context.
type OTelHook struct {
	tracer telemetry.Tracer
}

// NewOTelHook creates a new OTel tracing hook for Redis.
func NewOTelHook(tracer telemetry.Tracer) *OTelHook {
	return &OTelHook{tracer: tracer}
}

// DialHook is called when a new connection is dialed.
func (h *OTelHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

// ProcessHook is called for single commands (GET, SET, etc.)
func (h *OTelHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.tracer == nil {
			return next(ctx, cmd)
		}

		operation := strings.ToUpper(cmd.Name())
		ctx, span := telemetry.StartClientSpan(ctx, h.tracer, "redis."+operation)
		defer span.End()

		telemetry.AddAttribute(span, "db.system", "redis")
		telemetry.AddAttribute(span, "db.operation", operation)
		telemetry.AddAttribute(span, "db.statement", formatCmd(cmd))

		err := next(ctx, cmd)
		if err != nil && err != redis.Nil {
			telemetry.RecordError(span, err)
		}

		// Track cache hit/miss for read operations
		if isCacheReadOp(operation) {
			if err == redis.Nil {
				telemetry.AddBoolAttribute(span, "cache.hit", false)
			} else if err == nil {
				telemetry.AddBoolAttribute(span, "cache.hit", true)
			}
			// On other errors, don't set cache.hit - it's an error, not a miss
		}

		return err
	}
}

// isCacheReadOp returns true for Redis commands that return redis.Nil on cache miss.
// Note: EXISTS returns 0/1, not redis.Nil, so it's excluded.
func isCacheReadOp(operation string) bool {
	switch operation {
	case "GET", "MGET", "HGET", "HGETALL", "HMGET":
		return true
	default:
		return false
	}
}

// ProcessPipelineHook is called for pipeline commands.
func (h *OTelHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if h.tracer == nil {
			return next(ctx, cmds)
		}

		ctx, span := telemetry.StartClientSpan(ctx, h.tracer, "redis.PIPELINE")
		defer span.End()

		telemetry.AddAttribute(span, "db.system", "redis")
		telemetry.AddAttribute(span, "db.operation", "PIPELINE")
		telemetry.AddIntAttribute(span, "db.redis.pipeline_length", len(cmds))

		err := next(ctx, cmds)
		if err != nil {
			telemetry.RecordError(span, err)
		}

		return err
	}
}

// formatCmd formats a Redis command for the db.statement attribute.
// Only includes the command name and key (if present), not values (to avoid PII).
func formatCmd(cmd redis.Cmder) string {
	args := cmd.Args()
	if len(args) == 0 {
		return ""
	}

	name := strings.ToUpper(cmd.Name())

	// For most commands, the second arg is the key
	if len(args) >= 2 {
		if key, ok := args[1].(string); ok {
			return name + " " + key
		}
	}

	return name
}

// WithTracer is an Option that adds OpenTelemetry tracing to the Redis client.
func WithTracer(tracer telemetry.Tracer) Option {
	return func(o *options) {
		o.hooks = append(o.hooks, NewOTelHook(tracer))
	}
}

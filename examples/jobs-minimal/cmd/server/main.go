// Command server is the zero-infra jobs proof host (design §8): it wires
// features/jobs to the in-core memstore (no datastore driver in its module
// graph), registers a handful of demo handlers plus an interval and a cron
// schedule, and runs the jobs Runtime in-process next to an HTTP server whose
// only route is a host-owned POST /enqueue (v1 claims no feature routes).
//
// It proves the whole jobs surface with no external infrastructure: the
// enqueue->wake latency coupling (a fresh job runs sub-second, not at the next
// poll), the retry->dead-letter path, deterministic schedule fires with sched_
// job IDs, and graceful drain — a SIGTERM lets an in-flight handler finish
// before Run returns.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs"
	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/features/jobs/memstore"
	robfigcron "github.com/gopernicus/gopernicus/integrations/scheduling/robfig-cron"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/logging"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

func main() {
	_ = environment.LoadEnv()

	log := logging.New(logging.Options{
		Level:  environment.GetEnvOrDefault("LOG_LEVEL", "INFO"),
		Format: environment.GetEnvOrDefault("LOG_FORMAT", "text"),
		Output: environment.GetEnvOrDefault("LOG_OUTPUT", "STDERR"),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.ErrorContext(ctx, "server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	// Stores: the in-core memstore with its DEFAULT lease (15m). No driver, no
	// migrations, no datastore module — zero external infrastructure.
	queue := memstore.NewQueue()
	schedules := memstore.NewSchedules()
	repos := jobs.Repositories{Queue: queue, Schedules: schedules}

	cfg := jobs.Config{
		Handlers: map[string]jobs.HandlerFunc{
			"demo.print":  printHandler(log),
			"demo.flaky":  flakyHandler(log),
			"demo.doomed": doomedHandler(log),
			"demo.slow":   slowHandler(log),
		},
		// robfig-cron is a CPU-only library (the bcrypt zero-infra precedent); its
		// *Parser satisfies jobs.CronParser directly now that CronSchedule is a type
		// alias — no composition-root adapter.
		Cron: robfigcron.New(),
		// Short cadence so the demo is observable: the queue pool still runs a fresh
		// enqueue sub-second via the wake channel; these bound the SCHEDULER pool's
		// idle poll (it has no wake channel) so the interval/cron fire promptly.
		PollInterval: 1 * time.Second,
		IdleInterval: 2 * time.Second,
		MaxAttempts:  3,
		// Route the runtime pools' operational lines ("processing job"/"job
		// completed"/…) through the host logger, same format and stream as the
		// handler logs — instead of the slog.Default() fallback.
		Logger: log,
	}

	svc, err := jobs.NewService(repos, cfg)
	if err != nil {
		return err
	}

	// One stdlib-path interval schedule and one robfig-path cron schedule. Both
	// fire demo.print with a deterministic sched_<id>_<slot> job ID.
	if _, err := svc.EnsureSchedule(ctx, schedule.Ensure{
		Name:    "heartbeat-15s",
		Kind:    "demo.print",
		Spec:    schedule.Spec{Every: 15 * time.Second},
		Payload: json.RawMessage(`{"source":"heartbeat-15s"}`),
	}); err != nil {
		return err
	}
	if _, err := svc.EnsureSchedule(ctx, schedule.Ensure{
		Name:    "minute-cron",
		Kind:    "demo.print",
		Spec:    schedule.Spec{Cron: "* * * * *"},
		Payload: json.RawMessage(`{"source":"minute-cron"}`),
	}); err != nil {
		return err
	}

	rt, err := jobs.NewRuntime(svc)
	if err != nil {
		return err
	}

	// Host-owned router. The only route is the host's own POST /enqueue — jobs v1
	// registers no feature routes.
	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))
	router.Handle(http.MethodPost, "/enqueue", enqueueHandler(svc, log))
	// Host-local liveness probe (host route, not feature surface). Mounted on
	// the root router with no middleware — unauthenticated by design, since a
	// readiness probe can't log in.
	router.Handle(http.MethodGet, "/healthz", healthzHandler())

	// Register mounts the built Service and logs; it starts nothing — the host
	// owns the run loop.
	mount := feature.Mount{Router: router, Logger: log}
	if err := svc.Register(mount); err != nil {
		return err
	}

	// In-process topology (design §7.4): the Runtime runs next to the HTTP server,
	// sharing one process and one cancellation. On ctx-cancel both drain — the HTTP
	// server stops accepting, the pools stop claiming, in-flight handlers finish
	// and persist Complete/Fail — then we exit 0.
	rtDone := make(chan error, 1)
	go func() { rtDone <- rt.Run(ctx) }()

	log.InfoContext(ctx, "jobs proof host started", "enqueue", "POST /enqueue")
	srvErr := web.Run(ctx, router, serverConfig(), log)

	// web.Run returned because ctx was cancelled and the HTTP server drained. Wait
	// for the jobs Runtime to drain too, so an in-flight slow handler finishes
	// before the process exits.
	log.InfoContext(context.Background(), "waiting for jobs runtime to drain")
	rtErr := <-rtDone
	log.InfoContext(context.Background(), "jobs runtime drained")
	return errors.Join(srvErr, rtErr)
}

// printHandler logs the job's payload. It backs both schedules, so its log line
// carries the deterministic sched_ job ID when a schedule fires.
func printHandler(log *slog.Logger) jobs.HandlerFunc {
	return func(ctx context.Context, j job.Job) error {
		log.InfoContext(ctx, "demo.print", "job_id", j.ID(), "payload", string(j.Payload))
		return nil
	}
}

// flakyHandler fails until the job has been retried at least twice, proving the
// retry path: two failures (RetryCount 0, then 1) then a completion (RetryCount
// 2). With MaxAttempts 3 it always reaches completion before dead-letter.
func flakyHandler(log *slog.Logger) jobs.HandlerFunc {
	return func(ctx context.Context, j job.Job) error {
		if j.RetryCount() < 2 {
			log.InfoContext(ctx, "demo.flaky failing", "job_id", j.ID(), "retry_count", j.RetryCount())
			return errors.New("demo.flaky: transient failure")
		}
		log.InfoContext(ctx, "demo.flaky succeeded", "job_id", j.ID(), "retry_count", j.RetryCount())
		return nil
	}
}

// doomedHandler always fails, so the job exhausts MaxAttempts (3) and reaches
// dead_letter observably after three "job failed" log lines.
func doomedHandler(log *slog.Logger) jobs.HandlerFunc {
	return func(ctx context.Context, j job.Job) error {
		log.InfoContext(ctx, "demo.doomed failing", "job_id", j.ID(), "retry_count", j.RetryCount())
		return errors.New("demo.doomed: permanent failure")
	}
}

// slowHandler sleeps ~5s ignoring ctx, so a SIGTERM mid-flight lets the drain
// prove that in-flight handlers finish before Run returns.
func slowHandler(log *slog.Logger) jobs.HandlerFunc {
	return func(ctx context.Context, j job.Job) error {
		log.InfoContext(ctx, "demo.slow started", "job_id", j.ID())
		time.Sleep(5 * time.Second)
		log.InfoContext(ctx, "demo.slow finished", "job_id", j.ID())
		return nil
	}
}

// enqueueRequest is the host-owned POST /enqueue body. kind + payload are the
// primitive-typed pair jobs.Service.Enqueue takes; the optional id/priority/
// max_attempts fields route to EnqueueJob for full-fidelity enqueues (id is the
// idempotency key).
type enqueueRequest struct {
	Kind        string          `json:"kind"`
	Payload     json.RawMessage `json:"payload"`
	ID          string          `json:"id,omitempty"`
	Priority    int             `json:"priority,omitempty"`
	MaxAttempts int             `json:"max_attempts,omitempty"`
}

// enqueueHandler is the host's own enqueue route (deliberately not a feature
// route — jobs v1 claims none). It calls svc.Enqueue for the primitive-typed
// path, or svc.EnqueueJob when any full-fidelity field is present.
func enqueueHandler(svc *jobs.Service, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req enqueueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.Kind == "" {
			http.Error(w, "kind is required", http.StatusBadRequest)
			return
		}
		payload := req.Payload
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}

		var jobID string
		if req.ID != "" || req.Priority != 0 || req.MaxAttempts != 0 {
			j, err := svc.EnqueueJob(r.Context(), job.Enqueue{
				ID:          req.ID,
				Kind:        req.Kind,
				Payload:     payload,
				Priority:    req.Priority,
				MaxAttempts: req.MaxAttempts,
			})
			if err != nil {
				log.ErrorContext(r.Context(), "enqueue failed", "kind", req.Kind, "error", err)
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			jobID = j.ID()
		} else {
			id, err := svc.Enqueue(r.Context(), req.Kind, payload)
			if err != nil {
				log.ErrorContext(r.Context(), "enqueue failed", "kind", req.Kind, "error", err)
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			jobID = id
		}

		log.InfoContext(r.Context(), "enqueued", "kind", req.Kind, "job_id", jobID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"job_id": jobID})
	}
}

// healthzHandler is this host's liveness probe. This host is memory-backed, so
// there is no DB to probe — reaching the handler is itself the liveness signal.
func healthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}

func serverConfig() web.ServerConfig {
	return web.ServerConfig{
		Host:            environment.GetEnvOrDefault("HOST", "localhost"),
		Port:            environment.GetEnvOrDefault("PORT", "8083"),
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}

package main

import (
	"context"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
)

type deliveryRuntimeTestConfig struct {
	job.FencedRuntimePolicy
	DeadLetters map[string]job.DeadLetterFunc
}

// Existing restart/failure matrices stage policy changes before construction.
// Only this test fixture mutates a policy; public options replace whole groups.
func runtimeFromDeliveryConfig(svc *job.Service, runtime delivery.JobRuntime, tune ...func(*deliveryRuntimeTestConfig)) (*job.FencedRuntime, error) {
	cfg := deliveryRuntimeTestConfig{DeadLetters: map[string]job.DeadLetterFunc{runtime.Kind: func(ctx context.Context, j job.Job) error { return runtime.Discard(ctx, j.JobID, []byte(j.Payload)) }}}
	for _, apply := range tune {
		apply(&cfg)
	}
	return authjobs.NewRuntime(svc, runtime, job.WithFencedRuntimePolicy(cfg.FencedRuntimePolicy), job.WithDeadLetters(cfg.DeadLetters), job.WithFencedRuntimeLogger(quietLog()))
}

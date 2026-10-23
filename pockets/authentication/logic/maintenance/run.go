package maintenance

import (
	"context"
	"math/rand/v2"
	"time"
)

// Run is the zero-dependency runner: it blocks, running a first pass after a
// random start jitter of up to a tenth of the interval — so a host deployed
// more often than the interval still purges — and then one pass every
// interval (WithInterval, default one hour). A pass error is logged and the
// loop continues; a panicking step is recovered by Pass. Run returns nil when
// ctx ends.
//
// Run one runner per database: concurrent passes are safe, not cheap.
func (s *Service) Run(ctx context.Context) error {
	timer := time.NewTimer(startJitter(s.interval))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		if _, err := s.Pass(ctx); err != nil && ctx.Err() == nil {
			s.log.ErrorContext(ctx, "maintenance_pass_failed", "error_kind", errorKind(err))
		}
		timer.Reset(s.interval)
	}
}

// startJitter is a random delay in [0, interval/10].
func startJitter(interval time.Duration) time.Duration {
	span := int64(interval / 10)
	if span <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(span + 1))
}

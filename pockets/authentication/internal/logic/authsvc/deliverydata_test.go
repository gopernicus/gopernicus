package authsvc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/delivery"
)

// TestForgotPasswordHookErrorFollowsWorkerRetryPolicy pins the worker-side
// DataHook error contract for an opaque start: ForgotPassword has already accepted
// and queued the command, so a hook failure inside the worker-side render never
// reaches the caller — it follows the bounded initializer retry then dead-letters,
// and no mail is sent. The hook never sees a Secret.
func TestForgotPasswordHookErrorFollowsWorkerRetryPolicy(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	h.svc.passwordResetURL = "https://app.example.com/reset-password"
	h.mailer.sent = nil

	var calls atomic.Int32
	hook := func(_ context.Context, purpose string, data map[string]any) (map[string]any, error) {
		if purpose != delivery.PurposePasswordReset {
			return nil, nil
		}
		if _, ok := data["Secret"]; ok {
			t.Errorf("hook received Secret")
		}
		calls.Add(1)
		return nil, errors.New("name lookup down")
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	router, err := delivery.NewRouter(delivery.Deps{Mailer: h.mailer, MailFrom: "noreply@example.com", DataHook: hook, Logger: quiet})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	disp := newMemDispatcher()
	enc := fakeEncrypter{}
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Dispatcher: disp, Encrypter: enc})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	proc, err := delivery.NewJobsProcessor(delivery.JobsProcessorDeps{Encrypter: enc, Router: router, Initializer: h.svc})
	if err != nil {
		t.Fatalf("delivery.NewJobsProcessor: %v", err)
	}
	h.svc.deliver = router
	h.svc.queue = &drainingQueue{svc: dsvc, disp: disp, proc: proc, t: t}

	if err := h.svc.ForgotPassword(context.Background(), "known@example.com"); err != nil {
		t.Fatalf("ForgotPassword surfaced a worker-side hook error: %v", err)
	}
	items := disp.snapshot()
	if len(items) != 1 {
		t.Fatalf("submitted %d commands, want 1", len(items))
	}
	if items[0].state != genDeadLetter {
		t.Errorf("command state = %q, want %q after the bounded retry", items[0].state, genDeadLetter)
	}
	if got := calls.Load(); got < 2 || int(got) != items[0].attempt {
		t.Errorf("hook ran %d times over %d attempts, want one per bounded retry attempt", got, items[0].attempt)
	}
	if len(h.mailer.sent) != 0 {
		t.Errorf("mail sent despite hook error: %+v", h.mailer.sent)
	}
}

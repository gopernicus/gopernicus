package invitations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

func prepareManagement(t *testing.T, svc *Service, id string) PreparedManagement {
	t.Helper()
	p, err := svc.PrepareManagement(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type managementReader struct {
	InvitationRepository
	reads int
}

func (r *managementReader) Get(ctx context.Context, id string) (Invitation, error) {
	r.reads++
	return r.InvitationRepository.Get(ctx, id)
}

func TestPreparedManagementReadsOnceAndPinsTarget(t *testing.T) {
	for _, resend := range []bool{false, true} {
		repo := newFakeInvRepo()
		reader := &managementReader{InvitationRepository: repo}
		svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{})
		svc.invitations = reader
		inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "issuer", "secret", false, time.Now().Add(time.Hour))
		p := prepareManagement(t, svc, inv.ID)
		if p.ID() != inv.ID || p.InvitedBy() != "issuer" || reader.reads != 1 {
			t.Fatalf("prepared=%+v reads=%d", p, reader.reads)
		}
		// No principal is required by the domain; inbound already owns admission.
		var err error
		if resend {
			_, err = svc.Resend(t.Context(), p, "")
		} else {
			err = svc.Cancel(t.Context(), p)
		}
		if err != nil || reader.reads != 1 {
			t.Fatalf("resend=%v err=%v reads=%d", resend, err, reader.reads)
		}
	}
}

func TestPreparedManagementRejectsZeroForeignCanceledAndStale(t *testing.T) {
	repo := newFakeInvRepo()
	mailer := &recordingMailer{}
	svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{Mailer: mailer})
	other := newSvc(t, repo, &fakeGranter{}, constructorConfig{})
	inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "issuer", "secret", false, time.Now().Add(time.Hour))
	for _, p := range []PreparedManagement{{}, prepareManagement(t, other, inv.ID)} {
		if err := svc.Cancel(t.Context(), p); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("cancel zero/foreign=%v", err)
		}
		if _, err := svc.Resend(t.Context(), p, ""); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("resend zero/foreign=%v", err)
		}
	}
	p := prepareManagement(t, svc, inv.ID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.PrepareManagement(ctx, inv.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prepare=%v", err)
	}
	if err := svc.Cancel(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cancel=%v", err)
	}
	if _, err := svc.Resend(ctx, p, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resend=%v", err)
	}
	if len(mailer.sent) != 0 {
		t.Fatal("invalid management delivered")
	}
	if _, err := svc.Resend(t.Context(), p, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(t.Context(), p); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale cancel=%v", err)
	}
	if _, err := svc.Resend(t.Context(), p, ""); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale resend=%v", err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("stale command delivered: %d", len(mailer.sent))
	}
}

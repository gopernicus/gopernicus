package serviceaccount

import (
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

var base = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func TestNewValid(t *testing.T) {
	sa, err := New(cryptids.IDGenerator{}, "deployer", "CI bot", "admin-1", false, "", base)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sa.ID == "" {
		t.Error("New did not generate an ID")
	}
	if sa.Name != "deployer" || sa.CreatedBy != "admin-1" {
		t.Errorf("New round-trip: %+v", sa)
	}
	if !sa.CreatedAt.Equal(base) || !sa.UpdatedAt.Equal(base) {
		t.Errorf("timestamps = %v/%v, want %v", sa.CreatedAt, sa.UpdatedAt, base)
	}
}

func TestNewActAsUserRequiresOwner(t *testing.T) {
	// The pinned invariant: ActAsUser → OwnerUserID != "".
	if _, err := New(cryptids.IDGenerator{}, "personal", "", "admin", true, "", base); !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("act-as-user with no owner: err=%v, want ErrInvalidInput", err)
	}
	sa, err := New(cryptids.IDGenerator{}, "personal", "", "admin", true, "owner-9", base)
	if err != nil {
		t.Fatalf("act-as-user with owner: %v", err)
	}
	if !sa.ActAsUser || sa.OwnerUserID != "owner-9" {
		t.Errorf("act-as-user account: %+v", sa)
	}
}

func TestNewRequiredFields(t *testing.T) {
	if _, err := New(cryptids.IDGenerator{}, "", "d", "admin", false, "", base); !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("blank name: err=%v, want ErrInvalidInput", err)
	}
	if _, err := New(cryptids.IDGenerator{}, "name", "d", "", false, "", base); !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("blank created-by: err=%v, want ErrInvalidInput", err)
	}
}

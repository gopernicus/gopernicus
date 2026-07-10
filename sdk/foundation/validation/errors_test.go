package validation

import (
	"errors"
	"strings"
	"testing"
)

func TestErrors_Empty(t *testing.T) {
	var errs Errors
	if errs.Err() != nil {
		t.Error("empty Errors.Err() should be nil")
	}
	if errs.HasErrors() {
		t.Error("empty Errors.HasErrors() should be false")
	}
}

func TestErrors_Add(t *testing.T) {
	var errs Errors
	errs.Add(nil) // should be ignored
	errs.Add(errors.New("name is required"))
	errs.Add(nil) // should be ignored
	errs.Add(errors.New("email must be a valid email address"))

	if !errs.HasErrors() {
		t.Error("HasErrors() should be true after adding errors")
	}
	if len(errs.All()) != 2 {
		t.Errorf("All() len = %d, want 2", len(errs.All()))
	}

	combined := errs.Err()
	if combined == nil {
		t.Fatal("Err() should not be nil")
	}
	if !strings.Contains(combined.Error(), "name is required") {
		t.Errorf("Err() missing first error: %s", combined.Error())
	}
	if !strings.Contains(combined.Error(), "email must be a valid email address") {
		t.Errorf("Err() missing second error: %s", combined.Error())
	}
}

func TestErrors_WithValidators(t *testing.T) {
	var errs Errors
	errs.Add(Required("name", ""))
	errs.Add(Email("email", "bad"))
	errs.Add(Email("backup", "good@example.com")) // passes, adds nil

	if len(errs.All()) != 2 {
		t.Errorf("expected 2 errors, got %d", len(errs.All()))
	}
}

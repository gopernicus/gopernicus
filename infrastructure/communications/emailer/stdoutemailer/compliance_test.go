package stdoutemailer_test

import (
	"log/slog"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer/emailertest"
	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer/stdoutemailer"
)

func TestCompliance(t *testing.T) {
	client := stdoutemailer.New(slog.Default())
	emailertest.RunSuite(t, client)
}

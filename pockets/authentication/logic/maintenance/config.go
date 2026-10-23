package maintenance

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

// The bounds a Config is checked against.
const (
	// MinSecurityEventRetention is the shortest SecurityEventRetention New
	// accepts: the database floor (365 days) plus one day of clock-skew margin,
	// so the bundled service can never ask a store for a cutoff it refuses.
	MinSecurityEventRetention = 366 * 24 * time.Hour
	// MaxBatchSize is the largest BatchSize New accepts. It equals the clamp the
	// PostgreSQL purge function applies, so a full batch can never read as a
	// short one.
	MaxBatchSize = 10000
)

const (
	defaultSessionRetention          = 24 * time.Hour
	defaultOAuthStateRetention       = time.Hour
	defaultGrantRetention            = 24 * time.Hour
	defaultContactChangeRetention    = 24 * time.Hour
	defaultInvitationRetention       = 90 * 24 * time.Hour
	defaultSecurityEventRetention    = 400 * 24 * time.Hour
	defaultSecurityEventStepInterval = 24 * time.Hour
	defaultBatchSize                 = 1000
	defaultArchiveChunkRows          = 5000
	defaultPassBudget                = 5 * time.Minute
	defaultArchivePrefix             = "security-events/"
	defaultInterval                  = time.Hour

	// challengeRetention is how long an expired challenge is kept.
	challengeRetention = time.Hour
)

// ErrSecurityEventRetentionTooShort is returned by New when
// Config.SecurityEventRetention is set below MinSecurityEventRetention. It
// wraps sdk.ErrInvalidInput.
var ErrSecurityEventRetentionTooShort = fmt.Errorf("maintenance: SecurityEventRetention is below MinSecurityEventRetention (366 days: the 365-day database floor plus a day): %w", sdk.ErrInvalidInput)

// ArchiveSink is the object store the rail is archived to — the narrow subset
// of filestorage.Storer the service uses, which every filestorage.Storer
// satisfies. Upload replaces an existing object, so the service checks Exists
// and never writes over a key.
type ArchiveSink interface {
	Upload(ctx context.Context, path string, reader io.Reader) error
	Exists(ctx context.Context, path string) (bool, error)
	Download(ctx context.Context, path string) (io.ReadCloser, error)
	GetObjectSize(ctx context.Context, path string) (int64, error)
}

// Config is the retention policy. Every duration is how long a row is kept
// past the instant that makes it eligible; zero selects the default and a
// negative value is sdk.ErrInvalidInput. Retention values are Go defaults a
// host raises — nothing here reads the environment.
type Config struct {
	// SessionRetention keeps a session past its expiry. Default 24h.
	SessionRetention time.Duration
	// OAuthStateRetention keeps an OAuth state past its expiry. Default 1h.
	OAuthStateRetention time.Duration
	// GrantRetention keeps an authentication grant past its expiry. Default 24h.
	GrantRetention time.Duration
	// ContactChangeRetention keeps a pending contact change past its expiry.
	// Default 24h.
	ContactChangeRetention time.Duration
	// InvitationRetention keeps a terminal invitation past its last update and
	// a pending one past its expiry. Accepting claims older than it are counted
	// and reported, never purged. Default 90 days.
	InvitationRetention time.Duration
	// SecurityEventRetention keeps a rail row past its creation. Default 400
	// days; below MinSecurityEventRetention is ErrSecurityEventRetentionTooShort.
	SecurityEventRetention time.Duration
	// SecurityEventStepInterval is the rail step's cadence inside the pass: the
	// step is skipped as not_due when it completed less than this ago in this
	// process, and always runs on the first pass after a start. Default 24h.
	SecurityEventStepInterval time.Duration
	// BatchSize bounds one purge statement. Default 1000; above MaxBatchSize is
	// sdk.ErrInvalidInput.
	BatchSize int
	// ArchiveChunkRows is how many rail rows one archive object holds before
	// the rows tied at its last instant. Default 5000.
	ArchiveChunkRows int
	// PassBudget is how long one pass may keep starting work. It is checked
	// between batches and chunks — never a deadline that cancels a statement —
	// so a pass can overrun it by one batch or chunk. Keep it shorter than the
	// jobs lease window when a schedule runs the pass. Default 5m.
	PassBudget time.Duration
	// ArchiveSink receives one object per rail chunk before the chunk is
	// purged. Nil purges the rail unarchived — a valid policy the registration
	// log reports as archive_sink=none.
	ArchiveSink ArchiveSink
	// ArchivePrefix is the key prefix of every archive object. Default
	// "security-events/".
	ArchivePrefix string
	// ArchiveLocation is the host's label for the sink ("gcs://audit-archive",
	// "disk:/var/lib/app/archive"), carried in every archived row's Details.
	ArchiveLocation string
}

// resolve applies the defaults and refuses an invalid policy.
func (c Config) resolve() (Config, error) {
	for _, d := range []struct {
		name  string
		value time.Duration
	}{
		{"SessionRetention", c.SessionRetention}, {"OAuthStateRetention", c.OAuthStateRetention},
		{"GrantRetention", c.GrantRetention}, {"ContactChangeRetention", c.ContactChangeRetention},
		{"InvitationRetention", c.InvitationRetention}, {"SecurityEventRetention", c.SecurityEventRetention},
		{"SecurityEventStepInterval", c.SecurityEventStepInterval}, {"PassBudget", c.PassBudget},
	} {
		if d.value < 0 {
			return Config{}, fmt.Errorf("maintenance: %s is negative: %w", d.name, sdk.ErrInvalidInput)
		}
	}
	switch {
	case c.BatchSize < 0:
		return Config{}, fmt.Errorf("maintenance: BatchSize is negative: %w", sdk.ErrInvalidInput)
	case c.BatchSize > MaxBatchSize:
		return Config{}, fmt.Errorf("maintenance: BatchSize %d exceeds MaxBatchSize %d: %w", c.BatchSize, MaxBatchSize, sdk.ErrInvalidInput)
	case c.ArchiveChunkRows < 0:
		return Config{}, fmt.Errorf("maintenance: ArchiveChunkRows is negative: %w", sdk.ErrInvalidInput)
	case c.SecurityEventRetention != 0 && c.SecurityEventRetention < MinSecurityEventRetention:
		return Config{}, ErrSecurityEventRetentionTooShort
	}
	c.SessionRetention = orDefault(c.SessionRetention, defaultSessionRetention)
	c.OAuthStateRetention = orDefault(c.OAuthStateRetention, defaultOAuthStateRetention)
	c.GrantRetention = orDefault(c.GrantRetention, defaultGrantRetention)
	c.ContactChangeRetention = orDefault(c.ContactChangeRetention, defaultContactChangeRetention)
	c.InvitationRetention = orDefault(c.InvitationRetention, defaultInvitationRetention)
	c.SecurityEventRetention = orDefault(c.SecurityEventRetention, defaultSecurityEventRetention)
	c.SecurityEventStepInterval = orDefault(c.SecurityEventStepInterval, defaultSecurityEventStepInterval)
	c.PassBudget = orDefault(c.PassBudget, defaultPassBudget)
	if c.BatchSize == 0 {
		c.BatchSize = defaultBatchSize
	}
	if c.ArchiveChunkRows == 0 {
		c.ArchiveChunkRows = defaultArchiveChunkRows
	}
	if c.ArchivePrefix == "" {
		c.ArchivePrefix = defaultArchivePrefix
	}
	if strings.ContainsRune(c.ArchivePrefix, ':') || filestorage.ValidatePath(c.ArchivePrefix+"x") != nil {
		return Config{}, fmt.Errorf("maintenance: ArchivePrefix %q is not a relative object-key prefix without colons: %w", c.ArchivePrefix, sdk.ErrInvalidInput)
	}
	return c, nil
}

func orDefault(value, def time.Duration) time.Duration {
	if value == 0 {
		return def
	}
	return value
}

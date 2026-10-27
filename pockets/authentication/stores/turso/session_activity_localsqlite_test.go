package turso

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// The session-activity (0029) store legs, untagged so `make check` runs them
// against a local SQLite file. The conformance group itself runs through
// TestConformance_LocalSQLite.

// TestSessionActivityNeverBlanketProbed pins 0029 out of Repositories'
// blanket column probe: the column is required only when the host sets
// SessionsConfig.IdleTimeout.
func TestSessionActivityNeverBlanketProbed(t *testing.T) {
	for _, pc := range probeColumns {
		if pc.migration == sessionActivityMigration || pc.column == "last_rotated_at" {
			t.Fatalf("probeColumns lists %+v; 0029 must be probed only through SessionStore.ProbeSchema", pc)
		}
	}
}

// TestSessionActivityRowMatchesSessionRow pins the wired-only row to
// sessionRow plus last_rotated_at, field for field and tag for tag.
func TestSessionActivityRowMatchesSessionRow(t *testing.T) {
	plain, wired := reflect.TypeFor[sessionRow](), reflect.TypeFor[sessionActivityRow]()
	if wired.NumField() != plain.NumField()+1 {
		t.Fatalf("sessionActivityRow has %d fields, want sessionRow's %d plus the stamp", wired.NumField(), plain.NumField())
	}
	for i := range plain.NumField() {
		p, w := plain.Field(i), wired.Field(i)
		if p.Name != w.Name || p.Type != w.Type || p.Tag != w.Tag {
			t.Errorf("field %d: sessionActivityRow %s %s %q, sessionRow %s %s %q", i, w.Name, w.Type, w.Tag, p.Name, p.Type, p.Tag)
		}
	}
	if last := wired.Field(plain.NumField()); last.Tag.Get("db") != "last_rotated_at" {
		t.Errorf("last field = %s %q, want the last_rotated_at stamp", last.Name, last.Tag)
	}
	if sessionActivityColumns != sessionColumns+", last_rotated_at" {
		t.Errorf("sessionActivityColumns = %q, want sessionColumns plus last_rotated_at", sessionActivityColumns)
	}
}

// TestSessionActivityRowMapperCopiesEveryField fills every field of
// sessionActivityRow with a distinct non-zero value and requires
// sessionRow() to carry each one across, so a field added to both structs but
// not to the mapper fails here rather than reading back zero.
func TestSessionActivityRowMapperCopiesEveryField(t *testing.T) {
	var src sessionActivityRow
	sv := reflect.ValueOf(&src).Elem()
	for i := range sv.NumField() {
		fillDistinct(t, sv.Type().Field(i).Name, sv.Field(i), i+1)
	}
	got := reflect.ValueOf(src.sessionRow())
	for i := range got.NumField() {
		name := got.Type().Field(i).Name
		want := sv.FieldByName(name)
		if !want.IsValid() {
			t.Fatalf("sessionRow.%s has no sessionActivityRow source field", name)
		}
		if got.Field(i).IsZero() {
			t.Errorf("sessionRow().%s is zero: the mapper does not copy it", name)
		} else if !reflect.DeepEqual(got.Field(i).Interface(), want.Interface()) {
			t.Errorf("sessionRow().%s = %v, want %v", name, got.Field(i).Interface(), want.Interface())
		}
	}
}

// fillDistinct sets v, and every exported field beneath it, to a non-zero
// value derived from seed; an unhandled kind fails so a new field type cannot
// pass vacuously as zero.
func fillDistinct(t *testing.T, name string, v reflect.Value, seed int) {
	t.Helper()
	if v.Type() == reflect.TypeFor[time.Time]() {
		v.Set(reflect.ValueOf(time.Unix(int64(1_700_000_000+seed), int64(seed)).UTC()))
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(fmt.Sprintf("%s-%d", name, seed))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int32, reflect.Int64:
		v.SetInt(int64(seed))
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillDistinct(t, name+"."+v.Type().Field(i).Name, v.Field(i), seed)
			}
		}
	default:
		t.Fatalf("fillDistinct: field %s has unhandled kind %s; extend the filler", name, v.Kind())
	}
}

// TestSessionActivitySchema_LocalSQLite pins the conditional rule on a
// database without 0029: Repositories constructs, ProbeSchema refuses with
// sdk.ErrNotFound naming the file, the pocket boots unwired (and refreshes
// without naming the column), and refuses with ErrIdleTimeoutSchemaMissing
// once IdleTimeout is set. A closed connection is never schema-missing.
func TestSessionActivitySchema_LocalSQLite(t *testing.T) {
	db, _, _ := oauthSQLite(t)
	ctx := t.Context()
	if _, err := db.Exec(ctx, "ALTER TABLE sessions DROP COLUMN last_rotated_at"); err != nil {
		t.Fatalf("drop last_rotated_at: %v", err)
	}
	repos, err := Repositories(ctx, db)
	if err != nil {
		t.Fatalf("Repositories without 0029 = %v, want it to construct (idle expiry is opt-in)", err)
	}
	store := repos.Sessions.(*SessionStore)
	storetest.RunSessionActivitySchemaProbe(t, func(*testing.T) session.ActivityRepository { return store })
	if err := store.ProbeSchema(ctx); err == nil || !strings.Contains(err.Error(), sessionActivityMigration) {
		t.Fatalf("ProbeSchema = %v, want an error naming %s", err, sessionActivityMigration)
	}

	unwired, err := newIdlePocket(t, repos, 0)
	if err != nil {
		t.Fatalf("the pocket with idle expiry unwired over a database without 0029 = %v, want it to boot", err)
	}
	seedIdleUser(t, db, "unmigrated")
	seedIdleSession(t, repos, "unmigrated", "tok-unmigrated", time.Now())
	if _, err := unwired.Authentication.Refresh(ctx, "tok-unmigrated"); err != nil {
		t.Fatalf("an unwired refresh over a database without 0029 = %v, want success", err)
	}

	_, err = newIdlePocket(t, repos, time.Hour)
	if !errors.Is(err, auth.ErrIdleTimeoutSchemaMissing) || !strings.Contains(err.Error(), sessionActivityMigration) {
		t.Fatalf("the pocket with idle expiry wired over a database without 0029 = %v, want ErrIdleTimeoutSchemaMissing naming %s", err, sessionActivityMigration)
	}

	db.Close()
	if err := store.ProbeSchema(ctx); err == nil || errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("ProbeSchema on a closed connection = %v, want an infrastructure error, never sdk.ErrNotFound", err)
	}
}

// TestSessionActivityEncoding_LocalSQLite proves RotateAt writes
// last_rotated_at in exactly created_at's fixed-width UTC TEXT encoding
// (sub-microsecond digits and a non-UTC zone included), that the two order
// lexically as they do in time, and that the read-back observation deletes by
// CAS.
func TestSessionActivityEncoding_LocalSQLite(t *testing.T) {
	db, repos, _ := oauthSQLite(t)
	ctx := t.Context()
	store := repos.Sessions.(*SessionStore)
	created := time.Date(2026, 10, 27, 9, 0, 0, 123456789, time.UTC)
	sess := seedIdleSession(t, repos, "u-enc", "tok-enc", created)
	stamp := created.Add(90*time.Second + 7).In(time.FixedZone("UTC-5", -5*60*60))
	if err := store.RotateAt(ctx, sess.ID, sess.RefreshTokenHash, "enc-next", stamp); err != nil {
		t.Fatalf("RotateAt: %v", err)
	}

	var rawCreated, rawStamp string
	if err := db.QueryRow(ctx, `SELECT created_at, last_rotated_at FROM sessions WHERE id = ?`, sess.ID).Scan(&rawCreated, &rawStamp); err != nil {
		t.Fatal(err)
	}
	if rawStamp != tursodb.FormatTime(stamp) || rawCreated != tursodb.FormatTime(created) {
		t.Fatalf("stored created_at %q, last_rotated_at %q; want %q, %q", rawCreated, rawStamp, tursodb.FormatTime(created), tursodb.FormatTime(stamp))
	}
	if len(rawStamp) != len(rawCreated) || !strings.HasSuffix(rawStamp, "Z") {
		t.Fatalf("last_rotated_at %q is not created_at's fixed-width UTC encoding (%q)", rawStamp, rawCreated)
	}
	var ordered bool
	if err := db.QueryRow(ctx, `SELECT last_rotated_at > created_at FROM sessions WHERE id = ?`, sess.ID).Scan(&ordered); err != nil {
		t.Fatal(err)
	}
	if !ordered {
		t.Fatalf("last_rotated_at %q does not order after created_at %q as TEXT", rawStamp, rawCreated)
	}

	got, _, act, err := store.GetByRefreshHashWithActivity(ctx, "enc-next")
	if err != nil {
		t.Fatal(err)
	}
	if !act.Stamped || !act.LastRotatedAt.Equal(stamp) || act.LastRotatedAt.Location() != time.UTC {
		t.Fatalf("stamp read back as %+v, want %v in UTC", act, stamp)
	}
	if deleted, err := store.DeleteIfIdle(ctx, got.ID, act, got.RotationCount); err != nil || !deleted {
		t.Fatalf("DeleteIfIdle(read-back observation) = %v, %v; want true", deleted, err)
	}
}

// TestSessionActivityDeleteIfIdleRace_LocalSQLite races DeleteIfIdle callers
// holding the same observation: exactly one deletes.
func TestSessionActivityDeleteIfIdleRace_LocalSQLite(t *testing.T) {
	_, repos, _ := oauthSQLite(t)
	store := repos.Sessions.(*SessionStore)
	for i, stamped := range []bool{false, true} {
		sess := seedIdleSession(t, repos, "u-race", fmt.Sprintf("tok-race-%d", i), time.Now().Add(-2*time.Hour))
		hash := sess.RefreshTokenHash
		if stamped {
			hash = "race-next-" + sess.ID
			if err := store.RotateAt(t.Context(), sess.ID, sess.RefreshTokenHash, hash, time.Now().Add(-90*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		got, _, act, err := store.GetByRefreshHashWithActivity(t.Context(), hash)
		if err != nil {
			t.Fatal(err)
		}
		const racers = 8
		var wg sync.WaitGroup
		var mu sync.Mutex
		deleted := 0
		for range racers {
			wg.Go(func() {
				ok, err := store.DeleteIfIdle(t.Context(), got.ID, act, got.RotationCount)
				if err != nil {
					t.Errorf("DeleteIfIdle: %v", err)
					return
				}
				if ok {
					mu.Lock()
					deleted++
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		if deleted != 1 {
			t.Fatalf("stamped=%v: %d of %d racing DeleteIfIdle calls deleted, want exactly 1", stamped, deleted, racers)
		}
	}
}

// TestSessionActivityPocket_LocalSQLite drives the wired pocket's Refresh over
// the store: two concurrent refreshes of an idle row give two generic 401s,
// one deletion, one session_idle_expired row and no refresh_reuse; an idle
// STAMPED row expires through the read-back observation (an encoding defect
// would surface as an internal error instead); a legacy-rotated row is
// grandfathered and stamped on its next refresh; an unwired pocket never
// stamps.
func TestSessionActivityPocket_LocalSQLite(t *testing.T) {
	db, repos, _ := oauthSQLite(t)
	ctx := t.Context()
	store := repos.Sessions.(*SessionStore)
	comps, err := newIdlePocket(t, repos, time.Hour)
	if err != nil {
		t.Fatalf("the pocket with idle expiry wired over a migrated database = %v", err)
	}
	seedIdleUser(t, db, "u-idle")

	race := seedIdleSession(t, repos, "u-idle", "tok-idle-race", time.Now().Add(-2*time.Hour))
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { _, errs[i] = comps.Authentication.Refresh(ctx, "tok-idle-race") })
	}
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, authlogic.ErrInvalidRefreshToken) {
			t.Fatalf("racing idle refresh %d = %v, want ErrInvalidRefreshToken", i, err)
		}
	}
	if _, err := store.Get(ctx, race.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("idle row after the race = %v, want sdk.ErrNotFound", err)
	}
	if n := countEvents(t, db, "session_idle_expired", race.ID); n != 1 {
		t.Fatalf("session_idle_expired rows for the raced session = %d, want 1", n)
	}
	if n := countEvents(t, db, "refresh_reuse", race.ID); n != 0 {
		t.Fatalf("refresh_reuse rows for the raced session = %d, want 0", n)
	}

	stamped := seedIdleSession(t, repos, "u-idle", "tok-idle-stamped", time.Now().Add(-3*time.Hour))
	if err := store.RotateAt(ctx, stamped.ID, stamped.RefreshTokenHash, cryptidsHash(t, "tok-idle-stamped-2"), time.Now().Add(-2*time.Hour+333)); err != nil {
		t.Fatal(err)
	}
	if _, err := comps.Authentication.Refresh(ctx, "tok-idle-stamped-2"); !errors.Is(err, authlogic.ErrInvalidRefreshToken) {
		t.Fatalf("refresh of an idle stamped row = %v, want ErrInvalidRefreshToken (an internal error means the CAS missed the stored encoding)", err)
	}
	if n := countEvents(t, db, "session_idle_expired", stamped.ID); n != 1 {
		t.Fatalf("session_idle_expired rows for the stamped session = %d, want 1", n)
	}

	legacy := seedIdleSession(t, repos, "u-idle", "tok-legacy", time.Now().Add(-3*time.Hour))
	if err := store.Rotate(ctx, legacy.ID, legacy.RefreshTokenHash, cryptidsHash(t, "tok-legacy-2")); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if _, err := comps.Authentication.Refresh(ctx, "tok-legacy-2"); err != nil {
		t.Fatalf("refresh of a grandfathered legacy-rotated row = %v, want success", err)
	}
	row, err := store.Get(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, act, err := store.GetByRefreshHashWithActivity(ctx, row.RefreshTokenHash)
	if err != nil || !act.Stamped || act.LastRotatedAt.Before(before.Add(-time.Second)) {
		t.Fatalf("grandfathered row after a wired refresh = %+v, %v; want stamped now", act, err)
	}

	unwired, err := newIdlePocket(t, repos, 0)
	if err != nil {
		t.Fatal(err)
	}
	plain := seedIdleSession(t, repos, "u-idle", "tok-unwired", time.Now())
	if _, err := unwired.Authentication.Refresh(ctx, "tok-unwired"); err != nil {
		t.Fatalf("unwired refresh = %v", err)
	}
	var stamp *string
	if err := db.QueryRow(ctx, `SELECT last_rotated_at FROM sessions WHERE id = ?`, plain.ID).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	if stamp != nil {
		t.Fatalf("an unwired refresh stamped last_rotated_at = %q, want NULL", *stamp)
	}
}

// newIdlePocket builds the pocket over repos with SessionsConfig.IdleTimeout
// idle (zero = unwired).
func newIdlePocket(t *testing.T, repos auth.Repositories, idle time.Duration) (*auth.Components, error) {
	t.Helper()
	protector, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing{Active: "test", Keys: map[string][]byte{"test": []byte("session-activity-test-key-32-byt")}})
	if err != nil {
		t.Fatal(err)
	}
	return auth.New(repos, &protocolTestSigner{now: time.Now()}, environment.ModeDevelopment, delivery.ModeOff,
		auth.WithPassword(auth.PasswordConfig{PasswordFlowsDisabled: true, RequireVerifiedEmail: true}),
		auth.WithIdentity(auth.IdentityConfig{ChallengeProtector: protector}),
		auth.WithSessions(auth.SessionsConfig{IdleTimeout: idle}),
		auth.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
}

func seedIdleUser(t *testing.T, db *tursodb.DB, id string) {
	t.Helper()
	now := tursodb.FormatTime(time.Now())
	if _, err := db.Exec(t.Context(), `INSERT INTO users (id, display_name, created_at, updated_at) VALUES (?, '', ?, ?)`, id, now, now); err != nil {
		t.Fatal(err)
	}
}

// seedIdleSession creates a first-party session whose refresh token is
// presented, created at created.
func seedIdleSession(t *testing.T, repos auth.Repositories, userID, presented string, created time.Time) session.Session {
	t.Helper()
	sess, _ := session.NewSession(userID, 24*time.Hour, created)
	sess.RefreshTokenHash = cryptidsHash(t, presented)
	out, err := repos.Sessions.Create(t.Context(), sess)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func cryptidsHash(t *testing.T, presented string) string {
	t.Helper()
	h, err := cryptids.SHA256(presented)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func countEvents(t *testing.T, db *tursodb.DB, eventType, sessionID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM security_events WHERE event_type = ? AND json_extract(details, '$.session_id') = ?`,
		eventType, sessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

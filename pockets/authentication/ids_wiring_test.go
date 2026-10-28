package authentication

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

var wiringUUIDv7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// wiringInvitations keeps the pending rows Create receives.
type wiringInvitations struct {
	stubInvitations
	created []invitations.Invitation
}

func (r *wiringInvitations) Create(_ context.Context, inv invitations.Invitation) (invitations.Invitation, error) {
	r.created = append(r.created, inv)
	return inv, nil
}

// TestWithIDsReachesEveryClass drives the public constructor: with no WithIDs
// the identity class (service accounts, invitations) mints NanoIDs and the
// record class (API keys, security events) UUIDv7s; with WithIDs(counting)
// every one of them carries the host generator's prefix. The counting generator
// never repeats, so primary keys and the contact-change stale-request guard
// keep working.
func TestWithIDsReachesEveryClass(t *testing.T) {
	nano := func(s string) bool {
		return len(s) == sdk.DefaultIDLength && strings.Trim(s, sdk.DefaultIDAlphabet) == ""
	}
	v7 := wiringUUIDv7.MatchString
	for _, tc := range []struct {
		name             string
		opts             func() []Option
		identity, record func(string) bool
	}{
		{"class defaults", func() []Option { return nil }, nano, v7},
		{"host generator", func() []Option {
			var n atomic.Int64
			return []Option{WithIDs(sdk.NewIDGenerator(func() (string, error) { return fmt.Sprintf("fixed-%d", n.Add(1)), nil }))}
		}, hasFixedPrefix, hasFixedPrefix},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := &recorderEvents{}
			pending := &wiringInvitations{}
			cfg := browserBaseConfig()
			opts := append([]Option{
				WithPassword(PasswordConfig{Hasher: cfg.Hasher, PasswordFlowsDisabled: true}),
				WithDelivery(DeliveryConfig{Mailer: cfg.Mailer, DeliveryEncrypter: stubEncrypter{}}),
				WithInvitations(InvitationsConfig{Granter: stubGranter{}, InviteCheck: allowInvite}),
				WithAdministration(AdministrationConfig{MachineRoutesGate: func(next http.Handler) http.Handler { return next }}),
			}, tc.opts()...)
			c, err := New(testRepositories(Repositories{
				ServiceAccounts: &liveServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
				APIKeys:         &liveAPIKeys{m: map[string]apikey.APIKey{}},
				SecurityEvents:  events,
				Invitations:     pending,
			}), cfg.TokenSigner, cfg.RuntimeMode, delivery.ModeInProcess, opts...)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()

			sa, err := c.Authentication.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
			if err != nil {
				t.Fatalf("CreateServiceAccount: %v", err)
			}
			if !tc.identity(sa.ID) {
				t.Errorf("service account id %q is not identity-class", sa.ID)
			}
			key, _, err := c.Authentication.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
			if err != nil {
				t.Fatalf("MintAPIKey: %v", err)
			}
			if !tc.record(key.ID) {
				t.Errorf("API key id %q is not record-class", key.ID)
			}

			res, err := c.Invitations.Create(ctx, invitations.CreateInput{ResourceType: "team", ResourceID: "t1", Relation: "member", Identifier: "invitee@example.com", InvitedBy: "admin"})
			if err != nil {
				t.Fatalf("Invitations.Create: %v", err)
			}
			if res.DirectlyAdded || len(pending.created) != 1 {
				t.Fatalf("Invitations.Create = %+v, %d rows; want one pending row", res, len(pending.created))
			}
			if !tc.identity(pending.created[0].ID) {
				t.Errorf("invitation id %q is not identity-class", pending.created[0].ID)
			}

			c.SecurityEvents.Record(ctx, securityevent.Input{Type: securityevent.TypeLogout, Status: securityevent.StatusSuccess})
			if len(events.created) == 0 {
				t.Fatal("no security events recorded")
			}
			for _, evt := range events.created {
				if !tc.record(evt.ID) {
					t.Errorf("security event %s id %q is not record-class", evt.EventType, evt.ID)
				}
			}
		})
	}
}

func hasFixedPrefix(s string) bool { return strings.HasPrefix(s, "fixed-") }

// wiringExternalProvider authenticates every credential as one verified
// subject.
type wiringExternalProvider struct{}

func (wiringExternalProvider) Issuer() string { return "https://idp.example.test" }
func (wiringExternalProvider) Authenticate(context.Context, sdkauth.Credential) (sdkauth.Identity, error) {
	return sdkauth.Identity{Issuer: "https://idp.example.test", Subject: "ext-subject", Email: "ext@example.test", EmailVerified: true,
		AuthenticatedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

// wiringUsers keeps the users, identifiers and external subjects Provision
// receives, with the ids the engine minted.
type wiringUsers struct {
	configUsers
	mu       sync.Mutex
	users    map[string]user.User
	idents   []identifier.Identifier
	subjects []externalsubject.ExternalSubject
}

func (r *wiringUsers) Provision(_ context.Context, u user.User, ident identifier.Identifier, creds user.InitialCredentials) (user.User, identifier.Identifier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ident.UserID = u.ID
	r.users[u.ID] = u
	r.idents = append(r.idents, ident)
	if creds.External != nil {
		row := *creds.External
		row.UserID = u.ID
		r.subjects = append(r.subjects, row)
	}
	return u, ident, nil
}

// wiringSubjects reads back the subjects wiringUsers provisioned.
type wiringSubjects struct {
	cookieTestSubjects
	users *wiringUsers
}

func (r wiringSubjects) Get(_ context.Context, provider, subject string) (externalsubject.ExternalSubject, error) {
	r.users.mu.Lock()
	defer r.users.mu.Unlock()
	for _, row := range r.users.subjects {
		if row.Provider == provider && row.Subject == subject {
			return row, nil
		}
	}
	return externalsubject.ExternalSubject{}, sdk.ErrNotFound
}

func (r *wiringUsers) Get(_ context.Context, id string) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
}

// TestWithIDsReachesTheExternalEngine drives external provisioning through the
// public constructor under WithIDs(counting): the provisioned user and its
// identifier both carry the host generator's prefix.
func TestWithIDsReachesTheExternalEngine(t *testing.T) {
	var n atomic.Int64
	counting := sdk.NewIDGenerator(func() (string, error) { return fmt.Sprintf("fixed-%d", n.Add(1)), nil })
	users := &wiringUsers{users: map[string]user.User{}}
	cfg := browserBaseConfig()
	c, err := New(testRepositories(Repositories{Users: users, ExternalSubjects: wiringSubjects{users: users}}), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
		WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}),
		WithExternal(authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{{Name: "idp", Provider: wiringExternalProvider{}, ProvisionOnFirstVerification: true}}}),
		WithIDs(counting))
	if err != nil {
		t.Fatal(err)
	}
	// The engine routes on the token's unverified iss before the provider runs.
	raw := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://idp.example.test","sub":"ext-subject"}`)) + ".sig"
	if _, _, err := c.External.Verify(context.Background(), raw, authlogic.TransportHeader); err != nil {
		t.Fatalf("External.Verify: %v", err)
	}
	users.mu.Lock()
	defer users.mu.Unlock()
	if len(users.users) != 1 || len(users.idents) != 1 {
		t.Fatalf("provisioned %d users, %d identifiers; want one each", len(users.users), len(users.idents))
	}
	for id := range users.users {
		if !hasFixedPrefix(id) {
			t.Errorf("external user id %q does not carry the host generator's prefix", id)
		}
	}
	if !hasFixedPrefix(users.idents[0].ID) {
		t.Errorf("external identifier id %q does not carry the host generator's prefix", users.idents[0].ID)
	}
}

// wiringArchiveRepo holds an archivable security-event rail; every other
// retention step finds nothing.
type wiringArchiveRepo struct {
	mu   sync.Mutex
	rail []maintenance.ArchivedEvent
}

func (*wiringArchiveRepo) PurgeExpiredSessions(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func (*wiringArchiveRepo) PurgeExpiredOAuthStates(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func (*wiringArchiveRepo) PurgeExpiredGrants(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func (*wiringArchiveRepo) PurgeExpiredContactChanges(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func (*wiringArchiveRepo) PurgeInvitations(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func (*wiringArchiveRepo) CountStaleAcceptingInvitations(context.Context, time.Time) (int, error) {
	return 0, nil
}
func (*wiringArchiveRepo) PruneOAuth2(context.Context, time.Time, int) (int, error) { return 0, nil }

func (r *wiringArchiveRepo) ExportSecurityEvents(_ context.Context, before time.Time, after maintenance.ExportCursor, limit int) ([]maintenance.ArchivedEvent, maintenance.ExportCursor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []maintenance.ArchivedEvent
	for _, e := range r.rail {
		if !e.CreatedAt.Before(before) || e.CreatedAt.Before(after.CreatedAt) || (e.CreatedAt.Equal(after.CreatedAt) && e.ID <= after.ID) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, after, nil
	}
	last := out[len(out)-1]
	return out, maintenance.ExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

func (r *wiringArchiveRepo) PurgeSecurityEventsBefore(_ context.Context, before time.Time, _ int) (maintenance.PurgeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.rail[:0]
	n := 0
	for _, e := range r.rail {
		if e.CreatedAt.Before(before) {
			n++
			continue
		}
		kept = append(kept, e)
	}
	r.rail = kept
	return maintenance.PurgeResult{Cutoff: before, Count: n}, nil
}

// wiringSink is an in-memory archive sink that reads back what it stored.
type wiringSink struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (s *wiringSink) Upload(_ context.Context, path string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[path] = b
	return nil
}
func (s *wiringSink) Exists(_ context.Context, path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[path]
	return ok, nil
}
func (s *wiringSink) Download(_ context.Context, path string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[path]
	if !ok {
		return nil, sdk.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *wiringSink) GetObjectSize(_ context.Context, path string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.objects[path])), nil
}

// TestWithIDsReachesTheArchiveMarkerRow runs one archive pass through the
// public constructor: the marker row's security_events.id follows the host
// generator under WithIDs(counting) and is a UUIDv7 by default. The archive
// object key stays a secret either way.
func TestWithIDsReachesTheArchiveMarkerRow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		opts   func() []Option
		record func(string) bool
	}{
		{"class defaults", func() []Option { return nil }, wiringUUIDv7.MatchString},
		{"host generator", func() []Option {
			var n atomic.Int64
			return []Option{WithIDs(sdk.NewIDGenerator(func() (string, error) { return fmt.Sprintf("fixed-%d", n.Add(1)), nil }))}
		}, hasFixedPrefix},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := time.Now().Add(-500 * 24 * time.Hour)
			repo := &wiringArchiveRepo{rail: []maintenance.ArchivedEvent{
				{SecurityEvent: securityevent.SecurityEvent{ID: "old-1", EventType: securityevent.TypeLogin, EventStatus: securityevent.StatusSuccess, CreatedAt: old}},
				{SecurityEvent: securityevent.SecurityEvent{ID: "old-2", EventType: securityevent.TypeLogin, EventStatus: securityevent.StatusSuccess, CreatedAt: old.Add(time.Second)}},
			}}
			sink := &wiringSink{objects: map[string][]byte{}}
			events := &recorderEvents{}
			cfg := browserBaseConfig()
			c, err := New(testRepositories(Repositories{Maintenance: repo, SecurityEvents: events}), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
				append([]Option{
					WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
					WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}),
					WithMaintenance(MaintenanceConfig{Config: maintenance.Config{ArchiveSink: sink, ArchiveLocation: "disk:./archive"}}),
				}, tc.opts()...)...)
			if err != nil {
				t.Fatal(err)
			}
			rep, err := c.Maintenance.Pass(context.Background())
			if err != nil {
				t.Fatalf("Pass: %v (%+v)", err, rep)
			}
			if len(sink.objects) != 1 || len(events.created) != 1 {
				t.Fatalf("archived %d objects, %d marker rows; want one each", len(sink.objects), len(events.created))
			}
			if id := events.created[0].ID; !tc.record(id) {
				t.Errorf("archive marker row id %q is not record-class", id)
			}
			for key := range sink.objects {
				if hasFixedPrefix(key[strings.LastIndex(key, "/")+1:]) {
					t.Errorf("archive object key %q follows the entity generator", key)
				}
			}
		})
	}
}

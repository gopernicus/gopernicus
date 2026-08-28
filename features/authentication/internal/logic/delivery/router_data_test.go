package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// Pre-change golden output (captured on main 2026-08-28, before DataHook and the
// subject/SMS overrides existed) for the invitation and member-added purposes on
// both rails, rendered from the historical four-field data. The nil-hook /
// empty-override router must reproduce it byte-for-byte: subject and text body
// exactly, the HTML body by digest.
const (
	goldenInvitationHTMLSHA  = "63f425be0089d12653215f58049951c5345ec42bd9e3d0cbbff7919b0d0547af"
	goldenMemberAddedHTMLSHA = "0a4c85dc8693c7e47bf91e78f5305322fab79a02cdb70c66c6e892a4913c0476"
	goldenLink               = "https://app.example.test/accept?token=TOK"
)

var (
	goldenRule = strings.Repeat("=", 80)
	goldenSep  = strings.Repeat("-", 80)

	goldenInvitationText  = "\nYour Company\n\n" + goldenRule + "\n\nYou were invited to project p1 as member.\nAccept your invitation\nIf the link does not open, copy and paste this address into your browser:\n" + goldenLink + "\n\n" + goldenSep + "\n\nThis is an automated message. Please do not reply directly to this email.\n"
	goldenMemberAddedText = "\nYour Company\n\n" + goldenRule + "\n\nYou were added to project p1 as member.\nOpen it\n\n" + goldenSep + "\n\nThis is an automated message. Please do not reply directly to this email.\n"
)

// legacyData is the historical four-field invitation/member-added data.
func legacyData() map[string]any {
	return map[string]any{"ResourceType": "project", "ResourceID": "p1", "Relation": "member", "Link": goldenLink}
}

// enrichedData is the current feature-built invitation data shape (every field
// the README's per-purpose table lists), with the name-like fields empty.
func enrichedData() map[string]any {
	return map[string]any{
		"InvitationID": "inv-1", "OperationID": "inv-1",
		"ResourceType": "project", "ResourceID": "p1", "ResourceName": "", "ResourceKind": "",
		"Relation": "member", "RelationLabel": "",
		"InvitedBy": "user-1", "InviterName": "",
		"Metadata": map[string]string{"k": "v"},
		"Link":     goldenLink,
	}
}

func phoneNotifiers() map[string]notify.Notifier {
	return map[string]notify.Notifier{identity.KindPhone: &stubNotifier{kind: identity.KindPhone}}
}

func newDataRouter(t *testing.T, d Deps) *Router {
	t.Helper()
	if d.Mailer == nil {
		d.Mailer = &stubSender{}
	}
	d.MailFrom = "no-reply@example.test"
	if d.Notifiers == nil {
		d.Notifiers = phoneNotifiers()
	}
	r, err := NewRouter(d)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// A nil hook and empty override maps reproduce the pre-change output byte-for-byte
// — from the legacy four-field data AND from the enriched data with the name-like
// fields empty (the {{or .ResourceName .ResourceID}} fallback).
func TestRenderInvitationAndMemberAddedUnchangedWithoutHook(t *testing.T) {
	r := newDataRouter(t, Deps{})
	cases := []struct {
		purpose, kind, subject, body, htmlSHA string
	}{
		{PurposeInvitation, identity.KindEmail, "You have an invitation", goldenInvitationText, goldenInvitationHTMLSHA},
		{PurposeInvitation, identity.KindPhone, "", "You were invited to project p1 as member: " + goldenLink, sha("")},
		{PurposeMemberAdded, identity.KindEmail, "You were added", goldenMemberAddedText, goldenMemberAddedHTMLSHA},
		{PurposeMemberAdded, identity.KindPhone, "", "You were added to project p1 as member: " + goldenLink, sha("")},
	}
	for _, data := range []map[string]any{legacyData(), enrichedData()} {
		for _, c := range cases {
			env, err := r.Render(context.Background(), Request{Kind: c.kind, Purpose: c.purpose, Destination: "x", Secret: "TOK", Data: data})
			if err != nil {
				t.Fatalf("Render(%s/%s): %v", c.purpose, c.kind, err)
			}
			if env.Subject != c.subject {
				t.Errorf("%s/%s subject = %q, want %q", c.purpose, c.kind, env.Subject, c.subject)
			}
			if env.Body != c.body {
				t.Errorf("%s/%s body = %q, want %q", c.purpose, c.kind, env.Body, c.body)
			}
			if got := sha(env.HTML); got != c.htmlSHA {
				t.Errorf("%s/%s html sha = %s, want %s (html=%q)", c.purpose, c.kind, got, c.htmlSHA, env.HTML)
			}
		}
	}
}

// The hook receives the public purpose and a secret-free copy of the caller's
// data (Link included for context, Secret and Subject never), and a nil return
// leaves the render unchanged.
func TestRenderDataHookSeesPurposeAndSecretFreeData(t *testing.T) {
	var gotPurpose string
	var gotData map[string]any
	r := newDataRouter(t, Deps{DataHook: func(_ context.Context, purpose string, data map[string]any) (map[string]any, error) {
		gotPurpose, gotData = purpose, data
		return nil, nil
	}})
	env, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if gotPurpose != PurposeInvitation {
		t.Errorf("hook purpose = %q", gotPurpose)
	}
	if _, ok := gotData["Secret"]; ok {
		t.Errorf("hook received Secret: %v", gotData)
	}
	if _, ok := gotData["Subject"]; ok {
		t.Errorf("hook received Subject: %v", gotData)
	}
	if gotData["Link"] != goldenLink || gotData["ResourceID"] != "p1" || gotData["InvitedBy"] != "user-1" {
		t.Errorf("hook data = %v", gotData)
	}
	if env.Body != goldenInvitationText || env.Secret != "TOK" {
		t.Errorf("nil-return hook changed the render: %q", env.Body)
	}
}

// The hook's additions and replacements reach the subject/body/SMS templates,
// while the credential rendered is still the request's own Secret.
func TestRenderDataHookAddsAndReplacesFields(t *testing.T) {
	r := newDataRouter(t, Deps{DataHook: func(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
		return map[string]any{"ResourceName": "Apollo", "Relation": "owner"}, nil
	}})
	for _, kind := range []string{identity.KindEmail, identity.KindPhone} {
		env, err := r.Render(context.Background(), Request{Kind: kind, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()})
		if err != nil {
			t.Fatalf("Render(%s): %v", kind, err)
		}
		if !strings.Contains(env.Body, "You were invited to project Apollo as owner") {
			t.Errorf("%s body not enriched: %q", kind, env.Body)
		}
		if strings.Contains(env.Body, " p1 ") {
			t.Errorf("%s body still names the raw id: %q", kind, env.Body)
		}
		if !strings.Contains(env.Body, goldenLink) || env.Secret != "TOK" {
			t.Errorf("%s link/secret changed: body=%q secret=%q", kind, env.Body, env.Secret)
		}
	}
}

// Returning any reserved field fails the render with the typed invalid-input error
// and no envelope; the override never reaches a body, SMS, subject, or envelope.
func TestRenderDataHookReservedFieldsRejected(t *testing.T) {
	for _, field := range []string{"Secret", "Link", "Subject"} {
		sender := &stubSender{}
		r := newDataRouter(t, Deps{Mailer: sender, DataHook: func(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
			return map[string]any{field: "EVIL"}, nil
		}})
		for _, kind := range []string{identity.KindEmail, identity.KindPhone} {
			env, err := r.Render(context.Background(), Request{Kind: kind, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()})
			if !errors.Is(err, ErrDataHookReserved) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("%s/%s: err=%v, want ErrDataHookReserved (invalid input)", field, kind, err)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("%s/%s: error does not name the field: %v", field, kind, err)
			}
			if env != (Envelope{}) {
				t.Errorf("%s/%s: envelope built despite rejection: %+v", field, kind, env)
			}
		}
	}
}

// A hook error aborts the render before any envelope is built and is wrapped so
// the caller can match the cause.
func TestRenderDataHookErrorAbortsRender(t *testing.T) {
	r := newDataRouter(t, Deps{DataHook: func(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
		return nil, errBoom
	}})
	env, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err=%v, want errBoom", err)
	}
	if env != (Envelope{}) {
		t.Errorf("envelope built despite hook error: %+v", env)
	}
}

// The hook's input is a defensive copy — nested Metadata included — so mutating it
// (or deleting Link from it) changes neither this render nor the caller's map.
func TestRenderDataHookInputIsDefensiveCopy(t *testing.T) {
	r := newDataRouter(t, Deps{DataHook: func(_ context.Context, _ string, data map[string]any) (map[string]any, error) {
		data["ResourceName"] = "Tampered"
		delete(data, "Link")
		data["Metadata"].(map[string]string)["k"] = "tampered"
		return nil, nil
	}})
	orig := enrichedData()
	env, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: orig})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if env.Body != goldenInvitationText {
		t.Errorf("input mutation reached the render: %q", env.Body)
	}
	if orig["Metadata"].(map[string]string)["k"] != "v" || orig["Link"] != goldenLink {
		t.Errorf("input mutation reached the caller's data: %v", orig)
	}
}

// A caller-supplied Data["Secret"] never wins over Request.Secret: the rendered
// credential is always the one the sealed envelope carries.
func TestRenderRequestSecretIsAuthoritative(t *testing.T) {
	var sawSecret bool
	r := newDataRouter(t, Deps{DataHook: func(_ context.Context, _ string, data map[string]any) (map[string]any, error) {
		_, sawSecret = data["Secret"]
		return nil, nil
	}})
	env, err := r.Render(context.Background(), Request{Kind: identity.KindPhone, Purpose: PurposeLoginCode, Destination: "x", Secret: "654321", Data: map[string]any{"Secret": "EVIL"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if sawSecret {
		t.Errorf("hook saw the caller-supplied Secret")
	}
	if env.Body != "Your sign-in code is 654321" || env.Secret != "654321" {
		t.Errorf("rendered credential drifted: body=%q secret=%q", env.Body, env.Secret)
	}
}

// The hook runs for every purpose on both rails, never seeing a Secret.
func TestRenderDataHookRunsForEveryPurpose(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	r := newDataRouter(t, Deps{DataHook: func(_ context.Context, purpose string, data map[string]any) (map[string]any, error) {
		if _, ok := data["Secret"]; ok {
			t.Errorf("%s: hook received Secret", purpose)
		}
		mu.Lock()
		seen[purpose]++
		mu.Unlock()
		return nil, nil
	}})
	data := map[string]any{"ProviderName": "GitHub", "IdentifierKind": "email", "Link": goldenLink, "ResourceType": "project", "ResourceID": "p1", "Relation": "member"}
	for purpose, sp := range specs {
		if _, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: purpose, Destination: "x", Secret: "S", Data: data}); err != nil {
			t.Fatalf("Render(%s/email): %v", purpose, err)
		}
		if sp.sms == "" {
			continue
		}
		if _, err := r.Render(context.Background(), Request{Kind: identity.KindPhone, Purpose: purpose, Destination: "x", Secret: "S", Data: data}); err != nil {
			t.Fatalf("Render(%s/phone): %v", purpose, err)
		}
	}
	for purpose, sp := range specs {
		want := 1
		if sp.sms != "" {
			want = 2
		}
		if seen[purpose] != want {
			t.Errorf("%s: hook ran %d times, want %d", purpose, seen[purpose], want)
		}
	}
}

// Concurrent renders through one hook share no map: each result carries only
// its own enrichment (run under -race).
func TestRenderDataHookConcurrent(t *testing.T) {
	r := newDataRouter(t, Deps{DataHook: func(_ context.Context, _ string, data map[string]any) (map[string]any, error) {
		return map[string]any{"ResourceName": "Res-" + data["ResourceID"].(string)}, nil
	}})
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := enrichedData()
			data["ResourceID"] = fmt.Sprint(i)
			env, err := r.Render(context.Background(), Request{Kind: identity.KindPhone, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: data})
			if err != nil {
				errs <- err
				return
			}
			if want := fmt.Sprintf("project Res-%d as member", i); !strings.Contains(env.Body, want) {
				errs <- fmt.Errorf("render %d: body=%q, want %q", i, env.Body, want)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// A subject override wins for its purpose only, renders against the hook-enriched
// data, and leaves every other subject untouched.
func TestRenderSubjectOverridePrecedence(t *testing.T) {
	r := newDataRouter(t, Deps{
		Subjects: map[string]string{PurposeInvitation: "Join {{.ResourceName}} as {{.Relation}}"},
		DataHook: func(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
			return map[string]any{"ResourceName": "Apollo"}, nil
		},
	})
	env, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if env.Subject != "Join Apollo as member" {
		t.Errorf("subject = %q", env.Subject)
	}
	if !strings.Contains(env.HTML, "Join Apollo as member") {
		t.Errorf("layout did not receive the overridden subject: %q", env.HTML)
	}
	other, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: PurposeRegistrationVerification, Destination: "x", Secret: "123456"})
	if err != nil {
		t.Fatalf("Render(verification): %v", err)
	}
	if other.Subject != "Verify your email" {
		t.Errorf("unrelated subject changed: %q", other.Subject)
	}
}

// An SMS override wins on the body-only rail for its purpose only; the email
// body for the same purpose is untouched.
func TestRenderSMSOverridePrecedence(t *testing.T) {
	r := newDataRouter(t, Deps{SMSBodies: map[string]string{PurposeInvitation: "Join {{.ResourceType}} {{.ResourceID}}: {{.Link}}"}})
	sms, err := r.Render(context.Background(), Request{Kind: identity.KindPhone, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()})
	if err != nil {
		t.Fatalf("Render(sms): %v", err)
	}
	if sms.Body != "Join project p1: "+goldenLink {
		t.Errorf("sms body = %q", sms.Body)
	}
	mail, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()})
	if err != nil {
		t.Fatalf("Render(email): %v", err)
	}
	if mail.Body != goldenInvitationText {
		t.Errorf("email body changed by an SMS override: %q", mail.Body)
	}
	other, err := r.Render(context.Background(), Request{Kind: identity.KindPhone, Purpose: PurposeMemberAdded, Destination: "x", Secret: "TOK", Data: enrichedData()})
	if err != nil {
		t.Fatalf("Render(member_added sms): %v", err)
	}
	if other.Body != "You were added to project p1 as member: "+goldenLink {
		t.Errorf("unrelated sms changed: %q", other.Body)
	}
}

// Every construction-time override rejection is ErrOverrideInvalid (invalid input)
// with a message naming the cause.
func TestNewRouterOverrideRejections(t *testing.T) {
	cases := []struct {
		name string
		deps Deps
		want string
	}{
		{"subject unknown purpose", Deps{Subjects: map[string]string{"nope": "x"}}, `unknown purpose "nope"`},
		{"sms unknown purpose", Deps{SMSBodies: map[string]string{"nope": "x"}}, `unknown purpose "nope"`},
		{"subject empty", Deps{Subjects: map[string]string{PurposeInvitation: "  \n"}}, "is empty"},
		{"sms empty", Deps{SMSBodies: map[string]string{PurposeInvitation: ""}}, "is empty"},
		{"sms for email-only purpose", Deps{SMSBodies: map[string]string{PurposePasswordReset: "Reset: {{.Link}}"}}, "email-only purpose"},
		{"subject parse failure", Deps{Subjects: map[string]string{PurposeInvitation: "{{.Unclosed"}}, "parse subject override"},
		{"sms parse failure", Deps{SMSBodies: map[string]string{PurposeInvitation: "{{if}}"}}, "parse sms override"},
	}
	for _, c := range cases {
		c.deps.Mailer = &stubSender{}
		_, err := NewRouter(c.deps)
		if !errors.Is(err, ErrOverrideInvalid) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err=%v, want ErrOverrideInvalid (invalid input)", c.name, err)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%q does not mention %q", c.name, err, c.want)
		}
	}
}

// An override naming a field the data does not carry fails the render (missing-key
// errors are on) instead of shipping "<no value>".
func TestRenderOverrideMissingKeyFails(t *testing.T) {
	r := newDataRouter(t, Deps{
		Subjects:  map[string]string{PurposeInvitation: "Join {{.Nope}}"},
		SMSBodies: map[string]string{PurposeMemberAdded: "Added: {{.Nope}}"},
	})
	if _, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()}); err == nil || !strings.Contains(err.Error(), "Nope") {
		t.Errorf("subject with missing key: err=%v, want a missing-key failure", err)
	}
	if _, err := r.Render(context.Background(), Request{Kind: identity.KindPhone, Purpose: PurposeMemberAdded, Destination: "x", Secret: "TOK", Data: enrichedData()}); err == nil || !strings.Contains(err.Error(), "Nope") {
		t.Errorf("sms with missing key: err=%v, want a missing-key failure", err)
	}
}

// A rendered subject that is empty, or that carries a CR/LF, is ErrSubjectInvalid
// before any envelope is built — on the email rail, where a subject exists.
func TestRenderSubjectEmptyOrLineBreakRejected(t *testing.T) {
	cases := []struct {
		name, src, resourceName string
	}{
		{"empty", "{{.ResourceName}}", ""},
		{"whitespace", " {{.ResourceName}} ", "  "},
		{"LF", "Join {{.ResourceName}}", "a\nb"},
		{"CR", "Join {{.ResourceName}}", "a\rb"},
	}
	for _, c := range cases {
		name := c.resourceName
		r := newDataRouter(t, Deps{
			Subjects: map[string]string{PurposeInvitation: c.src},
			DataHook: func(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
				return map[string]any{"ResourceName": name}, nil
			},
		})
		env, err := r.Render(context.Background(), Request{Kind: identity.KindEmail, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()})
		if !errors.Is(err, ErrSubjectInvalid) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err=%v, want ErrSubjectInvalid (invalid input)", c.name, err)
		}
		if env != (Envelope{}) {
			t.Errorf("%s: envelope built despite invalid subject: %+v", c.name, env)
		}
	}
}

// The subject template is an email-rail concern: an SMS render never executes it,
// so a subject override that would fail cannot break the body-only rail.
func TestRenderSMSIgnoresSubjectTemplate(t *testing.T) {
	r := newDataRouter(t, Deps{Subjects: map[string]string{PurposeInvitation: "{{.Nope}}"}})
	env, err := r.Render(context.Background(), Request{Kind: identity.KindPhone, Purpose: PurposeInvitation, Destination: "x", Secret: "TOK", Data: enrichedData()})
	if err != nil {
		t.Fatalf("Render(sms): %v", err)
	}
	if env.Subject != "" || env.Body != "You were invited to project p1 as member: "+goldenLink {
		t.Errorf("sms envelope = %+v", env)
	}
}

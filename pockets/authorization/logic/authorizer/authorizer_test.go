package authorizer_test

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/authorizer"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauthorization "github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

func service(t *testing.T) *decisions.Service {
	t.Helper()
	actors := []decisions.SubjectTypeRef{{Type: "user"}}
	store := memory.NewTuples()
	root := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "root"}}
	if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{root}}); err != nil {
		t.Fatal(err)
	}
	svc, err := decisions.NewService(store, decisions.WithModel(decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "platform", Def: decisions.ResourceTypeDef{
			Relations:   map[string]decisions.RelationDef{"role_admin": {AllowedSubjects: actors}, "admin": {AllowedSubjects: actors}},
			Permissions: map[string]decisions.Expression{"manage": decisions.Role("admin")},
		}},
	})))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestConstruction(t *testing.T) {
	if _, err := authorizer.New(nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil service = %v", err)
	}
	svc := service(t)
	if _, err := authorizer.New(svc, authorizer.WithGlobalResource("platform", "global", nil)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil relationFor = %v", err)
	}
	if _, err := authorizer.New(svc, authorizer.WithGlobalResource("platform", "", func(l string) string { return l })); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty global resource id = %v", err)
	}
	if _, err := authorizer.New(svc, authorizer.WithGlobalResource("platform", "a.b", func(l string) string { return l })); err != nil {
		t.Fatalf("global resource id outside the portable grammar = %v; the engine accepts what the pocket accepts", err)
	}
	// Both the label "admin" (a model Role label) and "role_admin" (a declared
	// relation) would map to role_admin.
	ambiguous := func(label string) string {
		if label == "admin" || label == "role_admin" {
			return "role_admin"
		}
		return label
	}
	if _, err := authorizer.New(svc, authorizer.WithGlobalResource("platform", "global", ambiguous)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ambiguous mapping = %v", err)
	}
	e, err := authorizer.New(svc, authorizer.WithGlobalResource("platform", "global", func(l string) string { return "role_" + l }))
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Limits(); got.MaxCheckBatch != svc.Limits().MaxBatchSize || got.MaxLookupPage != svc.Limits().MaxLookupResults {
		t.Fatalf("Limits = %+v", got)
	}
}

func TestUnrestrictedIsUnavailableNotRetryable(t *testing.T) {
	if !errors.Is(authorizer.ErrUnrestricted, sdk.ErrUnavailable) {
		t.Fatal("ErrUnrestricted must wrap sdk.ErrUnavailable")
	}
	e, err := authorizer.New(service(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	admin := sdkauthorization.Principal{Type: "user", ID: "root"}
	_, err = e.LookupResources(ctx, sdkauthorization.LookupRequest{Principal: admin, Permission: "manage", ResourceType: "platform"})
	if !errors.Is(err, authorizer.ErrUnrestricted) {
		t.Fatalf("universal grant lookup = %v, want ErrUnrestricted", err)
	}
	d, err := e.Check(ctx, sdkauthorization.CheckRequest{Principal: admin, Permission: "manage", Resource: sdkauthorization.Resource{Type: "platform", ID: "any"}})
	if err != nil || !d.Allowed {
		t.Fatalf("universal grant check = %v, %v; want allowed", d, err)
	}
}

// D6': the engine accepts through the protocol whatever the pocket accepts.
func TestEngineAcceptsThePocketGrammar(t *testing.T) {
	e, err := authorizer.New(service(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	req := sdkauthorization.CheckRequest{Principal: sdkauthorization.Principal{Type: "user", ID: "a@b.c"}, Permission: "manage", Resource: sdkauthorization.Resource{Type: "platform", ID: "org.dot"}}
	if sdkauthorization.ValidatePortable(req) == nil {
		t.Fatal("request must be outside the portable grammar")
	}
	if d, err := e.Check(ctx, req); err != nil || d.Allowed {
		t.Fatalf("Check outside the portable grammar = %v, %v; want a plain deny", d, err)
	}
	if _, err := e.LookupResources(ctx, sdkauthorization.LookupRequest{Principal: req.Principal, Permission: "manage", ResourceType: "platform"}); err != nil {
		t.Fatalf("LookupResources outside the portable grammar = %v", err)
	}
	req.Resource.ID = "a\x01b"
	if _, err := e.Check(ctx, req); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Check outside the pocket grammar = %v, want sdk.ErrInvalidInput", err)
	}
}

package main

import (
	"context"
	"crypto/rand"
	"net/http"

	inbound "github.com/gopernicus/gopernicus/examples/auth-cms/internal/inbound/domains/documents"
	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	outbound "github.com/gopernicus/gopernicus/examples/auth-cms/internal/outbound/domains/documents"
	audit "github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	decisions "github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The host picks its listing strategy once. The ordinary domain/HTTP operation
// stays ListVisible; neither layer assembles permission IDs or SQL predicates.
func registerDocumentRoutes(ctx context.Context, router *web.WebHandler, authenticate web.Middleware, authorizer *decisions.Service, system *mutations.SystemMutator) error {
	ctx = audit.WithSource(ctx, audit.Source{System: "demo-document-seed"})
	documents := []domain.Document{
		{ID: "document-90", TenantID: "demo", Name: "Alpha"},
		{ID: "document-20", TenantID: "demo", Name: "Beta"},
		{ID: "document-10", TenantID: "demo", Name: "Zulu"},
	}
	for _, doc := range documents {
		if _, err := system.GrantRelationship(ctx, mutations.GrantRelationshipCommand{

			ResourceType: "document", ResourceID: doc.ID, Relation: "viewer", Subject: seedOwnerSubject,
		}); err != nil {
			return err
		}
	}
	// This proof host's documents and cursors are ephemeral. A persistent host
	// supplies a stable shared secret from its configuration instead of this key.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	codec, err := outbound.NewCursorCodec(key)
	if err != nil {
		return err
	}
	bypass := func(ctx context.Context, principal sdk.Principal) (bool, error) {
		result, err := authorizer.Check(ctx, model.CheckRequest{
			Principal: model.PrincipalFrom(principal), Permission: "admin",
			Resource: model.Resource{Type: platformResourceType, ID: platformResourceID},
		})
		return result.Allowed, err
	}
	store, err := outbound.NewMemory(documents)
	if err != nil {
		return err
	}
	lister, err := outbound.NewListing(store, authorizer, codec, outbound.Candidates, 0, bypass)
	if err != nil {
		return err
	}
	service, err := domain.New(lister)
	if err != nil {
		return err
	}
	router.Handle(http.MethodGet, "/demo/tenants/{tenant}/documents", inbound.Handler(service), authenticate)
	return nil
}

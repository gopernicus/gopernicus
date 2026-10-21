package documents

import (
	"fmt"
	"slices"
	"testing"

	inbound "github.com/gopernicus/gopernicus/examples/auth-cms/internal/inbound/domains/documents"
	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

func testProtocolListing(t *testing.T, reader domain.Reader, az authorization.Components, batch int, bypass inbound.Bypass) *inbound.ProtocolListing {
	t.Helper()
	service, err := domain.New(reader)
	if err != nil {
		t.Fatal(err)
	}
	l, err := inbound.NewProtocolListing(service, az.Authorizer, testCodec(t), batch, bypass)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestProtocolListingHTTP(t *testing.T) {
	store := newMemoryAuthorizationStore()
	grant(t, store, "01", "90", "30", "20", "other", "unicode")
	az := testAuthorizer(t, store, model.EvaluationLimits{})
	verifyListingHTTP(t, testProtocolListing(t, testMemory(t, orderedFixture()), az, 3, nil))
}

// The listing filtered through the protocol matches the native FilterPage
// listing page by page: items, continuation and scan flags.
func TestProtocolListingMatchesNativeCandidates(t *testing.T) {
	docs, ids := largeFixture(120)
	store := newMemoryAuthorizationStore()
	var permitted []string
	for i, id := range ids {
		if i%3 != 0 {
			permitted = append(permitted, id)
		}
	}
	grant(t, store, permitted...)
	reader := testMemory(t, append(docs, orderedFixture()...))
	az := testAuthorizer(t, store, model.EvaluationLimits{})
	for _, batch := range []int{0, 1, 7} {
		native := testListing(t, reader, az, inbound.Candidates, batch, nil)
		protocol := testProtocolListing(t, reader, az, batch, nil)
		for _, query := range []inbound.Query{
			{TenantID: "a", Limit: 1},
			{TenantID: "a", Limit: 5},
			{TenantID: "a", Limit: 7, Desc: true},
			{TenantID: "a", Limit: 3, Search: "document 00001"},
			{TenantID: "b", Limit: 2},
		} {
			for _, principal := range []sdk.Principal{alice, {Type: "user", ID: "nobody"}} {
				name := fmt.Sprintf("batch=%d/%+v/%s", batch, query, principal.ID)
				nativeQuery, protocolQuery := query, query
				for page := 0; ; page++ {
					want, err := native.ListVisible(t.Context(), principal, nativeQuery)
					if err != nil {
						t.Fatalf("%s native: %v", name, err)
					}
					got, err := protocol.ListVisible(t.Context(), principal, protocolQuery)
					if err != nil {
						t.Fatalf("%s protocol: %v", name, err)
					}
					if !slices.Equal(documentIDs(got.Items), documentIDs(want.Items)) || got.HasMore != want.HasMore ||
						got.ScanLimitReached != want.ScanLimitReached || (got.NextCursor == "") != (want.NextCursor == "") {
						t.Fatalf("%s page %d: protocol %+v, native %+v", name, page, got, want)
					}
					if !want.HasMore {
						break
					}
					nativeQuery.Cursor, protocolQuery.Cursor = want.NextCursor, got.NextCursor
				}
			}
		}
	}
}

package documents

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/examples/auth-cms/internal/inbound/domains/documents"
	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	decisions "github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	relationships "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

var alice = sdk.Principal{Type: "user", ID: "alice"}

func documentModel() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{{Name: "document", Def: relationships.ResourceTypeDef{
		Relations:   map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
		Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))},
	}}})
}

func testCodec(t *testing.T) *CursorCodec {
	t.Helper()
	c, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testAuthorizer(t *testing.T, store relationships.Storer, limits model.EvaluationLimits) authorization.Components {
	t.Helper()
	c, err := authorization.New(authorization.Repositories{Relationships: store}, authorization.WithRelationshipModel(documentModel()), authorization.WithLimits(limits))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func grant(t *testing.T, store relationships.Storer, ids ...string) {
	t.Helper()
	rows := make([]relationships.CreateRelationship, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, relationships.CreateRelationship{ResourceType: "document", ResourceID: id, Relation: "viewer", SubjectType: alice.Type, SubjectID: alice.ID})
	}
	if err := store.CreateRelationships(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
}

func testListing(t *testing.T, reader Reader, az authorization.Components, strategy Strategy, batch int, bypass Bypass) *Listing {
	t.Helper()
	l, err := NewListing(reader, az.Decisions, testCodec(t), strategy, batch, bypass)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func documentIDs(items []domain.Document) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

// Real HTTP exercises the handler, domain port, adapter, permission engine and
// repository. The fixture middleware supplies a known identity; it is not shipped
// host authentication or an impersonation route.
func listingServer(t *testing.T, lister domain.Lister) *httptest.Server {
	t.Helper()
	service, err := domain.New(lister)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /tenants/{tenant}/documents", inbound.Handler(service))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get("Fixture-Principal"); id != "" {
			r = r.WithContext(sdk.WithPrincipal(r.Context(), sdk.Principal{Type: "user", ID: id}))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func getPage(t *testing.T, server *httptest.Server, principal string, query domain.Query, wantStatus int) domain.Page {
	t.Helper()
	values := url.Values{"q": {query.Search}, "limit": {fmt.Sprint(query.Limit)}, "cursor": {query.Cursor}}
	if query.Desc {
		values.Set("sort", "-name")
	}
	req, err := http.NewRequest(http.MethodGet, server.URL+"/tenants/"+url.PathEscape(query.TenantID)+"/documents?"+values.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Fixture-Principal", principal)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("status %d, want %d: %s", response.StatusCode, wantStatus, body)
	}
	if wantStatus != http.StatusOK {
		return domain.Page{}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"total", "total_count", "prev_cursor", "has_prev", "facets"} {
		if _, ok := fields[name]; ok {
			t.Fatalf("unproved listing metadata %s: %s", name, body)
		}
	}
	var page domain.Page
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if page.Items == nil {
		t.Fatalf("items must be an array: %s", body)
	}
	if page.HasMore && page.NextCursor == "" {
		t.Fatalf("continuation without cursor: %s", body)
	}
	return page
}

func walkHTTP(t *testing.T, server *httptest.Server, principal string, query domain.Query) []string {
	t.Helper()
	ids := make([]string, 0)
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < 2000; pageNumber++ {
		page := getPage(t, server, principal, query, http.StatusOK)
		for _, id := range documentIDs(page.Items) {
			if seen[id] {
				t.Fatalf("duplicate %q", id)
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if !page.HasMore {
			return ids
		}
		if page.NextCursor == query.Cursor {
			t.Fatal("cursor did not progress")
		}
		query.Cursor = page.NextCursor
	}
	t.Fatal("pagination did not terminate")
	return nil
}

func orderedFixture() []domain.Document {
	return []domain.Document{
		{ID: "01", TenantID: "a", Name: "Zulu"},
		{ID: "90", TenantID: "a", Name: "Alpha"},
		{ID: "30", TenantID: "a", Name: "BETA"},
		{ID: "20", TenantID: "a", Name: "beta"},
		{ID: "denied", TenantID: "a", Name: "Aardvark secret"},
		{ID: "other", TenantID: "b", Name: "Alpha"},
		{ID: "unicode", TenantID: "a", Name: "Éclair"},
	}
}

func verifyListingHTTP(t *testing.T, lister domain.Lister) {
	t.Helper()
	server := listingServer(t, lister)
	for _, tt := range []struct {
		query domain.Query
		want  []string
	}{
		{domain.Query{TenantID: "a", Limit: 2}, []string{"90", "20", "30", "01", "unicode"}},
		{domain.Query{TenantID: "a", Limit: 2, Desc: true}, []string{"unicode", "01", "30", "20", "90"}},
		{domain.Query{TenantID: "a", Limit: 1, Search: " BETA "}, []string{"20", "30"}},
		{domain.Query{TenantID: "b", Limit: 1}, []string{"other"}},
	} {
		if got := walkHTTP(t, server, alice.ID, tt.query); !slices.Equal(got, tt.want) {
			t.Fatalf("query %+v: %v, want %v", tt.query, got, tt.want)
		}
	}
	query := domain.Query{TenantID: "a", Limit: 2}
	getPage(t, server, "", query, http.StatusUnauthorized)
	if got := walkHTTP(t, server, "nobody", query); len(got) != 0 {
		t.Fatalf("no grants: %v", got)
	}
	page := getPage(t, server, alice.ID, query, http.StatusOK)
	query.Cursor = page.NextCursor
	for _, changed := range []domain.Query{
		{TenantID: "b", Limit: 2, Cursor: query.Cursor},
		{TenantID: "a", Search: "beta", Limit: 2, Cursor: query.Cursor},
		{TenantID: "a", Desc: true, Limit: 2, Cursor: query.Cursor},
		{TenantID: "a", Limit: 2, Cursor: query.Cursor + "!"},
	} {
		getPage(t, server, alice.ID, changed, http.StatusBadRequest)
	}
	getPage(t, server, "nobody", query, http.StatusBadRequest)
	query.Limit = 1 // Changing page size is intentionally allowed.
	getPage(t, server, alice.ID, query, http.StatusOK)
}

func TestMemoryListingHTTP(t *testing.T) {
	store := memory.New().Relationships()
	grant(t, store, "01", "90", "30", "20", "other", "unicode")
	az := testAuthorizer(t, store, model.EvaluationLimits{})
	for _, strategy := range []Strategy{CompleteSet, Candidates} {
		t.Run(string(strategy), func(t *testing.T) {
			verifyListingHTTP(t, testListing(t, testMemory(t, orderedFixture()), az, strategy, 3, nil))
		})
	}
}

func TestListingSparseContinuationIsPrivate(t *testing.T) {
	store := memory.New().Relationships()
	grant(t, store, "visible")
	az := testAuthorizer(t, store, model.EvaluationLimits{MaxFilterScan: 2})
	reader := testMemory(t, []domain.Document{
		{ID: "secret-one", TenantID: "a", Name: "aaa"}, {ID: "secret-two", TenantID: "a", Name: "bbb"},
		{ID: "secret-three", TenantID: "a", Name: "ccc"}, {ID: "visible", TenantID: "a", Name: "ddd"},
	})
	server := listingServer(t, testListing(t, reader, az, Candidates, 2, nil))
	query := domain.Query{TenantID: "a", Limit: 2}
	page := getPage(t, server, alice.ID, query, http.StatusOK)
	if len(page.Items) != 0 || !page.HasMore || !page.ScanLimitReached {
		t.Fatalf("expected resumable empty page: %+v", page)
	}
	raw, err := base64.RawURLEncoding.DecodeString(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-two") || strings.Contains(string(raw), "bbb") {
		t.Fatal("denied candidate exposed in cursor")
	}
	query.Cursor = page.NextCursor
	page = getPage(t, server, alice.ID, query, http.StatusOK)
	if !slices.Equal(documentIDs(page.Items), []string{"visible"}) || page.HasMore || page.ScanLimitReached {
		t.Fatalf("resume: %+v", page)
	}
}

func TestListingBypassPreservesBusinessQueryAndErrors(t *testing.T) {
	store := memory.New().Relationships()
	az := testAuthorizer(t, store, model.EvaluationLimits{})
	for _, strategy := range []Strategy{CompleteSet, Candidates} {
		t.Run(string(strategy), func(t *testing.T) {
			bypass := func(context.Context, sdk.Principal) (bool, error) { return true, nil }
			server := listingServer(t, testListing(t, testMemory(t, orderedFixture()), az, strategy, 0, bypass))
			got := walkHTTP(t, server, alice.ID, domain.Query{TenantID: "a", Search: "beta", Limit: 1})
			if !slices.Equal(got, []string{"20", "30"}) {
				t.Fatalf("bypass removed business scope: %v", got)
			}
			bypassFailure := errors.New("policy backend unavailable")
			bad := testListing(t, testMemory(t, orderedFixture()), az, strategy, 0, func(context.Context, sdk.Principal) (bool, error) { return false, bypassFailure })
			getPage(t, listingServer(t, bad), alice.ID, domain.Query{TenantID: "a", Limit: 2}, http.StatusInternalServerError)
		})
	}
}

type failedReader struct{ err error }

func (r failedReader) read(context.Context, domain.Query, position, int, decisions.ResourceSet) ([]row, bool, error) {
	return nil, false, r.err
}

func TestListingDoesNotTurnStorageFailureIntoEmptySuccess(t *testing.T) {
	store := memory.New().Relationships()
	grant(t, store, "01")
	az := testAuthorizer(t, store, model.EvaluationLimits{})
	for _, strategy := range []Strategy{CompleteSet, Candidates} {
		server := listingServer(t, testListing(t, failedReader{errors.New("offline")}, az, strategy, 0, nil))
		getPage(t, server, alice.ID, domain.Query{TenantID: "a", Limit: 2}, http.StatusInternalServerError)
	}
}

func TestListingCompleteSetOverflowDoesNotUsePartialIDs(t *testing.T) {
	store := memory.New().Relationships()
	docs, ids := largeFixture(1005)
	grant(t, store, ids...)
	az := testAuthorizer(t, store, model.EvaluationLimits{})
	reader := testMemory(t, docs)
	complete := testListing(t, reader, az, CompleteSet, 0, nil)
	page, err := complete.ListVisible(t.Context(), alice, domain.Query{TenantID: "a", Limit: 50})
	if !errors.Is(err, model.ErrEvaluationLimit) || len(page.Items) != 0 {
		t.Fatalf("overflow: %+v, %v", page, err)
	}
	candidates := testListing(t, reader, az, Candidates, 0, nil)
	got := walkHTTP(t, listingServer(t, candidates), alice.ID, domain.Query{TenantID: "a", Limit: 50})
	slices.Reverse(ids)
	if !slices.Equal(got, ids) {
		t.Fatalf("candidate order/count differs: got %d IDs, want %d", len(got), len(ids))
	}
}

func largeFixture(n int) ([]domain.Document, []string) {
	docs, ids := make([]domain.Document, 0, n), make([]string, 0, n)
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("%06d", i)
		ids = append(ids, id)
		docs = append(docs, domain.Document{ID: id, TenantID: "a", Name: fmt.Sprintf("Document %06d", n+1-i)})
	}
	return docs, ids
}

type countedReader struct {
	Reader
	calls    int
	returned int
}

func (r *countedReader) read(ctx context.Context, q domain.Query, p position, limit int, filter decisions.ResourceSet) ([]row, bool, error) {
	r.calls++
	rows, more, err := r.Reader.read(ctx, q, p, limit, filter)
	r.returned += len(rows)
	return rows, more, err
}

func TestCandidatePullMeasurements(t *testing.T) {
	for _, divisor := range []int{1, 2, 100} {
		t.Run(fmt.Sprintf("one_in_%d", divisor), func(t *testing.T) {
			docs, ids := largeFixture(1000)
			store := memory.New().Relationships()
			var permitted []string
			for i, id := range ids {
				if (i+1)%divisor == 0 {
					permitted = append(permitted, id)
				}
			}
			grant(t, store, permitted...)
			az := testAuthorizer(t, store, model.EvaluationLimits{})
			var want []string
			for _, batch := range []int{0, 20} {
				reader := &countedReader{Reader: testMemory(t, docs)}
				page, err := testListing(t, reader, az, Candidates, batch, nil).ListVisible(t.Context(), alice, domain.Query{TenantID: "a", Limit: 10})
				if err != nil {
					t.Fatal(err)
				}
				if want == nil {
					want = documentIDs(page.Items)
				}
				if len(page.Items) != 10 || !slices.Equal(documentIDs(page.Items), want) {
					t.Fatalf("pull policy changed visible page: %+v", page)
				}
				t.Logf("batch=%d: source calls=%d; candidates returned=%d; visible=%d", batch, reader.calls, reader.returned, len(page.Items))
			}
		})
	}
}

func testMemory(t *testing.T, docs []domain.Document) *Memory {
	t.Helper()
	store, err := NewMemory(docs)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestDocumentValuesCannotProduceUnusableCursor(t *testing.T) {
	codec := testCodec(t)
	query := domain.Query{TenantID: "a", Limit: 1}
	// Quotes and backslashes exercise worst-case JSON expansion within the bound.
	doc := domain.Document{ID: strings.Repeat("x", 128), TenantID: "a", Name: strings.Repeat("\"\\", 256)}
	if _, err := NewMemory([]domain.Document{doc}); err != nil {
		t.Fatal(err)
	}
	token, err := codec.encode(position{NameKey: strings.ToLower(doc.Name), ID: doc.ID}, cursorBinding(alice, query))
	if err != nil {
		t.Fatal(err)
	}
	query.Cursor = token
	if err := query.Normalize(); err != nil {
		t.Fatalf("self-generated cursor refused: %v", err)
	}
	if _, err := codec.decode(token, cursorBinding(alice, query)); err != nil {
		t.Fatal(err)
	}
	doc.Name = strings.Repeat("x", 4000)
	if _, err := NewMemory([]domain.Document{doc}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unbounded sort key accepted: %v", err)
	}
	query.Search = string([]byte{0xff})
	if err := query.Normalize(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid UTF-8 normalized into accepted search: %v", err)
	}
}

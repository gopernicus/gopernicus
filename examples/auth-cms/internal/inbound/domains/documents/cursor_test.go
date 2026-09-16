package documents

import (
	"errors"
	"strings"
	"testing"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestDocumentValuesCannotProduceUnusableCursor(t *testing.T) {
	codec, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	query := Query{TenantID: "a", Limit: 1}
	// Quotes and backslashes exercise worst-case JSON expansion within the bound.
	doc := domain.Document{ID: strings.Repeat("x", 128), TenantID: "a", Name: strings.Repeat("\"\\", 256)}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	token, err := codec.encode(domain.Position{NameKey: strings.ToLower(doc.Name), ID: doc.ID}, cursorBinding(sdk.Principal{Type: "user", ID: "alice"}, query))
	if err != nil {
		t.Fatal(err)
	}
	query.Cursor = token
	if err := query.normalize(); err != nil {
		t.Fatalf("self-generated cursor refused: %v", err)
	}
	if _, err := codec.decode(token, cursorBinding(sdk.Principal{Type: "user", ID: "alice"}, query)); err != nil {
		t.Fatal(err)
	}
	doc.Name = strings.Repeat("x", 4000)
	if err := doc.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unbounded sort key accepted: %v", err)
	}
	query.Search = string([]byte{0xff})
	if err := query.normalize(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid UTF-8 normalized into accepted search: %v", err)
	}
}

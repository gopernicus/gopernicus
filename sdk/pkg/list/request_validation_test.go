package list

import (
	"errors"
	"net/url"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestParseRejectsUnknownResolvedStrategy(t *testing.T) {
	if _, err := ParseRequest(Params{DefaultStrategy: "sideways"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("raw parser error = %v", err)
	}
	if _, err := ParseQuery(url.Values{}, QueryOptions{DefaultStrategy: "sideways"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("query parser error = %v", err)
	}
	// Explicit supported input can override the host default.
	req, err := ParseRequest(Params{DefaultStrategy: "sideways", Offset: "0"})
	if err != nil || req.Strategy != StrategyOffset {
		t.Fatalf("explicit strategy: %+v %v", req, err)
	}
}

func TestMarkPrevPageInclusiveBoundary(t *testing.T) {
	var page Page[string]
	if err := MarkPrevPage(&page, []string{"e1"}, 1, encTest); err != nil {
		t.Fatal(err)
	}
	if !page.HasPrev || page.PreviousCursor != "" {
		t.Fatalf("page two: %+v", page)
	}
	if err := MarkPrevPage(&page, []string{"e1", "e2"}, 1, encTest); err != nil {
		t.Fatal(err)
	}
	if !page.HasPrev || page.PreviousCursor != "enc_e1" {
		t.Fatalf("page three: %+v", page)
	}
	if err := MarkPrevPage(&page, nil, 1, encTest); err != nil {
		t.Fatal(err)
	}
	if page.HasPrev || page.PreviousCursor != "" {
		t.Fatalf("empty probe: %+v", page)
	}
}

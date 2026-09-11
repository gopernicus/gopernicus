package authorizationhttp

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStrictJSONBodyBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, body string
		limit      int64
		want       int
		ok         bool
	}{
		{"null preserved", "null", 16, 200, true},
		{"exact limit", "{}" + strings.Repeat(" ", 14), 16, 200, true},
		{"trailing whitespace over limit", "{}" + strings.Repeat(" ", 32), 16, 413, false},
		{"trailing object", "{}{}", 16, 400, false},
		{"unknown field", `{"unknown":true}`, 32, 400, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/", strings.NewReader(test.body))
			var dst struct{}
			ok := strictJSONBody(w, r, &dst, test.limit)
			if ok != test.ok || w.Code != test.want {
				t.Fatalf("ok=%v status=%d body=%s", ok, w.Code, w.Body.String())
			}
		})
	}
}

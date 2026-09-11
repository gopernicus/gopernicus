package authenticationhttp

import (
	"strings"
	"testing"
)

func TestRegistrationJSONBodyBoundary(t *testing.T) {
	const valid = `{"email":"bounded@example.com","password":"password123456789","display_name":"Bounded"}`
	for _, test := range []struct {
		name, suffix string
		want         int
	}{
		{"trailing object", "{}", 400},
		{"trailing closer", "}", 400},
		{"oversized trailing whitespace", strings.Repeat(" ", maxJSONBodyBytes), 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHTMLTestHandler(t, nil)
			bad := doJSONReq(t, h, "/auth/register", valid+test.suffix)
			if bad.Code != test.want {
				t.Fatalf("invalid registration=%d body=%s", bad.Code, bad.Body.String())
			}
			good := doJSONReq(t, h, "/auth/register", valid)
			if good.Code != 201 {
				t.Fatalf("valid registration=%d; rejected body may have mutated state: %s", good.Code, good.Body.String())
			}
		})
	}
}

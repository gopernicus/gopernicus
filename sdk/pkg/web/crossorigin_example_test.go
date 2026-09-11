package web

// The ratified host HTML cross-origin posture (ARCHITECTURE.md, "Host HTML
// cross-origin posture") is direct stdlib use: a host constructs
// http.CrossOriginProtection itself and mounts its Handler method on the
// browser-HTML route groups. There is deliberately no sdk wrapper —
// (*http.CrossOriginProtection).Handler already structurally satisfies
// Middleware. This file is the compiled reference for the canonical
// construction and pins the accept/reject table the posture documents.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ExampleMiddleware_crossOriginProtection is the canonical host construction:
// direct stdlib use, an optional exact trusted origin, a styled host deny
// handler, and the Handler method mounted as group middleware.
func ExampleMiddleware_crossOriginProtection() {
	protection := http.NewCrossOriginProtection()
	if err := protection.AddTrustedOrigin("https://admin.example.com"); err != nil {
		panic(err)
	}
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "styled host 403 page")
	}))

	// The method value is already a Middleware — no wrapper, no adapter.
	var protect Middleware = protection.Handler

	h := NewWebHandler()
	html := h.Group("/admin", protect)
	html.Handle("POST", "/spaces", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	crossSite := httptest.NewRequest("POST", "/admin/spaces", nil)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, crossSite)
	fmt.Println(rec.Code)

	sameOrigin := httptest.NewRequest("POST", "/admin/spaces", nil)
	sameOrigin.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, sameOrigin)
	fmt.Println(rec.Code)

	// Output:
	// 403
	// 200
}

// TestCrossOriginProtectionPosture pins the exact stdlib semantics the
// ratified posture depends on. If a Go release changes any row, the posture
// documentation must be re-ratified, not silently re-worded.
func TestCrossOriginProtectionPosture(t *testing.T) {
	const styledDeny = "styled host 403 page"

	protection := http.NewCrossOriginProtection()
	if err := protection.AddTrustedOrigin("https://admin.example.com"); err != nil {
		t.Fatalf("AddTrustedOrigin: %v", err)
	}
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, styledDeny)
	}))

	h := NewWebHandler()
	// The empty method matches any method, so one probe route exercises safe
	// and unsafe methods alike. The handler never mutates — the posture rests
	// on safe methods staying read-only.
	h.Group("/admin", protection.Handler).Handle("", "/probe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// An API route outside the HTML group is untouched by the protection.
	h.Handle("POST", "/api/probe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name       string
		method     string
		path       string
		headers    map[string]string
		wantStatus int
	}{
		{"cross-site GET passes (safe methods always pass)", "GET", "/admin/probe",
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusOK},
		{"cross-site HEAD passes", "HEAD", "/admin/probe",
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusOK},
		{"cross-site OPTIONS passes", "OPTIONS", "/admin/probe",
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusOK},
		{"same-origin unsafe passes", "POST", "/admin/probe",
			map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"Sec-Fetch-Site none unsafe passes (user-initiated navigation)", "POST", "/admin/probe",
			map[string]string{"Sec-Fetch-Site": "none"}, http.StatusOK},
		{"untrusted same-site sibling unsafe rejects", "POST", "/admin/probe",
			map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"untrusted cross-site unsafe rejects", "POST", "/admin/probe",
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"exact trusted Origin without Sec-Fetch-Site passes", "POST", "/admin/probe",
			map[string]string{"Origin": "https://admin.example.com"}, http.StatusOK},
		{"untrusted Origin without Sec-Fetch-Site rejects", "POST", "/admin/probe",
			map[string]string{"Origin": "https://evil.example.com"}, http.StatusForbidden},
		{"missing both headers passes (assumed non-browser or legacy — fail-open, documented)", "POST", "/admin/probe",
			nil, http.StatusOK},
		{"API route outside the HTML group is unchanged", "POST", "/api/probe",
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusForbidden {
				body, _ := io.ReadAll(rec.Result().Body)
				if string(body) != styledDeny {
					t.Fatalf("deny body = %q, want the styled deny handler output %q", body, styledDeny)
				}
			}
		})
	}
}

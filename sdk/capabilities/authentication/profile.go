package authentication

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"
)

// defaultMFAMethods are the RFC 8176 amr values that count as MFA when
// ClaimMapping.MFAMethods is empty.
var defaultMFAMethods = []string{"mfa", "otp", "hwk", "sms", "swk", "pop"}

// ClaimMapping names the claims NewOIDC reads, as dotted paths into the
// verified claims. A path segment that is itself a claim name containing dots
// (a URL-namespaced claim) resolves as a literal key first. Empty fields take
// the defaults in parentheses.
type ClaimMapping struct {
	Subject       string   // ("sub")
	Email         string   // ("email")
	EmailVerified string   // ("email_verified")
	Phone         string   // ("phone_number")
	PhoneVerified string   // ("phone_number_verified")
	DisplayName   string   // ("name")
	AuthTime      string   // ("auth_time"; falls back to "iat")
	AMR           string   // ("amr")
	MFAMethods    []string // amr values counting as MFA ("mfa","otp","hwk","sms","swk","pop")
	MFAFlag       string   // optional path whose non-empty or true value means MFA ("")
	Tenant        string   // optional ("")
	SignInMethod  string   // optional path naming how the subject signed in ("")
	Extra         []string // paths copied into Identity.Attributes (string values only)
}

// Profile is the vendor-neutral claim mapping for NewOIDC. No field names a
// vendor; presets supply vendor values. There is deliberately no field for
// roles or permissions.
type Profile struct {
	Claims     ClaimMapping
	TrustPhone bool     // a present phone claim counts as verified
	DenySignIn []string // SignInMethod values refused outright
}

func (m ClaimMapping) withDefaults() ClaimMapping {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&m.Subject, "sub")
	def(&m.Email, "email")
	def(&m.EmailVerified, "email_verified")
	def(&m.Phone, "phone_number")
	def(&m.PhoneVerified, "phone_number_verified")
	def(&m.DisplayName, "name")
	def(&m.AuthTime, "auth_time")
	def(&m.AMR, "amr")
	if len(m.MFAMethods) == 0 {
		m.MFAMethods = defaultMFAMethods
	}
	m.MFAMethods = slices.Clone(m.MFAMethods)
	m.Extra = slices.Clone(m.Extra)
	return m
}

// lookup resolves a dotted path. At each level the longest literal key wins
// over a split, so "https://example.test/org" and "a.b" both resolve.
func lookup(claims map[string]any, path string) (any, bool) {
	if path == "" || claims == nil {
		return nil, false
	}
	if v, ok := claims[path]; ok {
		return v, true
	}
	for i := len(path) - 1; i > 0; i-- {
		if path[i] != '.' {
			continue
		}
		next, ok := claims[path[:i]].(map[string]any)
		if !ok {
			continue
		}
		if v, ok := lookup(next, path[i+1:]); ok {
			return v, true
		}
	}
	return nil, false
}

func lookupString(claims map[string]any, path string) string {
	v, _ := lookup(claims, path)
	s, _ := v.(string)
	return s
}

// lookupBool accepts a JSON boolean or the strings "true"/"false", which some
// issuers emit for verified flags.
func lookupBool(claims map[string]any, path string) bool {
	v, _ := lookup(claims, path)
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(b, "true")
	}
	return false
}

// lookupFlag treats true or any non-empty string as set.
func lookupFlag(claims map[string]any, path string) bool {
	v, _ := lookup(claims, path)
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b != "" && !strings.EqualFold(b, "false")
	}
	return false
}

func lookupStrings(claims map[string]any, path string) []string {
	v, _ := lookup(claims, path)
	var out []string
	switch s := v.(type) {
	case []string:
		out = slices.Clone(s)
	case []any:
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
	case string:
		if s != "" {
			out = []string{s}
		}
	}
	return out
}

// lookupTime reads a NumericDate (seconds since the epoch).
func lookupTime(claims map[string]any, path string) (time.Time, bool) {
	v, ok := lookup(claims, path)
	if !ok {
		return time.Time{}, false
	}
	var secs float64
	switch n := v.(type) {
	case float64:
		secs = n
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return time.Time{}, false
		}
		secs = f
	case int64:
		secs = float64(n)
	case int:
		secs = float64(n)
	default:
		return time.Time{}, false
	}
	if math.IsNaN(secs) || math.IsInf(secs, 0) || secs <= 0 || secs > 253402300799 {
		return time.Time{}, false
	}
	whole, frac := math.Modf(secs)
	return time.Unix(int64(whole), int64(frac*1e9)).UTC(), true
}

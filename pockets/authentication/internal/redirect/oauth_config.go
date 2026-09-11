package redirect

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"reflect"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// ValidateOAuthConfig validates callback destinations before either the assembled
// pocket or an independently constructed service can create an OAuth state.
func ValidateOAuthConfig(mode environment.Mode, providers []oauth.Provider, callbackBase string, nativeURIs []string) error {
	if len(providers) == 0 {
		if len(nativeURIs) != 0 {
			return fmt.Errorf("authentication: native OAuth requires providers")
		}
		return nil
	}
	seen := make(map[string]bool)
	for _, provider := range providers {
		if nilDependency(provider) {
			return fmt.Errorf("authentication: nil OAuth provider")
		}
		name := provider.Name()
		if name == "" || seen[name] {
			return fmt.Errorf("authentication: empty or duplicate OAuth provider name")
		}
		for _, c := range name {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return fmt.Errorf("authentication: invalid OAuth provider name")
			}
		}
		seen[name] = true
	}
	u, err := url.Parse(callbackBase)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(callbackBase, "#") ||
		(u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && (path.Clean(u.Path) != u.Path || strings.HasSuffix(u.Path, "/"))) || strings.Contains(u.Path, "\\") {
		return fmt.Errorf("authentication: OAuth callback base must be an HTTP(S) origin with an optional clean path prefix, no trailing slash, query, credentials or fragment")
	}
	if u.Scheme != "https" && mode == environment.ModeProduction {
		return fmt.Errorf("authentication: OAuth callback base requires HTTPS in production")
	}
	for _, raw := range nativeURIs {
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || (u.Host == "" && u.Path == "" && u.Opaque == "") {
			return fmt.Errorf("authentication: invalid native OAuth redirect URI")
		}
		if u.Scheme == "http" {
			ip := net.ParseIP(u.Hostname())
			if ip == nil || !ip.IsLoopback() {
				return fmt.Errorf("authentication: native HTTP redirect requires a loopback IP")
			}
		}
		if u.Scheme == "https" && u.Hostname() == "" {
			return fmt.Errorf("authentication: native HTTPS redirect requires a host")
		}
	}
	return nil
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

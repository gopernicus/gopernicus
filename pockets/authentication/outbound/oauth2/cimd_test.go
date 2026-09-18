package oauth2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	protocol "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const testClientID = "https://client.example.test/oauth.json"
const validDocument = `{"client_id":"https://client.example.test/oauth.json","client_name":"Test client","redirect_uris":["https://client.example.test/callback"],"token_endpoint_auth_method":"none"}`

func TestCIMDTrustIsCheckedBeforeFetchAndOnCacheHits(t *testing.T) {
	trusted := true
	c, err := NewCIMD([]string{testClientID}, WithClientTrust(func(context.Context, string) error {
		if !trusted {
			return sdk.ErrUnauthorized
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	c.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(validDocument))}, nil
	})
	if _, err := c.Resolve(context.Background(), "https://unknown.test/client.json"); !errors.Is(err, protocol.ErrInvalidClient) || calls != 0 {
		t.Fatal("untrusted client triggered network")
	}
	got, err := c.Resolve(context.Background(), testClientID)
	if err != nil {
		t.Fatal(err)
	}
	got.RedirectURIs[0] = "changed"
	again, err := c.Resolve(context.Background(), testClientID)
	if err != nil || calls != 1 || again.RedirectURIs[0] != "https://client.example.test/callback" {
		t.Fatalf("cache copy: %+v %v calls=%d", again, err, calls)
	}
	trusted = false
	if _, err := c.Resolve(context.Background(), testClientID); !errors.Is(err, protocol.ErrInvalidClient) {
		t.Fatal("cached metadata bypassed trust withdrawal")
	}
}

func TestCIMDBoundedMetadataAndIdentity(t *testing.T) {
	for name, body := range map[string]string{
		"wrong ID":          strings.Replace(validDocument, testClientID, "https://other.test/oauth.json", 1),
		"insecure redirect": strings.Replace(validDocument, "https://client.example.test/callback", "http://127.0.0.1/callback", 1),
		"client secret":     strings.Replace(validDocument, `"client_name"`, `"client_secret":null,"client_name"`, 1),
		"wrong method":      strings.Replace(validDocument, `"none"`, `"client_secret_basic"`, 1),
		"oversized":         strings.Repeat(" ", maxDocumentBytes) + validDocument,
		"trailing JSON":     validDocument + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := NewCIMD([]string{testClientID})
			c.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			if _, err := c.Resolve(context.Background(), testClientID); err == nil {
				t.Fatal("invalid metadata admitted")
			}
			if len(c.cache) != 0 {
				t.Fatal("invalid metadata cached")
			}
		})
	}
}

func TestCIMDNoRedirectsOrEnvironmentProxy(t *testing.T) {
	c, _ := NewCIMD([]string{testClientID})
	transport := c.client.Transport.(*http.Transport)
	if transport.Proxy != nil || c.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("unsafe network defaults")
	}
	for _, address := range []string{"127.0.0.1:80", "[::1]:80", "169.254.169.254:80"} {
		if conn, err := publicDial(context.Background(), "tcp", address); err == nil {
			conn.Close()
			t.Fatalf("private destination %s dialed", address)
		}
	}
	for _, ip := range []string{"10.1.2.3", "172.16.0.1", "192.168.1.1", "100.100.100.200", "0.0.0.0", "255.255.255.255", "::ffff:127.0.0.1", "fe80::1", "fc00::1", "2001:db8::1", "64:ff9b::a00:1"} {
		if publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("nonpublic address admitted: %s", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("public address denied: %s", ip)
		}
	}
}

func TestCIMDConfigurationAndCacheBounds(t *testing.T) {
	for _, id := range []string{"http://client.test/x", "https://client.test", "https://client.test/a/../b", "https://client.test/a/%2e%2e/b", "https://user@client.test/x", "https://client.test/x#fragment"} {
		if _, err := NewCIMD([]string{id}); err == nil {
			t.Errorf("invalid metadata URL accepted: %s", id)
		}
	}
	for value, want := range map[string]time.Duration{"no-store": 0, "max-age=2": 2 * time.Second, "max-age=999999999999999999999": 0, "max-age=50000": 10 * time.Minute, "no-cache, max-age=30": 0} {
		if got := metadataTTL(http.Header{"Cache-Control": []string{value}}); got != want {
			t.Errorf("cache %q=%v want %v", value, got, want)
		}
	}
}

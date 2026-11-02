package otel

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	// otlpHTTPTracesPath is the OTLP/HTTP traces signal path appended to the
	// base Config.Endpoint, as OTEL_EXPORTER_OTLP_ENDPOINT does.
	otlpHTTPTracesPath = "/v1/traces"

	// otlpHTTPTimeout bounds one export request. It is set explicitly so
	// OTEL_EXPORTER_OTLP_TIMEOUT never applies.
	otlpHTTPTimeout = 10 * time.Second
)

// otlpHTTPTarget is a validated OTLP/HTTP Config: the full traces URL, whether
// it is plain HTTP, and the decoded headers.
type otlpHTTPTarget struct {
	url      *url.URL
	insecure bool
	headers  map[string]string
}

// parseOTLPHTTP validates cfg for ExporterOTLPHTTP in every mode: Endpoint is
// an absolute http or https base URL with a host and no userinfo, query or
// fragment; Insecure may not contradict an https scheme; Headers is a
// well-formed k=v list. Errors wrap sdk.ErrInvalidInput and never carry a
// header value.
func parseOTLPHTTP(cfg Config) (otlpHTTPTarget, error) {
	if cfg.Endpoint == "" {
		return otlpHTTPTarget{}, fmt.Errorf("otel: exporter %q requires Config.Endpoint (TRACING_OTLP_ENDPOINT), a base URL such as https://collector.example.com/otlp: %w", ExporterOTLPHTTP, sdk.ErrInvalidInput)
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Opaque != "" {
		return otlpHTTPTarget{}, fmt.Errorf("otel: Config.Endpoint is not an absolute URL: %w", sdk.ErrInvalidInput)
	}
	if u.User != nil {
		// Never echo the endpoint here: it carries the credential.
		return otlpHTTPTarget{}, fmt.Errorf("otel: Config.Endpoint carries userinfo; put credentials in Config.Headers (TRACING_OTLP_HEADERS): %w", sdk.ErrInvalidInput)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return otlpHTTPTarget{}, fmt.Errorf("otel: Config.Endpoint %q must use http or https: %w", cfg.Endpoint, sdk.ErrInvalidInput)
	}
	if u.Host == "" {
		return otlpHTTPTarget{}, fmt.Errorf("otel: Config.Endpoint %q has no host: %w", cfg.Endpoint, sdk.ErrInvalidInput)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return otlpHTTPTarget{}, fmt.Errorf("otel: Config.Endpoint %q may not carry a query or fragment: %w", cfg.Endpoint, sdk.ErrInvalidInput)
	}
	base := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(base, otlpHTTPTracesPath) {
		return otlpHTTPTarget{}, fmt.Errorf("otel: Config.Endpoint %q is the base URL; drop %s, which the exporter appends: %w", cfg.Endpoint, otlpHTTPTracesPath, sdk.ErrInvalidInput)
	}
	insecure := u.Scheme == "http"
	if cfg.Insecure && !insecure {
		return otlpHTTPTarget{}, fmt.Errorf("otel: Config.Insecure contradicts the https Config.Endpoint %q; the scheme decides the transport: %w", cfg.Endpoint, sdk.ErrInvalidInput)
	}
	headers, err := parseOTLPHeaders(cfg.Headers)
	if err != nil {
		return otlpHTTPTarget{}, err
	}
	full := *u
	full.Path = base + otlpHTTPTracesPath
	full.RawPath = ""
	return otlpHTTPTarget{url: &full, insecure: insecure, headers: headers}, nil
}

// parseOTLPHeaders decodes the OTEL_EXPORTER_OTLP_HEADERS format: comma
// separated key=value entries, each part percent-decoded and trimmed. Keys
// must be HTTP tokens and unique; values may not hold control characters. An
// error names the entry's position, never its value.
func parseOTLPHeaders(secret sdk.Secret) (map[string]string, error) {
	headers := map[string]string{}
	raw := strings.TrimSpace(secret.Reveal())
	if raw == "" {
		return headers, nil
	}
	for i, entry := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("otel: Config.Headers entry %d has no '=': %w", i+1, sdk.ErrInvalidInput)
		}
		k, kerr := url.PathUnescape(strings.TrimSpace(key))
		v, verr := url.PathUnescape(strings.TrimSpace(value))
		if kerr != nil || verr != nil {
			return nil, fmt.Errorf("otel: Config.Headers entry %d is not valid percent-encoding: %w", i+1, sdk.ErrInvalidInput)
		}
		if !isHTTPToken(k) {
			return nil, fmt.Errorf("otel: Config.Headers entry %d has an empty or invalid header name: %w", i+1, sdk.ErrInvalidInput)
		}
		if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 && r != '\t' || r == 0x7f }) {
			return nil, fmt.Errorf("otel: Config.Headers entry %d value holds a control character: %w", i+1, sdk.ErrInvalidInput)
		}
		canonical := strings.ToLower(k)
		for existing := range headers {
			if strings.ToLower(existing) == canonical {
				return nil, fmt.Errorf("otel: Config.Headers entry %d repeats header %q: %w", i+1, k, sdk.ErrInvalidInput)
			}
		}
		headers[k] = v
	}
	return headers, nil
}

// isHTTPToken reports whether s is a non-empty RFC 9110 token.
func isHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

// openOTLPHTTP wires a batching OTLP/HTTP exporter. Every option the exporter
// would otherwise read from OTEL_EXPORTER_OTLP_* is set explicitly: the URL
// (host, path and scheme-derived transport), headers (an empty map when
// unset), TLS, compression and timeout.
func openOTLPHTTP(ctx context.Context, cfg Config, target otlpHTTPTarget) (*Tracer, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(target.url.String()),
		otlptracehttp.WithHeaders(target.headers),
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
		otlptracehttp.WithTimeout(otlpHTTPTimeout),
	}
	if target.insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	} else {
		tlsCfg := cfg.tlsConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		opts = append(opts, otlptracehttp.WithTLSClientConfig(tlsCfg))
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otel: creating OTLP/HTTP exporter: %w", err)
	}

	res, err := newResource(cfg)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(rootSampler(cfg.SampleRate)),
		sdktrace.WithResource(res),
	)
	return newOwnedTracer(tp, cfg), nil
}

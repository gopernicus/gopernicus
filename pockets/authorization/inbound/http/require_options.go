package authorizationhttp

import "net/http"

type requireConfig struct {
	deniedHandler http.Handler
}

// RequireOption configures one mounted policy. Options apply in order; nil
// options and invalid final settings panic when Require constructs the gate.
type RequireOption func(*requireConfig)

// WithDeniedHandler replaces the response to a completed, error-free denial.
// For example, http.NotFoundHandler hides resource existence with a 404.
// Authentication and evaluation errors retain their normal responses.
//
// The handler receives the original request after decision evaluation completes;
// it cannot continue to the protected handler. It is borrowed and must support
// concurrent requests. Nil (including a typed nil) is invalid. Choose this
// response per policy; the handler does not receive branch-specific decisions.
func WithDeniedHandler(handler http.Handler) RequireOption {
	return func(cfg *requireConfig) { cfg.deniedHandler = handler }
}

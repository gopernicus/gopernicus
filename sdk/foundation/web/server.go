package web

import (
	"net"
	"net/http"
	"time"
)

// ServerConfig holds reusable HTTP server configuration. The run/shutdown loop
// itself lives in the delivery layer (decision B-4); sdk owns only the config
// and the *http.Server constructor.
type ServerConfig struct {
	Host            string        `env:"HOST" default:"localhost"`
	Port            string        `env:"PORT" default:"8080"`
	ReadTimeout     time.Duration `env:"READ_TIMEOUT" default:"15s"`
	WriteTimeout    time.Duration `env:"WRITE_TIMEOUT" default:"15s"`
	IdleTimeout     time.Duration `env:"IDLE_TIMEOUT" default:"120s"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" default:"10s"`
}

// Address returns the host:port listen address.
func (c ServerConfig) Address() string {
	return net.JoinHostPort(c.Host, c.Port)
}

// HTTPServer builds an *http.Server from the config and handler.
func (c ServerConfig) HTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         c.Address(),
		Handler:      handler,
		ReadTimeout:  c.ReadTimeout,
		WriteTimeout: c.WriteTimeout,
		IdleTimeout:  c.IdleTimeout,
	}
}

package web

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// WebServer wraps http.Server with additional configuration.
type WebServer struct {
	*http.Server
	Config ServerConfig
}

// ServerConfig holds web server configuration.
type ServerConfig struct {
	Host            string        `env:"HOST" default:"0.0.0.0"`
	Port            string        `env:"PORT" default:"3000"`
	EnableDebug     bool          `env:"ENABLE_DEBUG" default:"false"`
	ReadTimeout     time.Duration `env:"READ_TIMEOUT" default:"30s"`
	WriteTimeout    time.Duration `env:"WRITE_TIMEOUT" default:"10s"`
	IdleTimeout     time.Duration `env:"IDLE_TIMEOUT" default:"120s"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" default:"20s"`
}

// Address returns the full listen address (host:port).
func (c ServerConfig) Address() string {
	return fmt.Sprintf("%s:%s", c.Host, c.Port)
}

type serveroptions struct {
	handler  http.Handler
	errorLog *log.Logger
	config   ServerConfig
}

// ServerOption configures a WebServer.
type ServerOption func(*serveroptions)

// WithHandler sets the HTTP handler.
func WithHandler(handler http.Handler) ServerOption {
	return func(o *serveroptions) {
		o.handler = handler
	}
}

// WithErrorLog sets the error logger.
func WithErrorLog(errorLog *log.Logger) ServerOption {
	return func(o *serveroptions) {
		o.errorLog = errorLog
	}
}

// WithPort sets the server port.
func WithPort(port string) ServerOption {
	return func(o *serveroptions) {
		o.config.Port = port
	}
}

// WithTimeouts sets all timeout values.
func WithTimeouts(read, write, idle, shutdown time.Duration) ServerOption {
	return func(o *serveroptions) {
		o.config.ReadTimeout = read
		o.config.WriteTimeout = write
		o.config.IdleTimeout = idle
		o.config.ShutdownTimeout = shutdown
	}
}

// WithReadTimeout sets the read timeout.
func WithReadTimeout(timeout time.Duration) ServerOption {
	return func(o *serveroptions) {
		o.config.ReadTimeout = timeout
	}
}

// WithWriteTimeout sets the write timeout.
func WithWriteTimeout(timeout time.Duration) ServerOption {
	return func(o *serveroptions) {
		o.config.WriteTimeout = timeout
	}
}

// WithIdleTimeout sets the idle timeout.
func WithIdleTimeout(timeout time.Duration) ServerOption {
	return func(o *serveroptions) {
		o.config.IdleTimeout = timeout
	}
}

// WithShutdownTimeout sets the shutdown timeout.
func WithShutdownTimeout(timeout time.Duration) ServerOption {
	return func(o *serveroptions) {
		o.config.ShutdownTimeout = timeout
	}
}

// WithDebug enables debug mode.
func WithDebug(enabled bool) ServerOption {
	return func(o *serveroptions) {
		o.config.EnableDebug = enabled
	}
}

// NewServerDefault creates a new WebServer with default settings.
func NewServerDefault(opts ...ServerOption) *WebServer {
	config := ServerConfig{
		Host:            "0.0.0.0",
		Port:            "8080",
		EnableDebug:     false,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    10 * time.Second,
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 20 * time.Second,
	}
	return NewServer(config, opts...)
}

// NewServer creates a new WebServer with given config and applies options.
func NewServer(cfg ServerConfig, opts ...ServerOption) *WebServer {
	internalOpts := &serveroptions{
		config: cfg,
	}

	for _, opt := range opts {
		opt(internalOpts)
	}

	server := &http.Server{
		Addr:         internalOpts.config.Address(),
		Handler:      internalOpts.handler,
		ReadTimeout:  internalOpts.config.ReadTimeout,
		WriteTimeout: internalOpts.config.WriteTimeout,
		IdleTimeout:  internalOpts.config.IdleTimeout,
		ErrorLog:     internalOpts.errorLog,
	}

	return &WebServer{
		Server: server,
		Config: internalOpts.config,
	}
}

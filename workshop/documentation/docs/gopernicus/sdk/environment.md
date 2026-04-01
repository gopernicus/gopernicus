---
sidebar_position: 4
title: Environment
---

# SDK — Environment

`sdk/environment` handles loading `.env` files and populating typed config structs from environment variables. It wraps `os`, `bufio`, `reflect`, `strconv`, and `time` from the standard library — no external config library required.

## Loading a .env file

```go
// Load from .env in the current directory (missing file is not an error)
environment.LoadEnv()

// Load from an explicit path
environment.LoadPath("/etc/myapp/.env")
```

`.env` parsing handles the common conventions you'd expect:

- `#` comments (full-line and inline)
- `export KEY=value` prefix
- Single and double quoted values
- Inline comments are stripped from unquoted values, preserved inside quotes
- Existing env vars are **never overwritten** — process environment always wins

## Struct-tag parsing

`ParseEnvTags` populates a config struct from environment variables. Define your config once and let the tags drive everything:

```go
type Config struct {
    Host     string        `env:"HOST"     default:"0.0.0.0"`
    Port     int           `env:"PORT"     default:"8080"`
    Debug    bool          `env:"DEBUG"    default:"false"`
    Timeout  time.Duration `env:"TIMEOUT"  default:"30s"`
    Origins  []string      `env:"ORIGINS"  separator:","`
    Secret   string        `env:"SECRET"   required:"true"`
}

cfg := Config{}
if err := environment.ParseEnvTags("APP", &cfg); err != nil {
    // returned if a required var is missing
}
```

With namespace `"APP"`, `PORT` resolves to `APP_PORT`. Pass `""` to skip namespacing.

### Supported tags

| Tag | Purpose |
|---|---|
| `env:"KEY"` | Environment variable name (required for the field to be read) |
| `default:"value"` | Value used when the env var is unset and the field is zero |
| `required:"true"` | Returns an error if the env var is not set |
| `separator:","` | Delimiter for `[]string` fields (defaults to `,`) |

### Supported field types

`string`, `int`, `int64`, `float32`, `float64`, `bool`, `time.Duration`, `[]string`

### Precedence

1. Environment variable (if set)
2. Existing non-zero field value (left untouched)
3. `default` tag value
4. Zero value

## Helpers

```go
// Single value with fallback
val := environment.GetEnvOrDefault("FEATURE_FLAG", "false")

// Namespaced lookup
val := environment.GetNamespaceEnvOrDefault("APP", "PORT", "8080")
// resolves APP_PORT, falls back to "8080"
```

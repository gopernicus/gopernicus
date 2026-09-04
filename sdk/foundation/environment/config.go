// Package environment provides utilities for loading .env files and reading
// environment variables. Zero external dependencies.
package environment

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnv loads environment variables from a .env file in the current directory.
// Missing file is not an error — it simply returns nil.
func LoadEnv() error {
	return LoadPath(".env")
}

// LoadPath loads environment variables from the specified file path.
// Missing file is not an error — it simply returns nil.
func LoadPath(p string) error {
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comment lines.
		if line == "" || line[0] == '#' {
			continue
		}

		// Strip "export " prefix.
		if trimmed, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(trimmed)
		}

		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value := strings.TrimSpace(raw)

		// Strip surrounding quotes (single or double).
		quoted := false
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
				quoted = true
			}
		}

		// Strip inline comments (only for unquoted values).
		// e.g. FOO=bar # this is a comment
		// The scan runs on the raw text after '=' so that whitespace TrimSpace
		// would have removed still marks the comment: "FOO=   # note" is empty.
		if !quoted {
			if idx := inlineCommentIndex(raw); idx != -1 {
				value = strings.TrimSpace(raw[:idx])
			}
		}

		// Don't overwrite existing env vars.
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}

// inlineCommentIndex reports the index of the '#' that starts an inline comment
// in an unquoted value — the first '#' preceded by a space or tab — or -1 when
// the value has none. A '#' that is not preceded by whitespace is part of the
// value (SLACK_CHANNEL=#alerts, COLOR=#ff0000).
func inlineCommentIndex(value string) int {
	for i := 1; i < len(value); i++ {
		if value[i] == '#' && (value[i-1] == ' ' || value[i-1] == '\t') {
			return i
		}
	}
	return -1
}

// GetEnvOrDefault retrieves an environment variable, returning fallback
// if the variable is not set.
func GetEnvOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// GetNamespaceEnvKey constructs a namespaced environment variable key.
// If namespace is empty, returns the key unchanged.
func GetNamespaceEnvKey(namespace, key string) string {
	if namespace == "" {
		return key
	}
	return fmt.Sprintf("%s_%s", namespace, key)
}

// GetNamespaceEnvOrDefault retrieves a namespaced environment variable,
// returning fallback if it is not set.
func GetNamespaceEnvOrDefault(namespace, key, fallback string) string {
	return GetEnvOrDefault(GetNamespaceEnvKey(namespace, key), fallback)
}

// GetNamespaceEnvValue retrieves the value of a namespaced environment variable.
func GetNamespaceEnvValue(namespace, key string) string {
	return os.Getenv(GetNamespaceEnvKey(namespace, key))
}

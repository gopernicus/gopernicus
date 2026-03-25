// Package environment provides utilities for loading .env files and populating
// structs from environment variables using struct tags. Zero external dependencies.
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

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

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
		if !quoted {
			if idx := strings.Index(value, " #"); idx != -1 {
				value = strings.TrimSpace(value[:idx])
			}
		}

		// Don't overwrite existing env vars.
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
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

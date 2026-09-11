// Package environment provides utilities for loading .env files and reading
// environment variables. Zero external dependencies.
package environment

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// LoadEnv loads environment variables from a .env file in the current directory.
// Missing file is not an error — it simply returns nil.
func LoadEnv() error {
	return LoadPath(".env")
}

// LoadPath loads single-line assignments from p without replacing existing
// environment variables, including ones set to an empty value. Missing files
// are ignored. Earlier assignments remain applied if a later line fails.
//
// Lines may be blank, comments, or KEY=value assignments with an optional
// "export " prefix. Keys must be nonempty and contain no whitespace or NUL.
// Values are literal: surrounding single or double quotes are removed; outside
// quotes, a space or tab followed by # starts a comment. A quoted value may
// only be followed by whitespace and a comment. Escapes, interpolation, and
// multiline values are not supported. Errors name the file, line, and valid
// key when available, without including the value.
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
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		key, value, err := parseEnvLine(scanner.Text())
		if err == nil && key != "" {
			if _, exists := os.LookupEnv(key); !exists {
				err = os.Setenv(key, value)
			}
		}
		if err != nil {
			if key != "" {
				return fmt.Errorf("%s:%d: %s: %w", p, lineNumber, key, err)
			}
			return fmt.Errorf("%s:%d: %w", p, lineNumber, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%s:%d: read environment: %w", p, lineNumber+1, err)
	}
	return nil
}

func parseEnvLine(line string) (key, value string, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", nil
	}
	if trimmed, ok := strings.CutPrefix(line, "export "); ok {
		line = strings.TrimSpace(trimmed)
	}
	key, raw, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", errors.New("expected KEY=value assignment")
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsRune(key, 0) || strings.IndexFunc(key, unicode.IsSpace) >= 0 {
		return "", "", errors.New("key must be nonempty and contain no whitespace or NUL")
	}

	value = strings.TrimSpace(raw)
	if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
		end := strings.IndexByte(value[1:], value[0])
		if end < 0 {
			return key, "", errors.New("unterminated quoted value")
		}
		end++
		tail := value[end+1:]
		if tail != "" {
			if (tail[0] != ' ' && tail[0] != '\t') || !strings.HasPrefix(strings.TrimSpace(tail), "#") {
				return key, "", errors.New("expected whitespace and a comment after quoted value")
			}
		}
		return key, value[1:end], nil
	}
	if i := inlineCommentIndex(raw); i >= 0 {
		value = strings.TrimSpace(raw[:i])
	}
	return key, value, nil
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

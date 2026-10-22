// Package environment provides utilities for loading .env files and reading
// environment variables. Zero external dependencies.
package environment

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode"
)

// dotenvPath is the working-directory file LoadEnvForMode loads in development.
const dotenvPath = ".env"

// ErrModeFromFile is returned by LoadEnvForMode when .env names the mode key.
// The mode has one source, the process environment, so a file in the working
// directory can never choose or downgrade the posture.
var ErrModeFromFile = errors.New("environment: the mode is read from the process environment only, never from .env")

// LoadEnv loads environment variables from a .env file in the current directory.
// Missing file is not an error — it simply returns nil.
func LoadEnv() error {
	return LoadPath(".env")
}

// LoadEnvForMode reads the runtime mode from the process environment variable
// key and loads .env only in development:
//
//   - unset or empty: an error matching ErrModeRequired; .env is not read.
//   - any value other than "development" or "production": an error matching
//     ErrModeInvalid; .env is not read.
//   - production: .env is never opened. If one exists in the working
//     directory, one WARN on slog.Default records that it was ignored.
//   - development: .env is loaded as by LoadEnv, unless it names key on any
//     line, which is ErrModeFromFile and loads nothing.
//
// The host chooses key, for example APP_RUNTIME_MODE. Every error names it and
// where its value belongs. LoadEnv remains for hosts that resolve no mode.
func LoadEnvForMode(key string) (Mode, error) {
	if key == "" {
		return "", errors.New("environment: LoadEnvForMode requires the mode's environment key")
	}
	raw := os.Getenv(key)
	if raw == "" {
		return "", fmt.Errorf("%w: %s is unset; %s", ErrModeRequired, key, modeRemedy(key))
	}
	mode, err := ParseMode(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w; %s", key, err, modeRemedy(key))
	}
	if mode.IsProduction() {
		if _, err := os.Stat(dotenvPath); err == nil {
			slog.Default().Warn(".env present, ignored in production", "path", dotenvPath, "mode_key", key)
		}
		return mode, nil
	}
	line, err := lineNaming(dotenvPath, key)
	if err != nil {
		return "", err
	}
	if line > 0 {
		return "", fmt.Errorf("%w: %s:%d names %s; delete that line and %s", ErrModeFromFile, dotenvPath, line, key, modeRemedy(key))
	}
	if err := LoadPath(dotenvPath); err != nil {
		return "", err
	}
	return mode, nil
}

// modeRemedy says where the mode belongs in each posture.
func modeRemedy(key string) string {
	return fmt.Sprintf("set %[1]s in the process environment: development by exporting %[1]s=development "+
		"or prefixing the command (the rest of .env still loads); production through systemd "+
		"Environment=/EnvironmentFile=, docker --env-file, or platform secrets", key)
}

// lineNaming returns the first line of the dotenv file p that assigns key, or
// 0 when p is missing or never names it. LoadPath reports malformed lines.
func lineNaming(p, key string) (int, error) {
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		if name, _, _ := parseEnvLine(scanner.Text()); name == key {
			return n, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("%s: read environment: %w", p, err)
	}
	return 0, nil
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

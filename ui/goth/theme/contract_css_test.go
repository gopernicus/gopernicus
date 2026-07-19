package theme

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// declRE matches a `--name: value;` custom-property declaration; commentRE strips
// /* ... */ comments so selector text in prose never matches a real selector.
var (
	declRE    = regexp.MustCompile(`--([a-z0-9-]+)\s*:\s*([^;{}]+);`)
	commentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// defaultTokenValues is the owner-default LIGHT palette used ONLY as test data to
// guard the Go token vocabulary against the CSS palette. It MUST stay
// byte-identical to the resolved light :root palette (contract.css neutrals
// overridden by default.css light), which TestDefaultCSSMatchesDefaultTheme
// enforces. It is test-only: the shipping token VALUES live in CSS, not in a Go
// value (amendment-1 D5). Token names are frozen public surface (README.md §5).
var defaultTokenValues = map[Token]string{
	TokenBackground:        "oklch(1 0 0)",
	TokenForeground:        "oklch(0.15 0 0)",
	TokenCard:              "oklch(1 0 0)",
	TokenCardForeground:    "oklch(0.15 0 0)",
	TokenPopover:           "oklch(1 0 0)",
	TokenPopoverForeground: "oklch(0.15 0 0)",
	TokenOverlay:           "oklch(0.15 0 0 / 0.5)",

	TokenPrimary:               "oklch(0.21 0.02 265)",
	TokenPrimaryForeground:     "oklch(0.98 0 0)",
	TokenSecondary:             "oklch(0.96 0 0)",
	TokenSecondaryForeground:   "oklch(0.21 0.02 265)",
	TokenMuted:                 "oklch(0.96 0 0)",
	TokenMutedForeground:       "oklch(0.55 0 0)",
	TokenAccent:                "oklch(0.96 0 0)",
	TokenAccentForeground:      "oklch(0.21 0.02 265)",
	TokenDestructive:           "oklch(0.58 0.22 27)",
	TokenDestructiveForeground: "oklch(0.98 0 0)",

	TokenSuccess:            "oklch(0.62 0.17 145)",
	TokenSuccessForeground:  "oklch(0.98 0 0)",
	TokenWarning:            "oklch(0.75 0.16 80)",
	TokenWarningForeground:  "oklch(0.21 0.02 265)",
	TokenTertiary:           "oklch(0.62 0.15 250)",
	TokenTertiaryForeground: "oklch(0.98 0 0)",

	TokenBorder: "oklch(0.9 0 0)",
	TokenInput:  "oklch(0.9 0 0)",
	TokenRing:   "oklch(0.21 0.02 265)",

	TokenChart1: "oklch(0.62 0.19 30)",
	TokenChart2: "oklch(0.6 0.12 185)",
	TokenChart3: "oklch(0.4 0.07 230)",
	TokenChart4: "oklch(0.83 0.19 85)",
	TokenChart5: "oklch(0.77 0.19 70)",

	TokenSidebar:                  "oklch(0.98 0 0)",
	TokenSidebarForeground:        "oklch(0.15 0 0)",
	TokenSidebarPrimary:           "oklch(0.21 0.02 265)",
	TokenSidebarPrimaryForeground: "oklch(0.98 0 0)",
	TokenSidebarAccent:            "oklch(0.96 0 0)",
	TokenSidebarAccentForeground:  "oklch(0.21 0.02 265)",
	TokenSidebarBorder:            "oklch(0.9 0 0)",
	TokenSidebarRing:              "oklch(0.21 0.02 265)",

	TokenRadius:   "0.5rem",
	TokenShadowSm: "0 1px 2px 0 rgb(0 0 0 / 0.05)",
	TokenShadow:   "0 1px 3px 0 rgb(0 0 0 / 0.1)",
	TokenShadowMd: "0 4px 6px -1px rgb(0 0 0 / 0.1)",
	TokenShadowLg: "0 10px 15px -3px rgb(0 0 0 / 0.1)",

	TokenFontSans:  "ui-sans-serif, system-ui, sans-serif",
	TokenFontSerif: "ui-serif, Georgia, serif",
	TokenFontMono:  "ui-monospace, SFMono-Regular, monospace",

	TokenDurationFast:   "120ms",
	TokenDuration:       "200ms",
	TokenDurationSlow:   "320ms",
	TokenEase:           "cubic-bezier(0.4, 0, 0.2, 1)",
	TokenEaseEmphasized: "cubic-bezier(0.2, 0, 0, 1)",

	TokenDensity:  "1",
	TokenZBase:    "0",
	TokenZSticky:  "100",
	TokenZOverlay: "1000",
	TokenZModal:   "1100",
	TokenZPopover: "1200",
	TokenZToast:   "1300",
}

// TestDefaultTokenValuesCoverFrozenSet keeps the test-data palette exhaustive: a
// new frozen token added without a default value fails here rather than silently.
func TestDefaultTokenValuesCoverFrozenSet(t *testing.T) {
	for _, tok := range Tokens() {
		if _, ok := defaultTokenValues[tok]; !ok {
			t.Errorf("defaultTokenValues test data is missing --%s (frozen token without a default value)", tok)
		}
	}
	if len(defaultTokenValues) != len(Tokens()) {
		t.Errorf("defaultTokenValues has %d entries, want %d (one per frozen token)", len(defaultTokenValues), len(Tokens()))
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return commentRE.ReplaceAllString(string(b), "")
}

// block returns the declarations inside the first `{...}` following selector.
func block(t *testing.T, css, selector string) map[Token]string {
	t.Helper()
	i := strings.Index(css, selector)
	if i < 0 {
		t.Fatalf("selector %q not found", selector)
	}
	open := strings.Index(css[i:], "{")
	if open < 0 {
		t.Fatalf("no block opens after %q", selector)
	}
	rest := css[i+open+1:]
	close := strings.Index(rest, "}")
	if close < 0 {
		t.Fatalf("no block closes after %q", selector)
	}
	out := map[Token]string{}
	for _, m := range declRE.FindAllStringSubmatch(rest[:close], -1) {
		out[Token(m[1])] = strings.TrimSpace(m[2])
	}
	return out
}

// TestContractCSSDeclaresEveryToken proves the compiled kit stylesheet defines a
// CSS custom property for every frozen token, so no primitive references an
// undefined variable.
func TestContractCSSDeclaresEveryToken(t *testing.T) {
	contract := block(t, readFile(t, "contract.css"), ":root")
	for _, tok := range Tokens() {
		if _, ok := contract[tok]; !ok {
			t.Errorf("contract.css :root is missing --%s (every frozen token needs a neutral fallback)", tok)
		}
	}
}

// TestDefaultCSSMatchesDefaultTheme proves the light :root palette the browser
// resolves (contract neutrals overridden by default.css light) is byte-identical
// to the defaultTokenValues test data, so the frozen token vocabulary and the CSS
// palette never drift apart. This is the Go↔CSS drift guard.
func TestDefaultCSSMatchesDefaultTheme(t *testing.T) {
	light := block(t, readFile(t, "contract.css"), ":root")
	for tok, v := range block(t, readFile(t, "default.css"), ":root") {
		light[tok] = v // default.css light overrides the neutral fallback
	}
	for _, tok := range Tokens() {
		want := defaultTokenValues[tok]
		got, ok := light[tok]
		if !ok {
			t.Errorf("resolved :root light palette is missing --%s", tok)
			continue
		}
		if got != want {
			t.Errorf("--%s: css %q != default test data %q (Go/CSS drift)", tok, got, want)
		}
	}
}

// TestDefaultCSSDefinesDarkPalette proves the dark palette exists under both the
// .dark class and the [data-theme="dark"] form (theme.HTMLAttributes emits the
// latter) and actually differs from light for the core surfaces.
func TestDefaultCSSDefinesDarkPalette(t *testing.T) {
	css := readFile(t, "default.css")
	for _, sel := range []string{".dark", `[data-theme="dark"]`} {
		if !strings.Contains(css, sel) {
			t.Errorf("default.css does not target %q", sel)
		}
	}
	dark := block(t, css, ".dark")
	light := block(t, css, ":root")
	for _, tok := range []Token{TokenBackground, TokenForeground, TokenCard} {
		if dark[tok] == "" {
			t.Errorf("dark palette missing --%s", tok)
		}
		if dark[tok] == light[tok] {
			t.Errorf("--%s: dark value equals light value %q (dark palette not applied)", tok, dark[tok])
		}
	}
}

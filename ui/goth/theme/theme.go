// Package theme holds the ui/goth semantic token vocabulary and the
// document-attribute helpers. Token NAMES are the frozen public contract (their
// CSS custom-property form "--<token>" is the styling surface); the token VALUES
// live in CSS (theme/contract.css neutral fallbacks + theme/default.css palette),
// never in a Go value. See ../README.md §5 for the frozen token contract.
package theme

import (
	"github.com/a-h/templ"
)

// Token is a frozen semantic token name. Its CSS variable form is "--<token>".
type Token string

// Frozen token set (README.md §5). Names are stable public compatibility surface.
const (
	// Surfaces & text.
	TokenBackground        Token = "background"
	TokenForeground        Token = "foreground"
	TokenCard              Token = "card"
	TokenCardForeground    Token = "card-foreground"
	TokenPopover           Token = "popover"
	TokenPopoverForeground Token = "popover-foreground"
	TokenOverlay           Token = "overlay"

	// Semantic roles (+ foregrounds).
	TokenPrimary               Token = "primary"
	TokenPrimaryForeground     Token = "primary-foreground"
	TokenSecondary             Token = "secondary"
	TokenSecondaryForeground   Token = "secondary-foreground"
	TokenMuted                 Token = "muted"
	TokenMutedForeground       Token = "muted-foreground"
	TokenAccent                Token = "accent"
	TokenAccentForeground      Token = "accent-foreground"
	TokenDestructive           Token = "destructive"
	TokenDestructiveForeground Token = "destructive-foreground"

	// Status roles (+ foregrounds).
	TokenSuccess            Token = "success"
	TokenSuccessForeground  Token = "success-foreground"
	TokenWarning            Token = "warning"
	TokenWarningForeground  Token = "warning-foreground"
	TokenTertiary           Token = "tertiary"
	TokenTertiaryForeground Token = "tertiary-foreground"

	// Form / outline.
	TokenBorder Token = "border"
	TokenInput  Token = "input"
	TokenRing   Token = "ring"

	// Charts.
	TokenChart1 Token = "chart-1"
	TokenChart2 Token = "chart-2"
	TokenChart3 Token = "chart-3"
	TokenChart4 Token = "chart-4"
	TokenChart5 Token = "chart-5"

	// Sidebar.
	TokenSidebar                  Token = "sidebar"
	TokenSidebarForeground        Token = "sidebar-foreground"
	TokenSidebarPrimary           Token = "sidebar-primary"
	TokenSidebarPrimaryForeground Token = "sidebar-primary-foreground"
	TokenSidebarAccent            Token = "sidebar-accent"
	TokenSidebarAccentForeground  Token = "sidebar-accent-foreground"
	TokenSidebarBorder            Token = "sidebar-border"
	TokenSidebarRing              Token = "sidebar-ring"

	// Shape & elevation. shadow-lg is the frozen elevation ceiling.
	TokenRadius   Token = "radius"
	TokenShadowSm Token = "shadow-sm"
	TokenShadow   Token = "shadow"
	TokenShadowMd Token = "shadow-md"
	TokenShadowLg Token = "shadow-lg"

	// Typography.
	TokenFontSans  Token = "font-sans"
	TokenFontSerif Token = "font-serif"
	TokenFontMono  Token = "font-mono"

	// Motion.
	TokenDurationFast   Token = "duration-fast"
	TokenDuration       Token = "duration"
	TokenDurationSlow   Token = "duration-slow"
	TokenEase           Token = "ease"
	TokenEaseEmphasized Token = "ease-emphasized"

	// Density & layering. density is a real spacing-scale multiplier from which
	// each component's padding and gap are derived.
	TokenDensity  Token = "density"
	TokenZBase    Token = "z-base"
	TokenZSticky  Token = "z-sticky"
	TokenZOverlay Token = "z-overlay"
	TokenZModal   Token = "z-modal"
	TokenZPopover Token = "z-popover"
	TokenZToast   Token = "z-toast"
)

// frozenTokens is every frozen token name (README.md §5). It is the single source
// of the public vocabulary; the token VALUES are CSS-only (contract.css/default.css)
// and are cross-checked against this list by contract_css_test.go.
var frozenTokens = []Token{
	TokenBackground,
	TokenForeground,
	TokenCard,
	TokenCardForeground,
	TokenPopover,
	TokenPopoverForeground,
	TokenOverlay,

	TokenPrimary,
	TokenPrimaryForeground,
	TokenSecondary,
	TokenSecondaryForeground,
	TokenMuted,
	TokenMutedForeground,
	TokenAccent,
	TokenAccentForeground,
	TokenDestructive,
	TokenDestructiveForeground,

	TokenSuccess,
	TokenSuccessForeground,
	TokenWarning,
	TokenWarningForeground,
	TokenTertiary,
	TokenTertiaryForeground,

	TokenBorder,
	TokenInput,
	TokenRing,

	TokenChart1,
	TokenChart2,
	TokenChart3,
	TokenChart4,
	TokenChart5,

	TokenSidebar,
	TokenSidebarForeground,
	TokenSidebarPrimary,
	TokenSidebarPrimaryForeground,
	TokenSidebarAccent,
	TokenSidebarAccentForeground,
	TokenSidebarBorder,
	TokenSidebarRing,

	TokenRadius,
	TokenShadowSm,
	TokenShadow,
	TokenShadowMd,
	TokenShadowLg,

	TokenFontSans,
	TokenFontSerif,
	TokenFontMono,

	TokenDurationFast,
	TokenDuration,
	TokenDurationSlow,
	TokenEase,
	TokenEaseEmphasized,

	TokenDensity,
	TokenZBase,
	TokenZSticky,
	TokenZOverlay,
	TokenZModal,
	TokenZPopover,
	TokenZToast,
}

// Tokens returns every frozen token name in a deterministic (sorted) order.
func Tokens() []Token {
	out := make([]Token, len(frozenTokens))
	copy(out, frozenTokens)
	sortTokens(out)
	return out
}

func sortTokens(toks []Token) {
	for i := 1; i < len(toks); i++ {
		for j := i; j > 0 && toks[j] < toks[j-1]; j-- {
			toks[j], toks[j-1] = toks[j-1], toks[j]
		}
	}
}

// Appearance selects light/dark/system rendering. Light lives on :root; dark
// supports both the .dark class and the data-theme form. System preference is an
// opt-in selection POLICY resolved by the host/controller, never a component.
type Appearance string

const (
	AppearanceLight  Appearance = "light"
	AppearanceDark   Appearance = "dark"
	AppearanceSystem Appearance = "system"
)

// Direction is the document text direction.
type Direction string

const (
	DirectionLTR Direction = "ltr"
	DirectionRTL Direction = "rtl"
)

// HTMLAttributes returns the <html> attributes (dir + data-theme) for an
// appearance and text direction, for a host to apply on the document element.
// The empty Appearance is treated as light; the empty Direction as LTR. System is
// left without a data-theme so the host/controller resolves it.
func HTMLAttributes(a Appearance, dir Direction) templ.Attributes {
	attrs := templ.Attributes{}
	switch dir {
	case DirectionRTL:
		attrs["dir"] = string(DirectionRTL)
	default:
		attrs["dir"] = string(DirectionLTR)
	}
	switch a {
	case AppearanceDark:
		attrs["data-theme"] = string(AppearanceDark)
	case AppearanceSystem:
		// Host/controller resolves system preference; emit no data-theme.
	default:
		attrs["data-theme"] = string(AppearanceLight)
	}
	return attrs
}

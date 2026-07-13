// Package testoverride ships one LayerApp email content template the delivery
// router's override tests embed as a stand-in host override, proving a host
// embed.FS can override a LayerCore default (design §6.2). It is test support for
// the sibling delivery package only and holds no production behavior.
package testoverride

import "embed"

// FS carries a single "templates/verification.html" that overrides the feature's
// LayerCore verification template when passed as a delivery.TemplateOverride.
//
//go:embed templates/*.html
var FS embed.FS

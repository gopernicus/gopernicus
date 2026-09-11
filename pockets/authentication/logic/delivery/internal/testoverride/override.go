// Package testoverride ships one LayerApp email content template and one LayerApp
// email layout the delivery router's override tests embed as stand-in host
// overrides, proving a host embed.FS can override a LayerCore body and the sdk's
// bundled layout frame (design §6.2). It is test support for the sibling delivery
// package only and holds no production behavior.
package testoverride

import "embed"

// FS carries a single "templates/verification.html" that overrides the pocket's
// LayerCore verification template when passed as a delivery.TemplateOverride.
//
//go:embed templates/*.html
var FS embed.FS

// LayoutsFS carries a "layouts/transactional.{html,txt}" pair that overrides the
// sdk's bundled transactional layout when passed as a delivery.LayoutOverride —
// the shape a host ships (an embed.FS of layout files, walked from "layouts").
//
//go:embed layouts/*
var LayoutsFS embed.FS

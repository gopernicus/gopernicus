package goth

import (
	"strconv"
	"strings"
)

const (
	// qrQuietZone is the light border, in modules, a scanner needs around the
	// symbol (ISO/IEC 18004). The module matrix a QRModules returns has none.
	qrQuietZone = 4
	// qrMinPixels is the smallest rendered edge, so a phone camera resolves the
	// modules from a laptop screen.
	qrMinPixels = 200
)

// QRModules encodes text as a QR code module matrix: row-major, square, true
// meaning dark, with no quiet zone. integrations/encoding/rsc-qr's Modules is
// one; the views module never imports an encoder itself.
type QRModules func(text string) ([][]bool, error)

// qrImage is one QR code ready for the template: a single SVG path of the dark
// modules over a white square that includes the quiet zone.
type qrImage struct {
	ViewBox string
	Pixels  string
	Path    string
}

// WithQR renders the enrollment page's otpauth:// URI as an inline SVG QR code
// through fn. Without it (or when fn fails) the page shows the key and the
// otpauth:// link only.
func WithQR(fn QRModules) Option {
	return func(v *viewConfig) { v.qr = fn }
}

// qrFor builds the QR image for text, or reports false when no encoder is wired,
// the encoder refuses text, or the matrix is not square.
func qrFor(fn QRModules, text string) (qrImage, bool) {
	if fn == nil || text == "" {
		return qrImage{}, false
	}
	modules, err := fn(text)
	if err != nil || len(modules) == 0 {
		return qrImage{}, false
	}
	return qrSVG(modules)
}

// qrSVG draws modules as one path: each horizontal run of dark modules is one
// rectangle, offset by the quiet zone. The edge is a whole number of pixels per
// module, at least qrMinPixels.
func qrSVG(modules [][]bool) (qrImage, bool) {
	n := len(modules)
	var d strings.Builder
	for y, row := range modules {
		if len(row) != n {
			return qrImage{}, false
		}
		for x := 0; x < n; {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < n && row[x] {
				x++
			}
			run := strconv.Itoa(x - start)
			d.WriteString("M" + strconv.Itoa(start+qrQuietZone) + " " + strconv.Itoa(y+qrQuietZone) + "h" + run + "v1h-" + run + "z")
		}
	}
	edge := n + 2*qrQuietZone
	scale := (qrMinPixels + edge - 1) / edge
	return qrImage{
		ViewBox: "0 0 " + strconv.Itoa(edge) + " " + strconv.Itoa(edge),
		Pixels:  strconv.Itoa(edge * scale),
		Path:    d.String(),
	}, true
}

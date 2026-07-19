package primitives

import "github.com/a-h/templ"

// Ratio is the typed ratio enum for AspectRatio (P02). A fixed set keeps the
// geometry in external CSS (via data-ratio) so the primitive never emits an
// inline style attribute (README §4 invariant a). The zero value is square.
type Ratio string

const (
	// AspectSquare is the zero value (1:1).
	AspectSquare    Ratio = ""
	AspectVideo     Ratio = "video"      // 16:9
	AspectWide      Ratio = "wide"       // 21:9
	AspectFourThree Ratio = "four-three" // 4:3
	AspectPortrait  Ratio = "portrait"   // 3:4
)

// Valid reports whether r is a known Ratio.
func (r Ratio) Valid() bool {
	switch r {
	case AspectSquare, AspectVideo, AspectWide, AspectFourThree, AspectPortrait:
		return true
	default:
		return false
	}
}

func (r Ratio) orDefault() Ratio {
	if r.Valid() {
		return r
	}
	return AspectSquare
}

func (r Ratio) attr() string {
	if r.orDefault() == AspectSquare {
		return "square"
	}
	return string(r.orDefault())
}

// AspectRatioProps configures AspectRatio (P02, family F2). The principal content
// (image, video, iframe, or any element) is passed as templ children and fills
// the ratio box. The zero value is a valid square wrapper.
type AspectRatioProps struct {
	Base
	Ratio Ratio
}

func aspectRatioClass(p AspectRatioProps) string {
	return classNames("goth-aspect-ratio", p.Class)
}

func aspectRatioAttrs(p AspectRatioProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "aspect-ratio",
		"data-ratio": p.Ratio.attr(),
	})
}

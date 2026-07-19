package primitives

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/a-h/templ"
)

// Chart (P58, family F4 shipped native/no-controller). The SERVER owns the chart:
// Go computes the scales and geometry and ChartSVG emits an <svg> using only
// presentation/geometry attributes (viewBox, x/y/width/height, points, d, cx/cy/r)
// plus stable .goth-* classes over the frozen chart-1..chart-5 theme tokens. There
// is no client-side charting library and no canvas dependency — the baseline is a
// complete server-rendered SVG that renders on the StylesOnly profile with no
// JavaScript. Per the frozen invariant (README §4) the SVG emits NO server-rendered
// style attribute and no inline <style>: series colors ride the data-series hook and
// external CSS, and geometry rides sanctioned SVG presentation attributes.
//
// The frame parts are caller-composed (F3): Chart (the themed figure), ChartHeader/
// ChartTitle/ChartDescription, ChartContent (the engine-neutral seam — drop in
// ChartSVG OR a host-rendered <svg>), ChartLegend/ChartLegendItem (swatches keyed to
// data-series), and ChartTable (a native <details> disclosure wrapping the tabular
// fallback the caller composes from Table P24). Tooltips are native SVG <title>
// elements on every mark (a no-JS, CSP-safe hover tooltip); the full data is always
// reachable through the ChartTable fallback, which is the keyboard/screen-reader path.
//
// data-slot hooks: chart, chart-header, chart-title, chart-description, chart-content,
// chart-svg, chart-legend, chart-legend-item, chart-swatch, chart-legend-label,
// chart-table, chart-table-summary.

// ChartKind selects how ChartSVG renders the series. The zero value is a bar chart.
type ChartKind string

const (
	// ChartBar is the zero value and the documented default: grouped vertical bars.
	ChartBar ChartKind = ""
	// ChartLine plots each series as a polyline with data-point dots.
	ChartLine ChartKind = "line"
	// ChartArea is a line chart with a filled area beneath each series.
	ChartArea ChartKind = "area"
)

// Valid reports whether k is a known ChartKind.
func (k ChartKind) Valid() bool {
	switch k {
	case ChartBar, ChartLine, ChartArea:
		return true
	default:
		return false
	}
}

// ChartPoint is one datum: a category label and its value.
type ChartPoint struct {
	Label string
	Value float64
}

// ChartSeries is one data series. Color selects the chart-N theme token (1..5);
// 0 falls back to the series index (wrapping through the five tokens). Name is used
// in the per-mark tooltip title.
type ChartSeries struct {
	Name   string
	Color  int
	Points []ChartPoint
}

// ChartData is the server-owned model ChartSVG renders. The zero value renders an
// empty bar chart. Width/Height default to 640x260 when zero. BaseID, when set, is
// the id prefix for the SVG <title>/<desc> so the role=img SVG is named via
// aria-labelledby; leave it empty and the <title> still names the graphic.
type ChartData struct {
	Kind        ChartKind
	BaseID      string
	Title       string
	Description string
	Series      []ChartSeries
	Width       int
	Height      int
}

func (d ChartData) titleID() string {
	if d.BaseID == "" {
		return ""
	}
	return d.BaseID + "-title"
}

func (d ChartData) descID() string {
	if d.BaseID == "" || d.Description == "" {
		return ""
	}
	return d.BaseID + "-desc"
}

func (d ChartData) labelledBy() string {
	ids := make([]string, 0, 2)
	if t := d.titleID(); t != "" {
		ids = append(ids, t)
	}
	if dd := d.descID(); dd != "" {
		ids = append(ids, dd)
	}
	return strings.Join(ids, " ")
}

// Chart geometry constants (logical viewBox units). Left/bottom padding leave room
// for the value and category axis labels.
const (
	chartPadLeft   = 44.0
	chartPadRight  = 16.0
	chartPadTop    = 16.0
	chartPadBottom = 32.0
	chartYTicks    = 4
)

type chartBar struct {
	X, Y, W, H, Series, Title string
}

type chartDot struct {
	CX, CY, Series, Title string
}

type chartLine struct {
	Series, Name, Points, Area string
	Dots                       []chartDot
}

type chartAxisLabel struct {
	X, Y, Text string
}

type chartGridLine struct {
	X1, Y1, X2, Y2, TX, TY, Text string
}

type chartLayout struct {
	Width, Height                  int
	ViewBox                        string
	AxisX1, AxisY1, AxisX2, AxisY2 string
	YTicks                         []chartGridLine
	XLabels                        []chartAxisLabel
	Bars                           []chartBar
	Lines                          []chartLine
}

// computeChartLayout turns the server-owned ChartData into concrete SVG geometry.
// It is a pure function so the scales/coordinates are deterministic and unit-tested.
func computeChartLayout(d ChartData) chartLayout {
	w := d.Width
	if w <= 0 {
		w = 640
	}
	h := d.Height
	if h <= 0 {
		h = 260
	}
	x0, x1 := chartPadLeft, float64(w)-chartPadRight
	y0, y1 := chartPadTop, float64(h)-chartPadBottom
	_ = y0
	pw, ph := x1-x0, y1-y0

	dataMax, dataMin := 0.0, 0.0
	n := 0
	for _, s := range d.Series {
		if len(s.Points) > n {
			n = len(s.Points)
		}
		for _, p := range s.Points {
			if p.Value > dataMax {
				dataMax = p.Value
			}
			if p.Value < dataMin {
				dataMin = p.Value
			}
		}
	}
	domMax := niceCeil(dataMax)
	if domMax == 0 {
		domMax = 1
	}
	domMin := 0.0
	if dataMin < 0 {
		domMin = -niceCeil(-dataMin)
	}
	span := domMax - domMin
	if span == 0 {
		span = 1
	}
	yOf := func(v float64) float64 { return y1 - (v-domMin)/span*ph }
	baseY := yOf(0)

	lay := chartLayout{
		Width:   w,
		Height:  h,
		ViewBox: fmt.Sprintf("0 0 %d %d", w, h),
		AxisX1:  fnum(x0), AxisY1: fnum(baseY),
		AxisX2: fnum(x1), AxisY2: fnum(baseY),
	}

	for t := 0; t <= chartYTicks; t++ {
		v := domMin + span*float64(t)/float64(chartYTicks)
		y := yOf(v)
		lay.YTicks = append(lay.YTicks, chartGridLine{
			X1: fnum(x0), Y1: fnum(y), X2: fnum(x1), Y2: fnum(y),
			TX: fnum(x0 - 6), TY: fnum(y + 3), Text: fmtValue(v),
		})
	}

	if n == 0 {
		return lay
	}
	catW := pw / float64(n)

	var labelSeries ChartSeries
	for _, s := range d.Series {
		if len(s.Points) == n {
			labelSeries = s
			break
		}
	}
	for i := 0; i < n; i++ {
		cx := x0 + catW*(float64(i)+0.5)
		txt := ""
		if i < len(labelSeries.Points) {
			txt = labelSeries.Points[i].Label
		}
		lay.XLabels = append(lay.XLabels, chartAxisLabel{X: fnum(cx), Y: fnum(y1 + 16), Text: txt})
	}

	switch d.Kind {
	case ChartLine, ChartArea:
		for si, s := range d.Series {
			tok := seriesToken(s.Color, si)
			pts := make([]string, 0, len(s.Points))
			dots := make([]chartDot, 0, len(s.Points))
			for i, p := range s.Points {
				cx := x0 + catW*(float64(i)+0.5)
				cy := yOf(p.Value)
				pts = append(pts, fnum(cx)+","+fnum(cy))
				dots = append(dots, chartDot{CX: fnum(cx), CY: fnum(cy), Series: tok, Title: seriesTitle(s.Name, p)})
			}
			line := chartLine{Series: tok, Name: s.Name, Points: strings.Join(pts, " "), Dots: dots}
			if d.Kind == ChartArea && len(s.Points) > 0 {
				lastCx := x0 + catW*(float64(len(s.Points)-1)+0.5)
				var b strings.Builder
				b.WriteString("M " + fnum(x0+catW*0.5) + " " + fnum(baseY))
				for i, p := range s.Points {
					cx := x0 + catW*(float64(i)+0.5)
					b.WriteString(" L " + fnum(cx) + " " + fnum(yOf(p.Value)))
				}
				b.WriteString(" L " + fnum(lastCx) + " " + fnum(baseY) + " Z")
				line.Area = b.String()
			}
			lay.Lines = append(lay.Lines, line)
		}
	default:
		ns := len(d.Series)
		if ns == 0 {
			return lay
		}
		groupW := catW * 0.7
		barW := groupW / float64(ns)
		for si, s := range d.Series {
			tok := seriesToken(s.Color, si)
			for i, p := range s.Points {
				cx := x0 + catW*(float64(i)+0.5)
				gx := cx - groupW/2 + barW*float64(si)
				top := yOf(p.Value)
				y, hh := top, baseY-top
				if hh < 0 {
					y, hh = baseY, -hh
				}
				lay.Bars = append(lay.Bars, chartBar{
					X: fnum(gx), Y: fnum(y), W: fnum(barW), H: fnum(hh),
					Series: tok, Title: seriesTitle(s.Name, p),
				})
			}
		}
	}
	return lay
}

// niceCeil rounds x up to a 1/2/5 * 10^n "nice" upper bound for the value axis.
func niceCeil(x float64) float64 {
	if x <= 0 {
		return 0
	}
	exp := math.Floor(math.Log10(x))
	base := math.Pow(10, exp)
	f := x / base
	switch {
	case f <= 1:
		return base
	case f <= 2:
		return 2 * base
	case f <= 5:
		return 5 * base
	default:
		return 10 * base
	}
}

// seriesToken maps a series to a chart-N token number (1..5). A caller Color of 1..5
// is honored (wrapping past 5); 0 falls back to the series index.
func seriesToken(color, idx int) string {
	if color >= 1 {
		return strconv.Itoa(((color - 1) % 5) + 1)
	}
	return strconv.Itoa((idx % 5) + 1)
}

func seriesTitle(name string, p ChartPoint) string {
	v := fmtValue(p.Value)
	switch {
	case name != "" && p.Label != "":
		return name + " — " + p.Label + ": " + v
	case p.Label != "":
		return p.Label + ": " + v
	case name != "":
		return name + ": " + v
	default:
		return v
	}
}

// fnum formats a coordinate to at most two decimals with no trailing zeros.
func fnum(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

// fmtValue formats an axis/tooltip value: integers render without a decimal point.
func fmtValue(v float64) string {
	r := math.Round(v*100) / 100
	if r == math.Trunc(r) {
		return strconv.FormatInt(int64(r), 10)
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

// ChartProps configures the Chart root, a themed figure container. Label, when set,
// names the figure as a group.
type ChartProps struct {
	Base
	Label string
}

// ChartHeaderProps configures ChartHeader, the title/description row.
type ChartHeaderProps struct{ Base }

// ChartTitleProps configures ChartTitle. Give it a stable ID so ChartData.BaseID can
// point the SVG's aria-labelledby at a matching title element when desired.
type ChartTitleProps struct{ Base }

// ChartDescriptionProps configures ChartDescription.
type ChartDescriptionProps struct{ Base }

// ChartContentProps configures ChartContent, the SVG surface wrapper — the
// engine-neutral seam. Compose ChartSVG inside it, or a host-rendered <svg>.
type ChartContentProps struct{ Base }

// ChartLegendProps configures ChartLegend. Label names the legend list.
type ChartLegendProps struct {
	Base
	Label string
}

// ChartLegendItemProps configures one ChartLegendItem. Series selects the chart-N
// swatch color (1..5) so the legend key matches its series.
type ChartLegendItemProps struct {
	Base
	Series int
}

// ChartTableProps configures ChartTable, the native <details> tabular fallback.
// Summary is the disclosure label; it defaults to "View data as table".
type ChartTableProps struct {
	Base
	Summary string
}

func chartAttrs(p ChartProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "chart"}
	if p.Label != "" {
		owned["role"] = "group"
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func chartPartAttrs(b Base, slot string) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{"data-slot": slot})
}

func chartLegendAttrs(p ChartLegendProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "chart-legend"}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func chartLegendItemAttrs(p ChartLegendItemProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":   "chart-legend-item",
		"data-series": seriesToken(p.Series, 0),
	})
}

func chartTableSummaryText(p ChartTableProps) string {
	if p.Summary != "" {
		return p.Summary
	}
	return "View data as table"
}

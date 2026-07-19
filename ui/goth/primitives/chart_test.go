package primitives

import (
	"strings"
	"testing"
)

// GOTH-6.4 Chart (P58). These tests prove: the server-geometry model computes
// deterministic SVG coordinates over sanctioned presentation attributes; the SVG is
// a named role=img graphic with per-mark <title> tooltips; series color rides the
// data-series hook (never a rendered fill=/style=); the frame parts and legend/table
// fallback carry their data-slot contract; attribute-merge ownership holds; and no
// chart part emits an inline style= in any kind.

var chartSample = ChartData{
	BaseID:      "revenue",
	Title:       "Quarterly revenue",
	Description: "Revenue by quarter, in thousands.",
	Series: []ChartSeries{
		{Name: "2025", Color: 1, Points: []ChartPoint{
			{Label: "Q1", Value: 40}, {Label: "Q2", Value: 80},
			{Label: "Q3", Value: 60}, {Label: "Q4", Value: 100},
		}},
		{Name: "2024", Color: 2, Points: []ChartPoint{
			{Label: "Q1", Value: 30}, {Label: "Q2", Value: 50},
			{Label: "Q3", Value: 70}, {Label: "Q4", Value: 90},
		}},
	},
}

// TestNoChartPrimitiveEmitsInlineStyle proves the frozen no-server-rendered-style
// invariant across every chart part and every kind.
func TestNoChartPrimitiveEmitsInlineStyle(t *testing.T) {
	bar := chartSample
	line := chartSample
	line.Kind = ChartLine
	area := chartSample
	area.Kind = ChartArea
	outs := []string{
		render(t, ChartSVG(bar)),
		render(t, ChartSVG(line)),
		render(t, ChartSVG(area)),
		render(t, ChartSVG(ChartData{})),
		renderKids(t, Chart(ChartProps{Label: "Revenue"}), "x"),
		renderKids(t, ChartHeader(ChartHeaderProps{}), "x"),
		renderKids(t, ChartTitle(ChartTitleProps{Base: Base{ID: "revenue-title"}}), "x"),
		renderKids(t, ChartDescription(ChartDescriptionProps{}), "x"),
		renderKids(t, ChartContent(ChartContentProps{}), "x"),
		renderKids(t, ChartLegend(ChartLegendProps{Label: "Series"}), "x"),
		renderKids(t, ChartLegendItem(ChartLegendItemProps{Series: 2}), "2024"),
		renderKids(t, ChartTable(ChartTableProps{}), "x"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("chart primitive emitted an inline style=: %s", o)
		}
	}
}

// TestChartSVGBar proves the bar chart is a named role=img SVG with geometry
// presentation attributes, a data-series-keyed rect per datum, and a per-mark title.
func TestChartSVGBar(t *testing.T) {
	out := render(t, ChartSVG(chartSample))
	mustContain(t, out,
		`data-slot="chart-svg"`, `role="img"`, `viewBox="0 0 640 260"`,
		`aria-labelledby="revenue-title revenue-desc"`,
		`<title id="revenue-title">Quarterly revenue</title>`,
		`<desc id="revenue-desc">Revenue by quarter, in thousands.</desc>`,
		`class="goth-chart-bar" data-series="1"`,
		`class="goth-chart-bar" data-series="2"`,
		`<title>2025 — Q4: 100</title>`,
		`class="goth-chart-axis-line"`,
		`class="goth-chart-grid"`,
	)
	// Eight bars (two series × four points), each with x/y/width/height.
	if n := strings.Count(out, `class="goth-chart-bar"`); n != 8 {
		t.Errorf("bar count = %d, want 8", n)
	}
	mustContain(t, out, `width=`, `height=`, ` x=`, ` y=`)
	// No per-mark fill= or style= — color is CSS-only.
	mustNotContain(t, out, `fill="#`, `style=`)
	// X-axis category labels render.
	mustContain(t, out, `>Q1<`, `>Q4<`)
}

// TestChartSVGLineAndArea proves the line/area kinds emit a polyline (and, for area,
// a closed path) plus data-point circles, all keyed to data-series.
func TestChartSVGLineAndArea(t *testing.T) {
	line := chartSample
	line.Kind = ChartLine
	lo := render(t, ChartSVG(line))
	mustContain(t, lo,
		`class="goth-chart-line" data-series="1"`,
		`class="goth-chart-line" data-series="2"`,
		`points=`, `fill="none"`,
		`class="goth-chart-dot" data-series="1"`,
		`cx=`, `cy=`, `r="3"`,
	)
	mustNotContain(t, lo, `class="goth-chart-bar"`, `class="goth-chart-area"`)

	area := chartSample
	area.Kind = ChartArea
	ao := render(t, ChartSVG(area))
	mustContain(t, ao,
		`class="goth-chart-area" data-series="1"`,
		`class="goth-chart-line" data-series="1"`,
		` d=`, ` Z`,
	)
}

// TestChartLayoutGeometry proves the pure geometry model: the value axis is scaled to
// a nice ceiling, bars sit on the baseline, and coordinates fall inside the plot box.
func TestChartLayoutGeometry(t *testing.T) {
	lay := computeChartLayout(chartSample)
	if lay.ViewBox != "0 0 640 260" {
		t.Fatalf("viewBox = %q", lay.ViewBox)
	}
	if len(lay.Bars) != 8 {
		t.Fatalf("bars = %d, want 8", len(lay.Bars))
	}
	// Baseline (value 0) sits at the bottom of the plot: height 260 − padBottom 32.
	if lay.AxisY1 != "228" {
		t.Errorf("baseline y = %q, want 228", lay.AxisY1)
	}
	// Five y ticks (0..4) with a nice max of 100 → top tick label "100", bottom "0".
	if len(lay.YTicks) != chartYTicks+1 {
		t.Errorf("y ticks = %d, want %d", len(lay.YTicks), chartYTicks+1)
	}
	mustEqualTick(t, lay.YTicks[0].Text, "0")
	mustEqualTick(t, lay.YTicks[len(lay.YTicks)-1].Text, "100")
	if len(lay.XLabels) != 4 {
		t.Errorf("x labels = %d, want 4", len(lay.XLabels))
	}

	// Empty data still yields a valid axis frame and no marks.
	empty := computeChartLayout(ChartData{})
	if len(empty.Bars) != 0 || len(empty.Lines) != 0 {
		t.Errorf("empty chart should have no marks")
	}
	if empty.ViewBox != "0 0 640 260" {
		t.Errorf("empty viewBox = %q", empty.ViewBox)
	}
}

func mustEqualTick(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("tick label = %q, want %q", got, want)
	}
}

// TestNiceCeil proves the value-axis nice-number rounding.
func TestNiceCeil(t *testing.T) {
	cases := map[float64]float64{0: 0, 1: 1, 7: 10, 42: 50, 100: 100, 230: 500, 0.3: 0.5}
	for in, want := range cases {
		if got := niceCeil(in); got != want {
			t.Errorf("niceCeil(%v) = %v, want %v", in, got, want)
		}
	}
}

// TestSeriesToken proves the chart-N token mapping: explicit colors are honored and
// wrap past 5; a 0 color falls back to the series index.
func TestSeriesToken(t *testing.T) {
	cases := []struct {
		color, idx int
		want       string
	}{
		{1, 0, "1"}, {5, 0, "5"}, {6, 0, "1"}, {0, 0, "1"}, {0, 4, "5"}, {0, 5, "1"},
	}
	for _, c := range cases {
		if got := seriesToken(c.color, c.idx); got != c.want {
			t.Errorf("seriesToken(%d,%d) = %q, want %q", c.color, c.idx, got, c.want)
		}
	}
}

// TestChartKindEnum proves the kind enum defaults and membership.
func TestChartKindEnum(t *testing.T) {
	if !ChartBar.Valid() || !ChartLine.Valid() || !ChartArea.Valid() {
		t.Error("known kinds should be Valid")
	}
	if ChartKind("pie").Valid() {
		t.Error("unknown kind should not be Valid")
	}
	// The zero value is a bar chart (no lines emitted).
	if strings.Contains(render(t, ChartSVG(chartSample)), "goth-chart-line") {
		t.Error("zero-value kind should render bars, not lines")
	}
}

// TestChartFrameParts proves the compound parts carry their data-slot contract and
// the legend swatch is keyed to a series, and the table fallback defaults its label.
func TestChartFrameParts(t *testing.T) {
	root := renderKids(t, Chart(ChartProps{Base: Base{ID: "c1"}, Label: "Revenue chart"}), "x")
	mustContain(t, root, `class="goth-chart"`, `data-slot="chart"`, `id="c1"`,
		`role="group"`, `aria-label="Revenue chart"`, "<figure")

	title := renderKids(t, ChartTitle(ChartTitleProps{Base: Base{ID: "revenue-title"}}), "Quarterly revenue")
	mustContain(t, title, `data-slot="chart-title"`, `id="revenue-title"`, "Quarterly revenue")

	legend := renderKids(t, ChartLegend(ChartLegendProps{Label: "Series"}), "x")
	mustContain(t, legend, `data-slot="chart-legend"`, `aria-label="Series"`, "<ul")

	item := renderKids(t, ChartLegendItem(ChartLegendItemProps{Series: 3}), "2023")
	mustContain(t, item, `data-slot="chart-legend-item"`, `data-series="3"`,
		`data-slot="chart-swatch"`, `aria-hidden="true"`, "2023")

	tbl := renderKids(t, ChartTable(ChartTableProps{}), "<table></table>")
	mustContain(t, tbl, `data-slot="chart-table"`, "<details", "<summary", "View data as table")
	labelled := renderKids(t, ChartTable(ChartTableProps{Summary: "Show the numbers"}), "x")
	mustContain(t, labelled, "Show the numbers")
	mustNotContain(t, labelled, "View data as table")
}

// TestChartMergeHonorsOwnership proves a caller cannot overwrite an owned attribute
// or drop the compatibility class through the Base.Attributes escape hatch.
func TestChartMergeHonorsOwnership(t *testing.T) {
	out := renderKids(t, Chart(ChartProps{Label: "Sales", Base: Base{
		Class: "custom-x",
		Attributes: map[string]any{
			"data-slot":  "hijack",
			"aria-label": "evil",
			"class":      "dropped",
		},
	}}), "x")
	mustContain(t, out, `data-slot="chart"`, `aria-label="Sales"`, `goth-chart custom-x`)
	mustNotContain(t, out, "hijack", `aria-label="evil"`, "dropped")

	item := renderKids(t, ChartLegendItem(ChartLegendItemProps{Series: 2, Base: Base{
		Attributes: map[string]any{"data-series": "9", "data-slot": "hijack"},
	}}), "x")
	mustContain(t, item, `data-series="2"`, `data-slot="chart-legend-item"`)
	mustNotContain(t, item, `data-series="9"`, "hijack")
}

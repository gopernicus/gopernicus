package showcase

import (
	"strconv"
	"strings"

	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// GOTH-6.4 Chart specimen (P58). The chart ships native/no-controller: the server
// computes every scale/coordinate and ChartSVG emits a complete SVG, so the whole
// page renders on the StylesOnly profile with no JavaScript. One specimen shows the
// three server-rendered kinds (bar, line, area), each a themed frame with a title/
// description, a role=img SVG whose marks carry native <title> tooltips, a legend
// keyed to the chart-N tokens, and a native <details> table fallback composed from
// Table (P24) — the keyboard/screen-reader data path.

func registerChartSpecimens(r *Registry) {
	r.Register(Specimen{
		ID:        "primitive-chart",
		Title:     "Chart (P58)",
		Section:   SectionPrimitive,
		Primitive: "P58",
		Profile:   goth.StylesOnly,
		Body:      chartSpecimen,
	})
}

// chartSeries is the shared demo data driving all three chart kinds and the table
// fallback, so the SVG geometry and the tabular numbers are provably the same model.
var chartSeries = []primitives.ChartSeries{
	{Name: "This year", Color: 1, Points: []primitives.ChartPoint{
		{Label: "Q1", Value: 40}, {Label: "Q2", Value: 80},
		{Label: "Q3", Value: 60}, {Label: "Q4", Value: 100},
	}},
	{Name: "Last year", Color: 2, Points: []primitives.ChartPoint{
		{Label: "Q1", Value: 30}, {Label: "Q2", Value: 50},
		{Label: "Q3", Value: 70}, {Label: "Q4", Value: 90},
	}},
}

// chartCard renders a complete Chart frame for one kind: header, SVG surface, legend,
// and the composed table fallback. baseID keys the SVG title/desc for aria-labelledby.
func chartCard(baseID, title, desc string, kind primitives.ChartKind) string {
	data := primitives.ChartData{
		Kind:        kind,
		BaseID:      baseID,
		Title:       title,
		Description: desc,
		Series:      chartSeries,
	}

	header := compKids(primitives.ChartHeader(primitives.ChartHeaderProps{}),
		compKids(primitives.ChartTitle(primitives.ChartTitleProps{Base: primitives.Base{ID: baseID + "-title"}}), title)+
			compKids(primitives.ChartDescription(primitives.ChartDescriptionProps{Base: primitives.Base{ID: baseID + "-desc"}}), desc))

	surface := compKids(primitives.ChartContent(primitives.ChartContentProps{}), comp(primitives.ChartSVG(data)))

	var legendItems strings.Builder
	for _, s := range chartSeries {
		legendItems.WriteString(compKids(primitives.ChartLegendItem(primitives.ChartLegendItemProps{Series: s.Color}), s.Name))
	}
	legend := compKids(primitives.ChartLegend(primitives.ChartLegendProps{Label: "Series"}), legendItems.String())

	table := compKids(primitives.ChartTable(primitives.ChartTableProps{}), chartFallbackTable())

	return compKids(primitives.Chart(primitives.ChartProps{Base: primitives.Base{ID: baseID}, Label: title}),
		header+surface+legend+table)
}

// chartFallbackTable composes the Table (P24) primitives into the accessible tabular
// representation of chartSeries.
func chartFallbackTable() string {
	headRow := compKids(primitives.TableHead(primitives.TableHeadProps{}), "Quarter")
	for _, s := range chartSeries {
		headRow += compKids(primitives.TableHead(primitives.TableHeadProps{}), s.Name)
	}
	head := compKids(primitives.TableHeader(primitives.TableHeaderProps{}),
		compKids(primitives.TableRow(primitives.TableRowProps{}), headRow))

	var body strings.Builder
	n := len(chartSeries[0].Points)
	for i := 0; i < n; i++ {
		row := compKids(primitives.TableHead(primitives.TableHeadProps{Scope: "row"}), chartSeries[0].Points[i].Label)
		for _, s := range chartSeries {
			row += compKids(primitives.TableCell(primitives.TableCellProps{}),
				strconv.FormatFloat(s.Points[i].Value, 'f', -1, 64))
		}
		body.WriteString(compKids(primitives.TableRow(primitives.TableRowProps{}), row))
	}

	caption := compKids(primitives.TableCaption(primitives.TableCaptionProps{}), "Quarterly values by series")
	return compKids(primitives.Table(primitives.TableProps{}),
		caption+head+compKids(primitives.TableBody(primitives.TableBodyProps{}), body.String()))
}

func chartSpecimen() string {
	bar := chartCard("chart-bar", "Quarterly revenue (bar)",
		"Grouped bars: this year versus last year, in thousands.", primitives.ChartBar)
	line := chartCard("chart-line", "Quarterly revenue (line)",
		"The same series plotted as lines with data-point markers.", primitives.ChartLine)
	area := chartCard("chart-area", "Quarterly revenue (area)",
		"The same series as filled areas beneath each line.", primitives.ChartArea)

	body := `<h2>Bar</h2>` + bar + `<h2>Line</h2>` + line + `<h2>Area</h2>` + area
	return page("Chart", `<section data-slot="chart-specimen">`+body+`</section>`)
}

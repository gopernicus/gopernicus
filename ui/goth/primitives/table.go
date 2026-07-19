package primitives

import "github.com/a-h/templ"

// Table (P24, family F3) is a compound of caller-composed parts: Table (a
// <table> inside a responsive scroll wrapper), TableCaption, TableHeader,
// TableBody, TableFooter, TableRow, TableHead (column header cell), and
// TableCell. Each part takes a Props value with the shared Base. The zero value
// of each Props is valid. data-slot hooks mirror the part names.
type TableProps struct{ Base }

// TableCaptionProps configures TableCaption (<caption>).
type TableCaptionProps struct{ Base }

// TableHeaderProps configures TableHeader (<thead>).
type TableHeaderProps struct{ Base }

// TableBodyProps configures TableBody (<tbody>).
type TableBodyProps struct{ Base }

// TableFooterProps configures TableFooter (<tfoot>).
type TableFooterProps struct{ Base }

// TableRowProps configures TableRow (<tr>).
type TableRowProps struct{ Base }

// TableHeadProps configures TableHead (<th>), a column header. Scope defaults to
// "col"; set Scope to "row" (or "") to change/omit it.
type TableHeadProps struct {
	Base
	// Scope is the native th scope. The zero value is "col"; pass "none" to omit.
	Scope string
}

// TableCellProps configures TableCell (<td>).
type TableCellProps struct{ Base }

func tablePartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func tablePartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}

func tableHeadAttrs(p TableHeadProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "table-head"}
	switch p.Scope {
	case "":
		owned["scope"] = "col"
	case "none":
		// omit scope
	default:
		owned["scope"] = p.Scope
	}
	return ownedAttrs(p.Base, owned)
}

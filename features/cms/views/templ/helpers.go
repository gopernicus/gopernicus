package templ

import (
	"net/http"
	"strconv"
)

// menuOrderStr renders an int menu-order as a string for a form value attribute.
func menuOrderStr(n int) string { return strconv.Itoa(n) }

// statusText renders an HTTP status as "<code> <reason>" for error pages.
func statusText(status int) string {
	if t := http.StatusText(status); t != "" {
		return strconv.Itoa(status) + " " + t
	}
	return strconv.Itoa(status)
}

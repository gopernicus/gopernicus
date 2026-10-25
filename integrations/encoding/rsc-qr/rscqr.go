package rscqr

import (
	"fmt"

	"rsc.io/qr"

	"github.com/gopernicus/gopernicus/sdk"
)

// Modules encodes text as a QR code at level M and returns its module matrix,
// row-major, true meaning dark, without a quiet zone. An empty or over-long
// text returns an error matching sdk.ErrInvalidInput.
func Modules(text string) ([][]bool, error) {
	if text == "" {
		return nil, fmt.Errorf("%w: rscqr: empty text", sdk.ErrInvalidInput)
	}
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, fmt.Errorf("%w: rscqr: %v", sdk.ErrInvalidInput, err)
	}
	modules := make([][]bool, code.Size)
	for y := range modules {
		row := make([]bool, code.Size)
		for x := range row {
			row[x] = code.Black(x, y)
		}
		modules[y] = row
	}
	return modules, nil
}

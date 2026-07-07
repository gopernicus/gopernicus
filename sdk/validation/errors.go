package validation

import (
	"errors"
	"strings"
)

// Errors collects validation errors. Use Add to accumulate errors from
// individual validators, then Err to get a combined result. It serves
// non-HTTP contexts; HTTP handlers accumulate into web.FieldErrors instead
// (see the package doc for the composition recipe).
//
//	var errs validation.Errors
//	errs.Add(validation.Required("name", req.Name))
//	errs.Add(validation.Email("email", req.Email))
//	errs.Add(validation.MaxLength("bio", req.Bio, 500))
//	if err := errs.Err(); err != nil {
//	    return err
//	}
type Errors []error

// Add appends the error if non-nil.
func (e *Errors) Add(err error) {
	if err != nil {
		*e = append(*e, err)
	}
}

// Err returns nil if no errors were collected, or a combined error.
func (e Errors) Err() error {
	if len(e) == 0 {
		return nil
	}
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}
	return errors.New(strings.Join(msgs, "; "))
}

// HasErrors returns true if any errors were collected.
func (e Errors) HasErrors() bool {
	return len(e) > 0
}

// All returns the individual errors.
func (e Errors) All() []error {
	return []error(e)
}

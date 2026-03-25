package validation

import (
	"errors"
	"strings"
)

// Errors collects validation errors. Use Add to accumulate errors from
// individual validators, then Err to get a combined result.
//
//	var errs validation.Errors
//	errs.Add(validation.Required("name", req.Name))
//	errs.Add(validation.Email("email", req.Email))
//	errs.Add(validation.MaxLength("bio", req.Bio, 500))
//	if err := errs.Err(); err != nil {
//	    return web.ErrBadRequest(err.Error())
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

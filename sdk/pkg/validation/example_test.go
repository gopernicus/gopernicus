package validation_test

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/validation"
)

func ExampleRequired() {
	validate := func(name, email string) error {
		var problems sdk.ValidationError
		problems.AddViolation(validation.Required("name", name))
		problems.AddViolation(validation.Email("email", email))
		return problems.Err()
	}
	fmt.Println(validate("Alice", "alice@example.com"))
	fmt.Println(validate("", "not-an-email"))
	// Output:
	// <nil>
	// name: name is required (and 1 more)
}

func ExampleIfSet() {
	nickname := "é"
	var problems sdk.ValidationError
	problems.AddViolation(validation.IfSet(&nickname, func(value string) *sdk.Violation {
		return validation.MinLength("nickname", value, 2)
	}))
	fmt.Println(problems.Err())
	// Output:
	// nickname: nickname must be at least 2 characters
}

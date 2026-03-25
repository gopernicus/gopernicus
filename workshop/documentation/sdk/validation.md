# sdk/validation -- Validation Framework Reference

Package `validation` provides simple, reflection-free validation functions. All validators follow a consistent pattern: field name, value, optional parameters, and return `nil` if valid or an error describing the problem.

**Import:** `github.com/gopernicus/gopernicus/sdk/validation`

## Core Pattern

Empty/nil values pass all validators except `Required`. Compose `Required` with other validators to enforce presence:

```go
var errs validation.Errors
errs.Add(validation.Required("email", req.Email))
errs.Add(validation.Email("email", req.Email))
errs.Add(validation.MaxLength("bio", req.Bio, 500))
if err := errs.Err(); err != nil {
    return err
}
```

## Errors Type

`Errors` collects validation errors. It is a `[]error` with helper methods.

```go
type Errors []error
```

| Method | Description |
|---|---|
| `Add(err error)` | Appends the error if non-nil |
| `Err() error` | Returns nil if empty, or a combined error (messages joined by "; ") |
| `HasErrors() bool` | Returns true if any errors collected |
| `All() []error` | Returns the individual errors |

## String Validators

| Function | Signature | Description |
|---|---|---|
| `Required` | `(field, value string) error` | Non-empty after trimming whitespace |
| `RequiredPtr` | `(field string, value *string) error` | Non-nil and non-empty |
| `MinLength` | `(field, value string, min int) error` | At least `min` characters |
| `MaxLength` | `(field, value string, max int) error` | At most `max` characters |
| `OneOf` | `(field, value string, allowed ...string) error` | Value in allowed set |
| `Email` | `(field, value string) error` | Valid email (via `net/mail`) |
| `UUID` | `(field, value string) error` | Valid UUID format |
| `URL` | `(field, value string) error` | Valid absolute URL |
| `Slug` | `(field, value string) error` | Lowercase letters, numbers, hyphens |
| `Matches` | `(field, value string, pattern *regexp.Regexp, msg string) error` | Matches regex |
| `PasswordStrength` | `(field, value string) error` | 8+ chars, upper, lower, digit, special |
| `PasswordsMatch` | `(password, confirm string) error` | Two strings are identical |

All string validators have `*Ptr` variants (e.g., `MinLengthPtr`, `EmailPtr`) that accept `*string` and skip validation when nil.

## Numeric Validators

| Function | Signature | Description |
|---|---|---|
| `Min` | `(field string, value, min int) error` | Value >= min |
| `Max` | `(field string, value, max int) error` | Value <= max |
| `Range` | `(field string, value, min, max int) error` | Value within [min, max] |
| `Positive` | `(field string, value int) error` | Value > 0 |

`MinPtr`, `MaxPtr`, and `PositivePtr` variants accept `*int`.

## Collection Validators

| Function | Signature | Description |
|---|---|---|
| `NotEmpty[T]` | `(field string, slice []T) error` | At least one element |
| `MinItems[T]` | `(field string, slice []T, min int) error` | At least `min` elements |
| `MaxItems[T]` | `(field string, slice []T, max int) error` | At most `max` elements |

## Optional Field Validation (IfSet)

For optional pointer fields with custom validators, use `IfSet` instead of writing Ptr variants:

```go
errs.Add(validation.IfSet(req.Nickname, func(v string) error {
    return validation.MinLength("nickname", v, 3)
}))
```

## Writing Custom Validators

Add custom validators to `validation/custom.go`. Follow the same pattern:

```go
func HexColor(field, value string) error {
    if value == "" {
        return nil // empty passes, compose with Required
    }
    if !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(value) {
        return fmt.Errorf("%s must be a valid hex color", field)
    }
    return nil
}
```

## Integration with DecodeJSON

When a request type implements `Validate() error`, `web.DecodeJSON` calls it automatically after unmarshaling. The returned error is passed to `web.ErrValidation(err)` for the HTTP response.

```go
type CreateUserRequest struct {
    Email    string `json:"email"`
    Password string `json:"password"`
}

func (r *CreateUserRequest) Validate() error {
    var errs validation.Errors
    errs.Add(validation.Required("email", r.Email))
    errs.Add(validation.Email("email", r.Email))
    errs.Add(validation.Required("password", r.Password))
    errs.Add(validation.PasswordStrength("password", r.Password))
    return errs.Err()
}
```

In the handler:

```go
req, err := web.DecodeJSON[CreateUserRequest](r)
if err != nil {
    web.RespondJSONError(w, web.ErrValidation(err))
    return
}
```

## Related

- [sdk/web](../sdk/web.md) -- `DecodeJSON` auto-validates, `ErrValidation` formats errors
- [sdk/errs](../sdk/errs.md) -- domain errors returned after validation passes

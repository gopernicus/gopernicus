---
sidebar_position: 9
title: Validation
---

# SDK — Validation

`sdk/validation` provides reflection-free field validators and an error accumulator for request input validation. It wraps `net/mail`, `net/url`, `regexp`, `strings`, `slices`, and `unicode` from the standard library.

## The pattern

All validators take a field name and value and return `nil` or an error. Collect results into `Errors`, then check once at the end:

```go
var errs validation.Errors
errs.Add(validation.Required("name", req.Name))
errs.Add(validation.Email("email", req.Email))
errs.Add(validation.MaxLength("bio", req.Bio, 500))
if err := errs.Err(); err != nil {
    return web.ErrBadRequest(err.Error())
}
```

`Errors.Add` ignores nil, so passing validators have no effect on the accumulator.

## Empty value behavior

Empty strings and nil pointers **pass all validators except `Required`/`RequiredPtr`**. This lets you compose presence and format checks independently:

```go
// optional — only validated if provided
errs.Add(validation.Email("email", req.Email))

// required — must be present and valid
errs.Add(validation.Required("email", req.Email))
errs.Add(validation.Email("email", req.Email))
```

## Pointer variants

Every validator has a `Ptr` variant that skips validation when the pointer is nil:

```go
errs.Add(validation.EmailPtr("email", req.Email))       // *string, nil passes
errs.Add(validation.MinLengthPtr("name", req.Name, 2))
errs.Add(validation.MinPtr("age", req.Age, 18))         // *int, nil passes
```

For optional fields with custom validators, use `IfSet` instead of writing a new `Ptr` variant:

```go
errs.Add(validation.IfSet(req.Nickname, func(v string) error {
    return validation.MinLength("nickname", v, 3)
}))
```

## Built-in validators

**Strings**

| Validator | Checks |
|---|---|
| `Required(field, value)` | Non-empty after trimming whitespace |
| `MinLength(field, value, n)` | At least `n` characters |
| `MaxLength(field, value, n)` | At most `n` characters |
| `OneOf(field, value, ...allowed)` | Value is in the allowed set |
| `Email(field, value)` | Valid email address |
| `UUID(field, value)` | Valid UUID format |
| `URL(field, value)` | Valid absolute URL |
| `Slug(field, value)` | Lowercase letters, numbers, hyphens |
| `Matches(field, value, pattern, msg)` | Matches a regex |

**Numerics**

| Validator | Checks |
|---|---|
| `Min(field, value, n)` | At least `n` |
| `Max(field, value, n)` | At most `n` |
| `Range(field, value, min, max)` | Within range inclusive |
| `Positive(field, value)` | Greater than zero |

**Collections**

| Validator | Checks |
|---|---|
| `NotEmpty(field, slice)` | At least one element |
| `MinItems(field, slice, n)` | At least `n` elements |
| `MaxItems(field, slice, n)` | At most `n` elements |

**Passwords**

```go
errs.Add(validation.PasswordStrength("password", req.Password))
// requires: 8+ chars, uppercase, lowercase, digit, special character

errs.Add(validation.PasswordsMatch(req.Password, req.Confirm))
```

> **Note:** `validation.PasswordStrength` enforces complexity rules (uppercase, lowercase, digit, special character) and is designed for user-facing registration forms. The core authenticator uses a separate `authentication.ValidatePassword` that follows [NIST SP 800-63B](https://pages.nist.gov/800-63-3/) — minimum 8 characters, maximum 72, no complexity rules. Use whichever policy fits your context.

## Custom validators

`custom.go` is the intended home for app-specific validators. Follow the same convention — field name first, nil for valid:

```go
func HexColor(field, value string) error {
    if value == "" {
        return nil
    }
    if !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(value) {
        return fmt.Errorf("%s must be a valid hex color", field)
    }
    return nil
}
```

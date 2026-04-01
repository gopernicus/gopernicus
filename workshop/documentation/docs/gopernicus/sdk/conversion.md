---
sidebar_position: 3
title: Conversion
---

# SDK — Conversion

`sdk/conversion` is a collection of small, focused utilities for type conversion, pointer handling, string case transformations, date parsing, and URL slugging. It wraps `strings`, `unicode`, `encoding/json`, `regexp`, and `time` from the standard library.

## Pointers

Go's requirement to take the address of a value before assigning it to a pointer field is verbose. Three generics cover the common patterns:

```go
// Ptr — create a pointer to any literal or value
post.Title = conversion.Ptr("Hello")
filter.Limit = conversion.Ptr(25)

// Deref — safely dereference; returns zero value if nil
title := conversion.Deref(post.Title)  // "" if nil

// DerefOr — dereference with an explicit fallback
limit := conversion.DerefOr(filter.Limit, 25)
```

## Case conversion

Converts between the naming conventions used across Go structs, JSON, URLs, and generated code.

```go
conversion.ToPascalCase("user_id")    // "UserID"
conversion.ToCamelCase("user_id")     // "userID"
conversion.ToSnakeCase("AuthAPIKey")  // "auth_api_key"
conversion.ToKebabCase("auth_sessions") // "auth-sessions"
conversion.ToLowerSpaced("ContentTag")  // "content tag"
```

Common acronyms (`ID`, `URL`, `API`, `HTTP`, `JSON`, `SQL`, `UUID`) are handled automatically. Register additional ones with `AddAcronym`:

```go
conversion.AddAcronym("JWT")
conversion.ToPascalCase("jwt_token") // "JWTToken"
```

## Date parsing

`ParseDateTime` is the right choice when you control the input format (API requests, structured data). It accepts RFC3339, ISO8601, and a few common variants:

```go
t, err := conversion.ParseDateTime("2024-01-15T10:30:00Z")
t, err := conversion.ParseDateTime("2024-01-15")
```

`ParseFlexibleDate` accepts a wider range of formats including US and European date conventions, but those are ambiguous — `01/02/2024` could be January 2nd or February 1st. US formats are tried first.

```go
// Use only when the source format is uncontrolled (user input, legacy imports)
t, err := conversion.ParseFlexibleDate("01/15/2024")
```

Prefer `ParseDateTime` for new code.

## JSON helpers

Safe wrappers for `*json.RawMessage` fields that may be nil or null:

```go
// Always returns a valid JSON object — never nil, never "null"
data := conversion.JSONOrEmptyObject(record.Metadata) // "{}" if nil/null

// Returns raw bytes, "{}" if nil
b := conversion.JSONOrEmpty(record.Metadata)
```

## Slices

`Overlap` filters a requested slice to only the items present in an allowed set. Useful for permission scoping and field filtering:

```go
requested := []string{"name", "email", "password_hash"}
allowed   := []string{"name", "email", "avatar_url"}

safe := conversion.Overlap(requested, allowed) // ["name", "email"]
```

## URL slugs

```go
conversion.ToURLSlug("Hello World!")  // "hello-world"
conversion.ToURLSlug("Café Résumé")   // "cafe-resume"
```

Handles accented characters, special characters, leading/trailing whitespace, and consecutive separators.

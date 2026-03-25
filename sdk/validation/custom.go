package validation

// Add your own validators to this file. Follow the same pattern as the builtins:
//
//   - Take the field name as the first parameter.
//   - Return nil for valid, error for invalid.
//   - Empty/nil values should pass (compose with Required for presence checks).
//   - Use IfSet[T] for optional pointer fields instead of writing Ptr variants.
//
// Example:
//
//	func HexColor(field, value string) error {
//	    if value == "" {
//	        return nil
//	    }
//	    if !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(value) {
//	        return fmt.Errorf("%s must be a valid hex color", field)
//	    }
//	    return nil
//	}

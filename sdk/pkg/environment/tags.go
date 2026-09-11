package environment

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ParseEnvTags populates a struct's fields from environment variables based on
// struct tags.
//
// Supported tags:
//   - `env:"KEY"` — environment variable name (namespaced via GetNamespaceEnvKey)
//   - `default:"value"` — value used when the env var is unset or empty and the field is zero
//   - `separator:","` — separator for slice values (defaults to comma)
//   - `required:"true"` — error when the env var is unset or empty
//
// Precedence: env var > existing non-zero field value > default tag value. Hosts
// building environments via struct literal are unaffected until they opt in.
// An explicit false, zero, or empty slice cannot override a nonzero default tag.
//
// An environment variable that is set to an empty value (KEY=) is treated
// exactly like one that is not set: required:"true" errors, a zero field takes
// its default tag, and a pre-seeded field keeps its value. A host that needs to
// blank a defaulted field must do so in code. GetEnvOrDefault does NOT share
// this rule — it keeps raw LookupEnv semantics, where KEY= is a set, empty
// value.
//
// Supported field kinds: string, int, int64 (including time.Duration), bool,
// float32/float64, and slices of strings (including named string types). cfg
// must be a pointer to a struct, and any tagged field of an unsupported kind is an error — including a struct field
// carrying an env tag. An exported, settable struct-kind field WITHOUT an env
// tag is instead recursed into under the same namespace, so a nested config
// struct's own tags apply with no prefix of their own; a struct type with no
// exported fields (time.Time) recurses to a no-op. Pointer-to-struct and
// interface fields without env tags are skipped. Parsing does not validate enum
// values or application rules. Fields already populated remain changed if a
// later field fails. Errors include the key, field path, and type, but not values.
func ParseEnvTags(namespace string, cfg any) error {
	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return errors.New("cfg must be a pointer to a struct")
	}

	return parseEnvStruct(namespace, v.Elem(), "")
}

func parseEnvStruct(namespace string, v reflect.Value, prefix string) error {
	t := v.Type()

	for i := range v.NumField() {
		field := v.Field(i)
		fieldType := t.Field(i)

		if !field.CanSet() {
			continue
		}

		fieldPath := prefix + fieldType.Name
		envKey := fieldType.Tag.Get("env")
		if envKey == "" {
			if field.Kind() == reflect.Struct {
				if err := parseEnvStruct(namespace, field, fieldPath+"."); err != nil {
					return err
				}
			}
			continue
		}

		defaultValue := fieldType.Tag.Get("default")
		separator := fieldType.Tag.Get("separator")
		required := fieldType.Tag.Get("required") == "true"

		key := GetNamespaceEnvKey(namespace, envKey)
		if !supportedFieldType(field.Type()) {
			return fmt.Errorf("environment variable %s (field %s, type %s): unsupported field type", key, fieldPath, field.Type())
		}
		value, exists := os.LookupEnv(key)

		if !exists || value == "" {
			if required {
				return fmt.Errorf("environment variable %s (field %s, type %s): required value is not set", key, fieldPath, field.Type())
			}
			if isZeroValue(field) && defaultValue != "" {
				value = defaultValue
			} else {
				continue
			}
		}

		if err := setFieldValue(field, value, separator); err != nil {
			return fmt.Errorf("environment variable %s (field %s, type %s): %w", key, fieldPath, field.Type(), err)
		}
	}

	return nil
}

func supportedFieldType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String, reflect.Int, reflect.Int64, reflect.Bool, reflect.Float32, reflect.Float64:
		return true
	case reflect.Slice:
		return t.Elem().Kind() == reflect.String
	default:
		return false
	}
}

func isZeroValue(v reflect.Value) bool {
	if v.Kind() == reflect.Slice {
		return v.Len() == 0
	}
	return v.IsZero()
}

func setFieldValue(field reflect.Value, value, separator string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)

	case reflect.Int, reflect.Int64:
		if field.Type() == reflect.TypeFor[time.Duration]() {
			duration, err := time.ParseDuration(value)
			if err != nil {
				return errors.New("invalid duration")
			}
			field.SetInt(int64(duration))
			return nil
		}
		intVal, err := strconv.ParseInt(value, 10, field.Type().Bits())
		if err != nil {
			return numberErrorCause(err)
		}
		field.SetInt(intVal)

	case reflect.Bool:
		boolVal, err := strconv.ParseBool(value)
		if err != nil {
			return numberErrorCause(err)
		}
		field.SetBool(boolVal)

	case reflect.Float32, reflect.Float64:
		floatVal, err := strconv.ParseFloat(value, field.Type().Bits())
		if err != nil {
			return numberErrorCause(err)
		}
		field.SetFloat(floatVal)

	case reflect.Slice:
		if separator == "" {
			separator = ","
		}
		parts := strings.Split(value, separator)
		values := reflect.MakeSlice(field.Type(), len(parts), len(parts))
		for i, part := range parts {
			values.Index(i).SetString(strings.TrimSpace(part))
		}
		field.Set(values)

	default:
		return fmt.Errorf("unsupported field type: %s", field.Kind())
	}

	return nil
}

// strconv errors retain the input value. Keep only their value-free cause.
func numberErrorCause(err error) error {
	if errors.Is(err, strconv.ErrRange) {
		return strconv.ErrRange
	}
	return strconv.ErrSyntax
}

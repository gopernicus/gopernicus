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
//   - `default:"value"` — value used when the env var is unset and the field is zero
//   - `separator:","` — separator for slice values (defaults to comma)
//   - `required:"true"` — error when the env var is unset
//
// Precedence: env var > existing non-zero field value > default tag value. Hosts
// building environments via struct literal are unaffected until they opt in.
//
// Supported field kinds: string, int, int64 (including time.Duration), bool,
// float32/float64, and []string. cfg must be a pointer to a struct, and any
// tagged field of an unsupported kind is an error.
func ParseEnvTags(namespace string, cfg any) error {
	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return errors.New("cfg must be a pointer to a struct")
	}

	v = v.Elem()
	t := v.Type()

	for i := range v.NumField() {
		field := v.Field(i)
		fieldType := t.Field(i)

		if !field.CanSet() {
			continue
		}

		envKey := fieldType.Tag.Get("env")
		if envKey == "" {
			continue
		}

		defaultValue := fieldType.Tag.Get("default")
		separator := fieldType.Tag.Get("separator")
		required := fieldType.Tag.Get("required") == "true"

		key := GetNamespaceEnvKey(namespace, envKey)
		value, exists := os.LookupEnv(key)

		if !exists {
			if required {
				return fmt.Errorf("required environment variable %s is not set", key)
			}
			if isZeroValue(field) && defaultValue != "" {
				value = defaultValue
			} else {
				continue
			}
		}

		if err := setFieldValue(field, value, separator); err != nil {
			return fmt.Errorf("error setting field %s: %w", fieldType.Name, err)
		}
	}

	return nil
}

func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Int, reflect.Int64:
		return v.Int() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Slice:
		return v.IsNil() || v.Len() == 0
	default:
		zero := reflect.Zero(v.Type())
		return reflect.DeepEqual(v.Interface(), zero.Interface())
	}
}

func setFieldValue(field reflect.Value, value, separator string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)

	case reflect.Int, reflect.Int64:
		if value == "" {
			return nil
		}
		if field.Type() == reflect.TypeFor[time.Duration]() {
			duration, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("cannot parse duration: %w", err)
			}
			field.SetInt(int64(duration))
			return nil
		}
		intVal, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("cannot parse int: %w", err)
		}
		field.SetInt(intVal)

	case reflect.Bool:
		if value == "" {
			return nil
		}
		boolVal, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("cannot parse bool: %w", err)
		}
		field.SetBool(boolVal)

	case reflect.Float32, reflect.Float64:
		if value == "" {
			return nil
		}
		floatVal, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("cannot parse float: %w", err)
		}
		field.SetFloat(floatVal)

	case reflect.Slice:
		if field.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice type: %s", field.Type())
		}
		if value == "" {
			return nil
		}
		if separator == "" {
			separator = ","
		}
		parts := strings.Split(value, separator)
		stringSlice := make([]string, len(parts))
		for i, part := range parts {
			stringSlice[i] = strings.TrimSpace(part)
		}
		field.Set(reflect.ValueOf(stringSlice))

	default:
		return fmt.Errorf("unsupported field type: %s", field.Kind())
	}

	return nil
}

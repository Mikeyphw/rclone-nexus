package redact

import (
	"encoding/json"
	"reflect"
	"strings"
	"unicode/utf8"
)

const Redacted = "<redacted>"

var sensitiveFragments = []string{
	"password", "passwd", "secret", "token", "credential", "authorization",
	"cookie", "client_secret", "refresh_token", "access_token", "api_key", "apikey",
}

func SensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, fragment := range sensitiveFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func BoundedString(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	if maxBytes < 16 {
		return value[:maxBytes]
	}
	limit := maxBytes - len("…<truncated>")
	if limit < 0 {
		limit = 0
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "…<truncated>"
}

func Value(value any, maxStringBytes int) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]any:
		return mapAny(typed, maxStringBytes)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = Value(item, maxStringBytes)
		}
		return out
	case string:
		return BoundedString(typed, maxStringBytes)
	}

	// Results often arrive as typed structs or map[string]string. Normalize only
	// composite values through JSON before applying the same recursive policy so
	// sensitive JSON field names cannot bypass redaction because of Go type.
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return value
	}
	switch rv.Kind() {
	case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array, reflect.Pointer, reflect.Interface:
		encoded, err := json.Marshal(value)
		if err != nil {
			return value
		}
		var generic any
		if err := json.Unmarshal(encoded, &generic); err != nil {
			return value
		}
		return Value(generic, maxStringBytes)
	default:
		return value
	}
}

func mapAny(value map[string]any, maxStringBytes int) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		if SensitiveKey(key) {
			out[key] = Redacted
			continue
		}
		out[key] = Value(item, maxStringBytes)
	}
	return out
}

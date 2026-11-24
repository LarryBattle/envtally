package group

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Lookup reads a dot-path such as "git.commit.id" from decoded JSON.
// It handles nested objects ({"git":{"commit":{"id":..}}}) and flat keys
// that contain dots ({"git.commit.id":..}). Null, empty and absent values
// count as missing.
func Lookup(doc any, path string) (string, bool) {
	value, ok := walk(doc, strings.Split(path, "."))
	if !ok {
		return "", false
	}
	return scalar(value)
}

func walk(node any, segments []string) (any, bool) {
	if len(segments) == 0 {
		return node, true
	}
	object, ok := node.(map[string]any)
	if !ok {
		return nil, false
	}
	for i := 1; i <= len(segments); i++ {
		child, ok := object[strings.Join(segments[:i], ".")]
		if !ok {
			continue
		}
		if value, ok := walk(child, segments[i:]); ok {
			return value, true
		}
	}
	return nil, false
}

func scalar(value any) (string, bool) {
	switch v := value.(type) {
	case nil:
		return "", false
	case string:
		return v, v != ""
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	default: // object or array: compact JSON still compares reliably
		data, err := json.Marshal(v)
		return string(data), err == nil
	}
}

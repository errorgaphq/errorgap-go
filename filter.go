package errorgap

import (
	"fmt"
	"strings"
)

const (
	filteredValue  = "[FILTERED]"
	truncatedValue = "[TRUNCATED]"
	maxStringLen   = 4096
	maxItems       = 128
	maxDepth       = 8
)

// FilterParams masks sensitive keys (case-insensitive substring match) at
// any depth, inside slices too, and cuts long strings and large or deeply
// nested collections.
func FilterParams(params map[string]any, filterKeys []string) map[string]any {
	if params == nil {
		return map[string]any{}
	}
	lowered := make([]string, len(filterKeys))
	for i, k := range filterKeys {
		lowered[i] = strings.ToLower(k)
	}
	return walkMap(params, lowered, true, 0)
}

// cleanMap truncates without masking anything.
func cleanMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	return walkMap(in, nil, false, 0)
}

func truncateString(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "") + truncatedValue
}

func walkMap(in map[string]any, lowered []string, mask bool, depth int) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if len(out) >= maxItems {
			break
		}
		if mask && isSensitive(k, lowered) {
			out[k] = filteredValue
			continue
		}
		out[k] = walkValue(v, lowered, mask, depth+1)
	}
	return out
}

func walkValue(v any, lowered []string, mask bool, depth int) any {
	switch value := v.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return value
	case string:
		return truncateString(value, maxStringLen)
	case error:
		return truncateString(value.Error(), maxStringLen)
	case fmt.Stringer:
		return truncateString(value.String(), maxStringLen)
	case map[string]any:
		if depth >= maxDepth {
			return truncatedValue
		}
		return walkMap(value, lowered, mask, depth)
	case map[string]string:
		if depth >= maxDepth {
			return truncatedValue
		}
		converted := make(map[string]any, len(value))
		for k, item := range value {
			converted[k] = item
		}
		return walkMap(converted, lowered, mask, depth)
	case []any:
		if depth >= maxDepth {
			return truncatedValue
		}
		if len(value) > maxItems {
			value = value[:maxItems]
		}
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = walkValue(item, lowered, mask, depth+1)
		}
		return out
	case []map[string]any:
		items := make([]any, len(value))
		for i, item := range value {
			items[i] = item
		}
		return walkValue(items, lowered, mask, depth)
	case []string:
		items := make([]any, len(value))
		for i, item := range value {
			items[i] = item
		}
		return walkValue(items, lowered, mask, depth)
	default:
		// Structs and other values are left to encoding/json; if they turn
		// out not to be encodable the client falls back to their %v form.
		return v
	}
}

func isSensitive(key string, lowered []string) bool {
	lk := strings.ToLower(key)
	for _, needle := range lowered {
		if needle != "" && strings.Contains(lk, needle) {
			return true
		}
	}
	return false
}

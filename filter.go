package errorgap

import "strings"

const filteredValue = "[FILTERED]"

// FilterParams masks sensitive keys (case-insensitive substring match) in
// a params map. Nested maps are walked; arrays/slices are not recursed into.
func FilterParams(params map[string]any, filterKeys []string) map[string]any {
	if params == nil {
		return map[string]any{}
	}
	lowered := make([]string, len(filterKeys))
	for i, k := range filterKeys {
		lowered[i] = strings.ToLower(k)
	}
	return walkMap(params, lowered)
}

func walkMap(in map[string]any, lowered []string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if isSensitive(k, lowered) {
			out[k] = filteredValue
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = walkMap(nested, lowered)
			continue
		}
		out[k] = v
	}
	return out
}

func isSensitive(key string, lowered []string) bool {
	lk := strings.ToLower(key)
	for _, needle := range lowered {
		if strings.Contains(lk, needle) {
			return true
		}
	}
	return false
}

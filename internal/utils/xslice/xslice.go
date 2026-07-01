package xslice

// Unique removes duplicate elements from a slice while preserving order.
// Works with any comparable type (int, string, float64, etc.).
func Unique[T comparable](items []T) []T {
	if len(items) == 0 {
		return items
	}
	seen := make(map[T]struct{}, len(items))
	out := make([]T, 0, len(items))
	for _, item := range items {
		if _, exists := seen[item]; !exists {
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

// UniqueFunc removes duplicate elements using a custom key function.
// Useful for deduplicating by a specific field in structs.
func UniqueFunc[T any, K comparable](items []T, key func(T) K) []T {
	if len(items) == 0 {
		return items
	}
	seen := make(map[K]struct{}, len(items))
	out := make([]T, 0, len(items))
	for _, item := range items {
		k := key(item)
		if _, exists := seen[k]; !exists {
			seen[k] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

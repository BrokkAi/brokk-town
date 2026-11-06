// Package pathcmp compares filesystem paths as Git prints them.
package pathcmp

import "path/filepath"

// Same reports whether a and b name the same path once both are cleaned.
func Same(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

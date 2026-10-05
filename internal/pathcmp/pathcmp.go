// Package pathcmp compares filesystem paths as Git prints them.
package pathcmp

import (
	"path/filepath"
	"runtime"
	"strings"
)

// Same reports whether a and b name the same path. Git prints forward slashes
// on Windows and drive letters in either case, so paths are cleaned and, on
// Windows, compared without case.
func Same(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

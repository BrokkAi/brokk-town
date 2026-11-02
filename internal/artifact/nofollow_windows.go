//go:build windows

package artifact

// NoFollow has no open flag on Windows. Callers check PathSafe first, and
// creating a symbolic link there requires privilege or Developer Mode.
const NoFollow = 0

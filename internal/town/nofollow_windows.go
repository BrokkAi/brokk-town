//go:build windows

package town

// oNoFollow has no open flag on Windows. Callers check storagePathSafe first,
// and creating a symbolic link there requires privilege or Developer Mode.
const oNoFollow = 0

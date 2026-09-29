//go:build unix

// Package durable persists directory entries after a create or rename.
package durable

import "os"

// SyncDir flushes the entries of directory path, so a file just created or
// renamed into it survives a crash.
func SyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

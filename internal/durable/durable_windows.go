//go:build windows

// Package durable persists directory entries after a create or rename.
package durable

// SyncDir has nothing to do on Windows: a directory handle cannot be flushed,
// and NTFS journals the metadata of a create or rename itself.
func SyncDir(path string) error { return nil }

// Package artifact holds the filesystem checks shared by everything that
// measures, verifies or removes retained data: the service for its own
// directories and each bot for the workspace it owns. Both sides must agree on
// what a safe path is and what a transcript's digest is, so they use one copy.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxTranscriptBytes bounds the size of a transcript that can be verified.
const MaxTranscriptBytes = 128 << 20

// SafeRel reports whether path is a clean relative path that stays below its
// base.
func SafeRel(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator)) && !strings.ContainsAny(path, "\x00\r\n")
}

// PathSafe reports whether path lies strictly below root and reaches it
// without passing through a symbolic link.
func PathSafe(root, path string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || !SafeRel(rel) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(pathAbs)
	if err != nil {
		return false
	}
	base, err := filepath.EvalSymlinks(rootAbs)
	return err == nil && filepath.Clean(resolved) == filepath.Join(base, rel)
}

// Measure walks path and reports its total size, file count and newest
// modification time.
func Measure(ctx context.Context, path string) (size int64, files int, modified time.Time, err error) {
	visited := 0
	err = filepath.WalkDir(path, func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 200000 {
			return errors.New("inventory entry limit")
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if info.ModTime().After(modified) {
			modified = info.ModTime()
		}
		if !entry.IsDir() {
			size += info.Size()
			files++
		}
		return nil
	})
	return
}

// Digest hashes one regular file without following a final symbolic link, and
// fails if the file changes while it is read.
func Digest(ctx context.Context, path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, errors.New("artifact is not a regular file")
	}
	if info.Size() > MaxTranscriptBytes {
		return "", 0, errors.New("artifact exceeds the verification limit")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|NoFollow, 0)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	hash := sha256.New()
	var total int64
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		n, err := f.Read(buffer)
		total += int64(n)
		if total > MaxTranscriptBytes {
			return "", 0, errors.New("artifact exceeds the verification limit")
		}
		_, _ = hash.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(info, after) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", 0, errors.New("artifact changed during inspection")
	}
	return hex.EncodeToString(hash.Sum(nil)), total, nil
}

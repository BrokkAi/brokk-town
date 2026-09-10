package harness

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func localPath(name string) bool {
	p := path.Clean(name)
	return p != "." && p != ".." && !strings.HasPrefix(p, "../") && !path.IsAbs(p) && !strings.ContainsAny(p, "\x00\\:")
}

// Launch prepares a command; it never starts it. Versioned archives are installed
// atomically and locked across concurrent workers, with no shell interpolation.
func Launch(ctx context.Context, root string, e Entry) ([]string, map[string]string, error) {
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !safeHTTPS(r.URL.String()) {
			return errors.New("unsafe archive redirect")
		}
		return nil
	}}
	return launch(ctx, root, e, client)
}
func launch(ctx context.Context, root string, e Entry, client *http.Client) ([]string, map[string]string, error) {
	if err := e.Validate(); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(e.Command) > 0 {
		p, err := exec.LookPath(e.Command[0])
		if err != nil {
			return nil, nil, fmt.Errorf("%s is not on the service PATH. %s", e.Name, e.Setup)
		}
		return append([]string{p}, e.Command[1:]...), nil, nil
	}
	for _, option := range []struct {
		bin string
		p   *Package
	}{{"npx", e.Distribution.Npx}, {"uvx", e.Distribution.Uvx}} {
		if option.p == nil {
			continue
		}
		bin, err := exec.LookPath(option.bin)
		if err != nil {
			return nil, nil, fmt.Errorf("%s needs %s on the service PATH", e.Name, option.bin)
		}
		args := []string{bin}
		if option.bin == "npx" {
			args = append(args, "--yes")
		}
		args = append(args, "--", option.p.Package)
		args = append(args, option.p.Args...)
		return args, option.p.Env, nil
	}
	b, ok := e.Distribution.Binary[Platform()]
	if !ok {
		return nil, nil, fmt.Errorf("%s has no registry distribution for %s", e.Name, Platform())
	}
	if root == "" {
		return nil, nil, errors.New("private harness cache directory is required")
	}
	base, err := filepath.Abs(filepath.Join(root, "harnesses", "installed"))
	if err != nil {
		return nil, nil, err
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		return nil, nil, err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return nil, nil, err
	}
	id := identity(e) + "-" + Platform()
	lock, err := os.OpenFile(filepath.Join(base, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, nil, err
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil, err
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	dest := filepath.Join(base, id)
	command := filepath.Join(dest, filepath.FromSlash(b.Cmd))
	if _, err = os.Stat(filepath.Join(dest, ".town-install")); errors.Is(err, os.ErrNotExist) {
		stage, err := os.MkdirTemp(base, ".install-*")
		if err != nil {
			return nil, nil, err
		}
		defer os.RemoveAll(stage)
		archive, err := download(ctx, client, b, base)
		if err != nil {
			return nil, nil, err
		}
		defer os.Remove(archive)
		if err = unpack(ctx, archive, b.Archive, stage, b.Cmd); err != nil {
			return nil, nil, fmt.Errorf("install %s: %w", e.Name, err)
		}
		if err = executable(stage, b.Cmd); err != nil {
			return nil, nil, err
		}
		if err = os.WriteFile(filepath.Join(stage, ".town-install"), []byte(id), 0600); err != nil {
			return nil, nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		if err = os.Rename(stage, dest); err != nil {
			return nil, nil, err
		}
	} else if err != nil {
		return nil, nil, err
	}
	if err = executable(dest, b.Cmd); err != nil {
		return nil, nil, err
	}
	return append([]string{command}, b.Args...), b.Env, nil
}
func executable(root, command string) error {
	p, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(command)))
	if err != nil {
		return errors.New("registry executable is missing from the archive")
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || !localPath(filepath.ToSlash(rel)) {
		return errors.New("registry executable escapes its installation")
	}
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("registry command is not a regular file")
	}
	return os.Chmod(p, 0700)
}
func download(ctx context.Context, client *http.Client, b Binary, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", b.Archive, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("could not download the registry archive")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry archive returned HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(f.Name())
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, (512<<20)+1))
	if err != nil {
		return "", err
	}
	if n > 512<<20 {
		return "", errors.New("registry archive exceeds 512 MiB")
	}
	if b.SHA256 != "" && !strings.EqualFold(b.SHA256, fmt.Sprintf("%x", hash.Sum(nil))) {
		return "", errors.New("registry archive checksum mismatch")
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	ok = true
	return f.Name(), nil
}

type archiveLink struct {
	name, target string
	hard         bool
}

func unpack(ctx context.Context, archive, source, root, command string) error {
	var rootErr error
	root, rootErr = filepath.EvalSymlinks(root)
	if rootErr != nil {
		return rootErr
	}
	u, err := url.Parse(source)
	if err != nil {
		return err
	}
	name := strings.ToLower(u.Path)
	var total int64
	count := 0
	var links []archiveLink
	write := func(name string, mode os.FileMode, size int64, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." || name == "./" {
			if mode.IsDir() {
				return nil
			}
		}
		if !localPath(name) || path.Clean(name) == ".town-install" {
			return errors.New("unsafe archive path")
		}
		count++
		total += size
		if count > 50000 || size < 0 || total > 2<<30 {
			return errors.New("extracted archive exceeds installation limits")
		}
		dest := filepath.Join(root, filepath.FromSlash(name))
		if mode.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|mode.Perm()&0100)
		if err != nil {
			return err
		}
		n, err := io.CopyN(f, r, size)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if n != size {
			return io.ErrUnexpectedEOF
		}
		return closeErr
	}
	addLink := func(name, target string, hard bool) error {
		resolved := target
		if !hard {
			resolved = path.Join(path.Dir(name), target)
		}
		if !localPath(name) || path.Clean(name) == ".town-install" || !localPath(resolved) || path.IsAbs(target) || strings.ContainsAny(target, "\x00\\:") {
			return errors.New("unsafe archive link")
		}
		links = append(links, archiveLink{name, target, hard})
		if len(links) > 10000 {
			return errors.New("too many archive links")
		}
		return nil
	}
	if strings.HasSuffix(name, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, f := range z.File {
			if f.UncompressedSize64 > 2<<30 {
				return errors.New("archive entry exceeds installation limits")
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			if f.Mode()&os.ModeSymlink != 0 {
				data, e := io.ReadAll(io.LimitReader(r, 4097))
				err = e
				if err == nil && len(data) > 4096 {
					err = errors.New("oversized archive link")
				}
				if err == nil {
					err = addLink(f.Name, string(data), false)
				}
			} else if f.Mode().IsRegular() || f.Mode().IsDir() {
				err = write(f.Name, f.Mode(), int64(f.UncompressedSize64), r)
			} else {
				err = errors.New("unsupported archive entry")
			}
			r.Close()
			if err != nil {
				return err
			}
		}
	} else {
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		var stream io.Reader = f
		isTar := true
		switch {
		case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
			gz, err := gzip.NewReader(f)
			if err != nil {
				return err
			}
			defer gz.Close()
			stream = gz
		case strings.HasSuffix(name, ".tar.bz2"), strings.HasSuffix(name, ".tbz2"):
			stream = bzip2.NewReader(f)
		default:
			isTar = false
		}
		if isTar {
			tr := tar.NewReader(stream)
			for {
				h, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
				switch h.Typeflag {
				case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
					err = write(h.Name, h.FileInfo().Mode(), h.Size, tr)
				case tar.TypeSymlink, tar.TypeLink:
					err = addLink(h.Name, h.Linkname, h.Typeflag == tar.TypeLink)
				default:
					err = errors.New("unsupported archive entry")
				}
				if err != nil {
					return err
				}
			}
		} else {
			for _, extension := range []string{".dmg", ".pkg", ".deb", ".rpm", ".msi", ".appimage"} {
				if strings.HasSuffix(name, extension) {
					return errors.New("registry installer formats are unsupported")
				}
			}
			info, err := f.Stat()
			if err != nil {
				return err
			}
			return write(command, 0700, info.Size(), f)
		}
	}
	// Links are created last so extraction never writes through archive symlinks.
	sortLinks := append([]archiveLink{}, links...)
	for pass := 0; pass < 2; pass++ {
		for _, l := range sortLinks {
			if (pass == 0) != l.hard {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			dest := filepath.Join(root, filepath.FromSlash(l.name))
			if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return err
			}
			parent, err := filepath.EvalSymlinks(filepath.Dir(dest))
			if err != nil || parent != filepath.Dir(dest) {
				return errors.New("archive link parent is another link")
			}
			if l.hard {
				src := filepath.Join(root, filepath.FromSlash(l.target))
				info, err := os.Lstat(src)
				if err != nil || !info.Mode().IsRegular() {
					return errors.New("invalid archive hard link")
				}
				if err = os.Link(src, dest); err != nil {
					return err
				}
			} else if err = os.Symlink(l.target, dest); err != nil {
				return err
			}
		}
	}
	return nil
}

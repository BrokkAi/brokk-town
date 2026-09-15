package town

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// ReleaseURL is the GitHub releases base that install.sh downloads from.
var ReleaseURL = "https://github.com/BrokkAi/brokk-town/releases"

// npmInstalled reports whether the executable is the native package that the
// @brokkai/brokk-town launcher spawns. Only that layout upgrades through npm.
func npmInstalled(executable string) bool {
	return strings.Contains(filepath.ToSlash(executable), "/node_modules/@brokkai/")
}

// UpdateCommand is the manual equivalent of InstallUpdate for this binary.
func UpdateCommand(executable, latest string) string {
	if npmInstalled(executable) {
		return "npm install -g @brokkai/brokk-town@" + latest
	}
	return "curl -fsSL https://raw.githubusercontent.com/BrokkAi/brokk-town/master/install.sh | sh -s -- v" + latest
}

// InstallUpdate replaces this binary with the exact requested release using
// the channel it was installed from. npm layouts reinstall the package; every
// other layout (install.sh, go install, a build) downloads the checksummed
// release archive and swaps the file in place, as install.sh does.
func InstallUpdate(ctx context.Context, dir, executable, latest string) error {
	if _, ok := versionParts(latest); !ok {
		return fmt.Errorf("invalid release version %q", latest)
	}
	if npmInstalled(executable) {
		_, err := osrun.Run(ctx, dir, nil, "npm", "install", "--global", "@brokkai/brokk-town@"+latest)
		return err
	}
	return installArchive(ctx, http.DefaultClient, ReleaseURL, executable, latest)
}

func installArchive(ctx context.Context, client *http.Client, base, executable, latest string) error {
	if executable == "" {
		return errors.New("unknown executable path")
	}
	tag := "v" + strings.TrimPrefix(latest, "v")
	asset := fmt.Sprintf("brokk-town-%s-%s-%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
	archive, err := fetch(ctx, client, base+"/download/"+tag+"/"+asset, 256<<20)
	if err != nil {
		return err
	}
	sums, err := fetch(ctx, client, base+"/download/"+tag+"/checksums.txt", 64<<10)
	if err != nil {
		return err
	}
	expected, err := checksumFor(string(sums), asset)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(archive)
	if hex.EncodeToString(actual[:]) != expected {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	binary, err := extractMember(archive, "bt")
	if err != nil {
		return err
	}
	staged, err := os.CreateTemp(filepath.Dir(executable), ".brokk-town.*")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if _, err = staged.Write(binary); err != nil {
		staged.Close()
		return err
	}
	if err = staged.Chmod(0755); err != nil {
		staged.Close()
		return err
	}
	if err = staged.Close(); err != nil {
		return err
	}
	return os.Rename(stagedPath, executable)
}

func fetch(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", url, limit)
	}
	return data, nil
}

// checksumFor requires exactly one entry for the asset, as install.sh does.
func checksumFor(sums, asset string) (string, error) {
	found := ""
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			if found != "" {
				return "", errors.New("checksums.txt lists the archive more than once")
			}
			found = strings.ToLower(fields[0])
		}
	}
	if len(found) != 64 {
		return "", errors.New("checksums.txt has no entry for the archive")
	}
	return found, nil
}

// extractMember returns one regular file from the archive without following
// its directory structure or links.
func extractMember(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive has no %s member", name)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(header.Name) != name || header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(reader, 256<<20))
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, errors.New("archive contains an empty binary")
		}
		return data, nil
	}
}

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
	"time"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// ReleaseURL is the GitHub releases base that install.sh downloads from, and
// RegistryURL the npm registry the launcher's packages come from.
var (
	ReleaseURL  = "https://github.com/BrokkAi/brokk-town/releases"
	RegistryURL = "https://registry.npmjs.org"
)

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

// platformPackage is the native npm package that carries bt for this build.
// Its arch token follows node's process.arch, which the launcher resolves,
// rather than GOARCH.
func platformPackage() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return "@brokkai/brokk-town-" + runtime.GOOS + "-" + arch
}

// archiveAsset is the release archive install.sh and installArchive download.
func archiveAsset(tag string) string {
	return fmt.Sprintf("brokk-town-%s-%s-%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
}

// ReleaseReady reports whether the payload this binary would actually install
// is published for this platform. A release is not atomic: npm's latest tag
// flips when the launcher lands, and the launcher pins the native package as
// an optional dependency, so between the two uploads npm installs a bt that is
// not there and still exits 0. GitHub's assets appear on their own schedule
// too. Offering a version in that window turns one click into a failed upgrade,
// so the offer waits until the payload can be fetched.
func ReleaseReady(ctx context.Context, client *http.Client, executable, latest string) error {
	version := strings.TrimPrefix(latest, "v")
	url := RegistryURL + "/" + strings.Replace(platformPackage(), "/", "%2f", 1) + "/" + version
	if !npmInstalled(executable) {
		url = ReleaseURL + "/download/v" + version + "/" + archiveAsset("v"+version)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	// Only an outright absence withholds the offer. A rate limit, a proxy that
	// dislikes HEAD, or any other refusal says nothing about the release, and
	// withholding on those would silence upgrades indefinitely; the install
	// verifies the binary itself, so letting one through costs a clear failure
	// rather than a broken town.
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s is not published for %s/%s yet", version, runtime.GOOS, runtime.GOARCH)
	}
	return nil
}

// InstallUpdate replaces this binary with the exact requested release using
// the channel it was installed from. npm layouts reinstall the package; every
// other layout (install.sh, go install, a build) downloads the checksummed
// release archive and swaps the file in place, as install.sh does. Either way
// the binary itself has the last word: the caller restarts by exec'ing this
// path, so an install that did not land there must fail here instead.
func InstallUpdate(ctx context.Context, dir, executable, latest string) error {
	if _, ok := versionParts(latest); !ok {
		return fmt.Errorf("invalid release version %q", latest)
	}
	if !npmInstalled(executable) {
		// The archive channel checks the staged file and only then renames it
		// over the executable, so a release that reports the wrong version
		// leaves the working binary exactly where it was.
		return installArchive(ctx, http.DefaultClient, ReleaseURL, executable, latest)
	}
	// npm has already rewritten its tree by the time it exits, so this channel
	// can only inspect the result.
	if _, err := osrun.Run(ctx, dir, nil, "npm", "install", "--global", "@brokkai/brokk-town@"+latest); err != nil {
		return err
	}
	return verifyInstalled(ctx, executable, latest)
}

// verifyInstalled requires the binary that a restart will exec to report the
// version just installed. npm is the reason this is not paranoia: the platform
// package carrying bt is an optional dependency, and npm exits 0 when it skips
// one, so a "successful" install can leave this path stale or missing. Exec'ing
// it then trades a working town for a dead service and an unusable bt.
func verifyInstalled(ctx context.Context, executable, latest string) error {
	if executable == "" {
		return errors.New("unknown executable path")
	}
	// Running a local binary gets its own budget rather than whatever a slow
	// download left of the caller's: a landed install must not be reported as
	// unverifiable because the deadline ran out one call earlier.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	out, err := osrun.Run(ctx, filepath.Dir(executable), nil, executable, "version")
	if err != nil {
		return fmt.Errorf("%s did not run after the upgrade: %w", executable, err)
	}
	installed := strings.TrimPrefix(strings.TrimSpace(out), "v")
	if installed != strings.TrimPrefix(latest, "v") {
		return fmt.Errorf("the upgrade reported success but %s is still %s, not %s", executable, installed, latest)
	}
	return nil
}

func installArchive(ctx context.Context, client *http.Client, base, executable, latest string) error {
	if executable == "" {
		return errors.New("unknown executable path")
	}
	tag := "v" + strings.TrimPrefix(latest, "v")
	asset := archiveAsset(tag)
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
	// The staged file is asked for its version while it is still a temporary
	// file: a checksummed archive can still hold a binary built with the wrong
	// version stamp, and finding that out after the rename would leave the town
	// running a binary this code has already declared unusable.
	if err = verifyInstalled(ctx, stagedPath, latest); err != nil {
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

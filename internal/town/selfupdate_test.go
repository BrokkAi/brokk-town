package town

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func releaseArchive(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "bt", Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUpdateChannelFollowsInstallLayout(t *testing.T) {
	npm := "/usr/lib/node_modules/@brokkai/brokk-town/node_modules/@brokkai/brokk-town-darwin-arm64/bin/bt"
	if !npmInstalled(npm) || npmInstalled("/home/me/.local/bin/bt") {
		t.Fatal("npm layout detection is wrong")
	}
	if !strings.HasPrefix(UpdateCommand(npm, "1.2.3"), "npm install -g @brokkai/brokk-town@1.2.3") {
		t.Fatal(UpdateCommand(npm, "1.2.3"))
	}
	if !strings.Contains(UpdateCommand("/home/me/.local/bin/bt", "1.2.3"), "install.sh") {
		t.Fatal(UpdateCommand("/home/me/.local/bin/bt", "1.2.3"))
	}
	if err := InstallUpdate(context.Background(), t.TempDir(), "/home/me/.local/bin/bt", "not-a-version"); err == nil {
		t.Fatal("invalid version accepted")
	}
}

func TestArchiveUpdateVerifiesChecksumAndReplacesBinaryInPlace(t *testing.T) {
	archive := releaseArchive(t, "#!/bin/sh\necho v1.1.0\n")
	sum := sha256.Sum256(archive)
	asset := fmt.Sprintf("brokk-town-v1.1.0-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	checksums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/v1.1.0/" + asset:
			_, _ = w.Write(archive)
		case "/download/v1.1.0/checksums.txt":
			_, _ = w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	executable := filepath.Join(t.TempDir(), "bt")
	if err := os.WriteFile(executable, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := installArchive(context.Background(), server.Client(), server.URL, executable, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil || !strings.Contains(string(data), "echo v1.1.0") {
		t.Fatalf("binary not replaced: %v %q", err, data)
	}
	info, _ := os.Stat(executable)
	if info.Mode().Perm() != 0755 {
		t.Fatalf("mode %v", info.Mode())
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(executable), ".brokk-town.*"))
	if len(leftovers) != 0 {
		t.Fatalf("staged files left behind: %v", leftovers)
	}

	checksums = strings.Repeat("0", 64) + "  " + asset + "\n"
	if err := installArchive(context.Background(), server.Client(), server.URL, executable, "1.1.0"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("bad checksum accepted: %v", err)
	}
	if data, _ := os.ReadFile(executable); !strings.Contains(string(data), "echo v1.1.0") {
		t.Fatal("binary was replaced despite checksum failure")
	}
	if err := installArchive(context.Background(), server.Client(), server.URL, executable, "9.9.9"); err == nil {
		t.Fatal("missing release accepted")
	}
}

// A stale binary is what npm leaves behind when it skips the optional package
// that carries bt, and an exit code alone cannot tell that from success.
func TestInstallRefusesAVersionThatNeverReachedTheBinary(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "bt")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho v1.0.0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	err := verifyInstalled(context.Background(), executable, "1.1.0")
	if err == nil || !strings.Contains(err.Error(), "still 1.0.0") {
		t.Fatalf("a stale binary passed verification: %v", err)
	}
	if err = os.WriteFile(executable, []byte("#!/bin/sh\necho v1.1.0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = verifyInstalled(context.Background(), executable, "1.1.0"); err != nil {
		t.Fatalf("the installed version was rejected: %v", err)
	}
	if err = os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if err = verifyInstalled(context.Background(), executable, "1.1.0"); err == nil {
		t.Fatal("a missing binary passed verification")
	}
}

// An archive can carry a binary stamped with the wrong version even when its
// checksum is right, and the working executable has to survive that.
func TestArchiveKeepsTheRunningBinaryWhenTheReleaseReportsAnotherVersion(t *testing.T) {
	archive := releaseArchive(t, "#!/bin/sh\necho v1.0.9\n")
	sum := sha256.Sum256(archive)
	asset := archiveAsset("v1.1.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/v1.1.0/" + asset:
			_, _ = w.Write(archive)
		case "/download/v1.1.0/checksums.txt":
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	executable := filepath.Join(dir, "bt")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho v1.0.0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	err := installArchive(context.Background(), server.Client(), server.URL, executable, "1.1.0")
	if err == nil || !strings.Contains(err.Error(), "still 1.0.9") {
		t.Fatalf("a mismatched release was installed: %v", err)
	}
	if data, _ := os.ReadFile(executable); !strings.Contains(string(data), "echo v1.0.0") {
		t.Fatalf("the running binary was replaced anyway: %q", data)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".brokk-town.*"))
	if len(leftovers) != 0 {
		t.Fatalf("staged files left behind: %v", leftovers)
	}
}

// Releases publish in pieces, so a version npm already names as latest can have
// no payload for this platform yet. Offering it would install nothing. Only an
// outright absence withholds it: any other refusal says nothing about the
// release, and treating those as absence would silence upgrades for good.
func TestReleaseReadyWaitsForThisPlatformsPayload(t *testing.T) {
	npm := "/usr/lib/node_modules/@brokkai/brokk-town/node_modules/@brokkai/brokk-town-darwin-arm64/bin/bt"
	local := "/home/me/.local/bin/bt"
	published := map[string]bool{
		"/download/v1.1.0/" + archiveAsset("v1.1.0"):                       true,
		"/" + strings.Replace(platformPackage(), "/", "%2f", 1) + "/1.1.0": true,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case published[r.URL.EscapedPath()]:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "1.3.0"):
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	release, registry := ReleaseURL, RegistryURL
	ReleaseURL, RegistryURL = server.URL, server.URL
	defer func() { ReleaseURL, RegistryURL = release, registry }()
	for _, exe := range []string{local, npm} {
		if err := ReleaseReady(context.Background(), server.Client(), exe, "1.1.0"); err != nil {
			t.Fatalf("a published release was withheld from %s: %v", exe, err)
		}
		if err := ReleaseReady(context.Background(), server.Client(), exe, "1.2.0"); err == nil {
			t.Fatalf("a release with no payload for %s was offered", exe)
		}
		if err := ReleaseReady(context.Background(), server.Client(), exe, "1.3.0"); err != nil {
			t.Fatalf("a refused request withheld the offer from %s: %v", exe, err)
		}
	}
}

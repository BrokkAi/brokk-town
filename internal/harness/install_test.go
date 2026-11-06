package harness

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type tarItem struct {
	name, body, target string
	kind               byte
}

func tarBytes(t *testing.T, items ...tarItem) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, item := range items {
		kind := item.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(item.body))
		if kind != tar.TypeReg {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{Name: item.name, Mode: 0700, Size: size, Typeflag: kind, Linkname: item.target}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := tw.Write([]byte(item.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func binaryEntry(data []byte, extension string) Entry {
	return Entry{ID: "binary-agent", Name: "Binary Agent", Version: "1", Distribution: Distribution{Binary: map[string]Binary{Platform(): {Archive: "https://example.com/agent" + extension, Cmd: "./bin/agent", Args: []string{"--acp"}, Env: map[string]string{"MODE": "acp"}, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}}}}
}
func TestNativeInstallIsAtomicCachedAndSharedAcrossWorkers(t *testing.T) {
	data := tarBytes(t, tarItem{name: "real-agent", body: "fake executable"}, tarItem{name: "bin/agent", kind: tar.TypeSymlink, target: "../real-agent"})
	entry := binaryEntry(data, ".tar.gz")
	root := t.TempDir()
	var calls atomic.Int32
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { calls.Add(1); return response(data), nil })}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			command, env, err := launch(context.Background(), root, entry, client)
			if err != nil {
				t.Error(err)
				return
			}
			actual, err := os.ReadFile(command[0])
			if err != nil || string(actual) != "fake executable" || command[1] != "--acp" || env["MODE"] != "acp" {
				t.Error(command, env, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("download repeated", calls.Load())
	}
	if _, _, err := launch(context.Background(), root, entry, client); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("cache was not used")
	}
}
func TestZipBzipAndRawBinaryDistributions(t *testing.T) {
	bzip, err := os.ReadFile("testdata/agent.tar.bz2")
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	f, err := z.Create("bin/agent")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("zip executable"))
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		extension string
		data      []byte
		want      string
	}{{".tar.bz2", bzip, "bzip executable"}, {".zip", buffer.Bytes(), "zip executable"}, {"", []byte("raw executable"), "raw executable"}} {
		t.Run(tc.extension, func(t *testing.T) {
			client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(tc.data), nil })}
			cmd, _, err := launch(context.Background(), t.TempDir(), binaryEntry(tc.data, tc.extension), client)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(cmd[0])
			if err != nil || string(b) != tc.want {
				t.Fatal(string(b), err)
			}
		})
	}
}
func TestBadArchivesNeverBecomeInstalled(t *testing.T) {
	for name, items := range map[string][]tarItem{
		"parent traversal": {{name: "../escaped", body: "bad"}},
		"absolute path":    {{name: "/escaped", body: "bad"}},
		"link escape":      {{name: "bin/agent", kind: tar.TypeSymlink, target: "../../escaped"}},
		"hardlink escape":  {{name: "bin/agent", kind: tar.TypeLink, target: "../escaped"}},
		"special file":     {{name: "bin/agent", kind: tar.TypeFifo}},
		"duplicate file":   {{name: "bin/agent", body: "one"}, {name: "bin/agent", body: "two"}},
		"reserved receipt": {{name: "bin/agent", body: "safe"}, {name: ".town-install", kind: tar.TypeSymlink, target: "bin/agent"}},
		"missing command":  {{name: "another-file", body: "not an agent"}},
	} {
		t.Run(name, func(t *testing.T) {
			data := tarBytes(t, items...)
			entry := binaryEntry(data, ".tgz")
			root := t.TempDir()
			client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(data), nil })}
			if _, _, err := launch(context.Background(), root, entry, client); err == nil {
				t.Fatal("unsafe archive installed")
			}
			matches, _ := filepath.Glob(filepath.Join(root, "harnesses", "installed", "*", ".town-install"))
			if len(matches) != 0 {
				t.Fatal("partial install published")
			}
		})
	}
	data := tarBytes(t, tarItem{name: "bin/agent", body: "safe"})
	entry := binaryEntry(data, ".tgz")
	b := entry.Distribution.Binary[Platform()]
	b.SHA256 = strings.Repeat("0", 64)
	entry.Distribution.Binary[Platform()] = b
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(data), nil })}
	if _, _, err := launch(context.Background(), t.TempDir(), entry, client); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal("bad checksum accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := launch(ctx, t.TempDir(), entry, client); err != context.Canceled {
		t.Fatal("cancellation ignored", err)
	}
}

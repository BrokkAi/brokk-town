package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestSendsTextWithoutHTMLEscaping(t *testing.T) {
	received := make(chan string, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		received <- string(data)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer h.Close()
	var out any
	body := strings.Repeat("<a & b>", 4000)
	if err := request(context.Background(), connection{URL: h.URL, Token: "test-key"}, "POST", "/api/requests", map[string]string{"body": body}, &out); err != nil {
		t.Fatal(err)
	}
	got := <-received
	if !strings.Contains(got, body) || len(got) > len(body)+32 {
		t.Fatalf("body was escaped: %d bytes for %d", len(got), len(body))
	}
}

func TestRequestReportsServiceError(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":"request body exceeds the 64 KiB limit"}`))
	}))
	defer h.Close()
	var out any
	err := request(context.Background(), connection{URL: h.URL, Token: "test-key"}, "POST", "/api/requests", map[string]string{}, &out)
	if err == nil || err.Error() != "town service: request body exceeds the 64 KiB limit" {
		t.Fatal(err)
	}
}

func stubTerminal(t *testing.T, terminal bool) {
	t.Helper()
	previous := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return terminal }
	t.Cleanup(func() { stdoutIsTerminal = previous })
}

func TestBannersKeepAccessKeyOffRedirectedOutput(t *testing.T) {
	conn := connection{URL: "http://127.0.0.1:1234", Token: "secret-key", PID: 42}
	banners := map[string]func(demo bool) string{
		"serve":      func(demo bool) string { return serveBanner(conn, demo) },
		"background": func(demo bool) string { return backgroundBanner(conn, demo, "/state/logs") },
	}
	for name, banner := range banners {
		stubTerminal(t, true)
		if got := banner(false); !strings.Contains(got, "Browser: http://127.0.0.1:1234/#token=secret-key\n") {
			t.Fatalf("%s on a terminal: %q", name, got)
		}
		stubTerminal(t, false)
		for _, demo := range []bool{false, true} {
			got := banner(demo)
			if strings.Contains(got, "secret-key") || !strings.Contains(got, "run bt web") {
				t.Fatalf("%s redirected: %q", name, got)
			}
		}
		if got := banner(true); !strings.Contains(got, "bt web --demo") {
			t.Fatalf("%s redirected demo: %q", name, got)
		}
	}
}

func TestOpenLogsRedactsKeysFromEarlierVersions(t *testing.T) {
	dir := t.TempDir()
	key := strings.Repeat("ab", 32)
	stdout, _ := logPaths(dir)
	if err := os.MkdirAll(filepath.Dir(stdout), 0700); err != nil {
		t.Fatal(err)
	}
	old := "Brokk Town v1\nBrowser: http://127.0.0.1:1/#token=" + key + "\nother line\n"
	if err := os.WriteFile(stdout, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	out, errFile, err := openLogs(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = out.WriteString("new line\n")
	out.Close()
	errFile.Close()
	data, _ := os.ReadFile(stdout)
	want := "Brokk Town v1\nBrowser: http://127.0.0.1:1/#token=[redacted]\nother line\nnew line\n"
	if string(data) != want {
		t.Fatalf("%q", data)
	}
	if info, _ := os.Stat(stdout); info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}

func TestOpenLogsTruncatesAnOversizedOldLog(t *testing.T) {
	dir := t.TempDir()
	stdout, _ := logPaths(dir)
	if err := os.MkdirAll(filepath.Dir(stdout), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stdout, make([]byte, maxScrubbedLog+1), 0600); err != nil {
		t.Fatal(err)
	}
	out, errFile, err := openLogs(dir)
	if err != nil {
		t.Fatal(err)
	}
	out.Close()
	errFile.Close()
	if info, _ := os.Stat(stdout); info.Size() != 0 {
		t.Fatal(info.Size())
	}
}

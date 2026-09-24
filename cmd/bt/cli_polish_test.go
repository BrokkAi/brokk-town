package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

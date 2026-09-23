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

func TestBrowserLinkKeepsAccessKeyOffRedirectedOutput(t *testing.T) {
	conn := connection{URL: "http://127.0.0.1:1234", Token: "secret-key"}
	if got := browserLink(conn, false, true); got != "http://127.0.0.1:1234/#token=secret-key" {
		t.Fatal(got)
	}
	for _, demo := range []bool{false, true} {
		got := browserLink(conn, demo, false)
		if strings.Contains(got, "secret-key") || !strings.Contains(got, "bt web") {
			t.Fatal(got)
		}
	}
	if got := browserLink(conn, true, false); !strings.Contains(got, "bt web --demo") {
		t.Fatal(got)
	}
}

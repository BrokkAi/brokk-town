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
	if err == nil || err.Error() != "Brokk Town service: request body exceeds the 64 KiB limit" {
		t.Fatal(err)
	}
}

// The banner lands in the pod's log, so it never carries the access key.
func TestBannerKeepsAccessKeyOutOfTheLog(t *testing.T) {
	got := serveBanner(connection{URL: testPublicURL, Token: "secret-key", PID: 42})
	if strings.Contains(got, "secret-key") || !strings.Contains(got, testPublicURL) || !strings.Contains(got, "run bt web") {
		t.Fatalf("banner: %q", got)
	}
}

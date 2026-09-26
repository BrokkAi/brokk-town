package mjolnir

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const profileFixture = `{"models":[{"value":"provider/model+1","name":"Model","description":"private detail"}],"efforts":[{"value":"high","name":"High"}],"observed_at":123,"private":"secret"}`

func TestProfileConfigUsesDaemonAndEscapesSelectors(t *testing.T) {
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.EscapedPath() != "/api/v1/profiles/account%2Fspecial/config" || r.URL.Query().Get("model") != "provider/model+1&effort=other" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Mj-Api-Version", "1")
		fmt.Fprint(w, profileFixture)
	})
	got, err := c.ProfileConfig(context.Background(), Selection{"target", "account/special"}, "provider/model+1&effort=other")
	if err != nil || len(got.Models) != 1 || got.Models[0].Value != "provider/model+1" || len(got.Efforts) != 1 {
		t.Fatalf("%+v: %v", got, err)
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "secret") {
		t.Fatal("nonpublic fields retained", string(data))
	}
}

func TestProfileFailuresNeverBecomeEmptySuccess(t *testing.T) {
	for _, name := range []string{"missing", "unavailable", "auth", "version", "redirect", "oversized", "malformed", "missing-lists", "duplicate", "control"} {
		t.Run(name, func(t *testing.T) {
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Mj-Api-Version", "1")
				switch name {
				case "missing":
					w.WriteHeader(404)
				case "unavailable":
					w.WriteHeader(503)
				case "auth":
					w.WriteHeader(401)
				case "version":
					w.Header().Set("Mj-Api-Version", "2")
				case "redirect":
					http.Redirect(w, r, "/private-token", 302)
				case "oversized":
					fmt.Fprint(w, strings.Repeat("x", maxBytes+1))
					return
				case "missing-lists":
					fmt.Fprint(w, `{}`)
					return
				case "duplicate":
					fmt.Fprint(w, `{"models":[{"value":"x"},{"value":"x"}],"efforts":[]}`)
					return
				case "control":
					fmt.Fprint(w, `{"models":[{"value":"x\n"}],"efforts":[]}`)
					return
				}
				fmt.Fprint(w, "private-token private-path")
			})
			_, err := c.ProfileConfig(context.Background(), Selection{"target", "profile"}, "")
			if err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatalf("unsafe result: %v", err)
			}
		})
	}
}

func TestProfileDiscoveryCancellationAndDemo(t *testing.T) {
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.ProfileConfig(ctx, Selection{"target", "profile"}, ""); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no request")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("request did not cancel")
	}
	demo := New(filepath.Dir(c.cache), true, c.connection)
	if _, err := demo.ProfileConfig(context.Background(), Selection{"target", "profile"}, ""); err == nil || calls.Load() != 1 {
		t.Fatal("demo contacted daemon")
	}
}

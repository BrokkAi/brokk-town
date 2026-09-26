package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestStorageAPIAuthStrictInputAndSharedInventory(t *testing.T) {
	s, h := fixture(t)
	if err := s.Store.Update(func(st *town.State) error { _, err := st.Add(town.DefaultConfig("acme/project")); return err }); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/storage", "/api/storage/cleanup"} {
		r := call(t, h.URL, "POST", path, `{"town":"acme/project"}`, "", "")
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatal(path, r.Status)
		}
		for _, body := range []string{`null`, `{"town":"acme/project","unknown":true}`, `{"town":"acme/project","minimum_age_hours":-1}`, `{"town":"missing/project"}`, `{"town":"acme/project","ids":["../../outside"]}`} {
			r := call(t, h.URL, "POST", path, body, "test-key", "")
			if r.StatusCode != http.StatusBadRequest {
				t.Fatal(path, body, r.Status)
			}
		}
	}
	r := call(t, h.URL, "POST", "/api/storage", `{"town":"Acme/Project"}`, "test-key", "")
	var result town.StorageInventory
	if r.StatusCode != 200 || json.NewDecoder(r.Body).Decode(&result) != nil || result.Town != "acme/project" || result.MinimumAgeHours != 168 || result.Artifacts == nil {
		t.Fatal(r.Status, result)
	}
	if len(s.Store.Snapshot().Towns) != 1 {
		t.Fatal("inventory changed towns")
	}
}

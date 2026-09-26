package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestHistoryAPIAuthPaginationDetailAndStrictInput(t *testing.T) {
	s, h := fixture(t)
	now := time.Now()
	if err := s.Store.Update(func(st *town.State) error {
		x, err := st.Add(town.DefaultConfig("acme/project"))
		if err != nil {
			return err
		}
		x.Tasks["issue:1"] = &town.Task{ID: "issue:1", Kind: "issue", Number: 1, House: town.Issue, Stage: "closed", Title: "Saved answer", Updated: now.Add(-40 * 24 * time.Hour)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.ArchiveCompleted(context.Background(), "acme/project", now); err != nil {
		t.Fatal(err)
	}
	if r := call(t, h.URL, "POST", "/api/history", `{"town":"acme/project"}`, "", ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatal(r.Status)
	}
	for _, body := range []string{`null`, `{"town":"acme/project","unknown":true}`, `{"town":"missing/project"}`, `{"town":"acme/project","limit":101}`, `{"town":"acme/project","after":"../../outside"}`, `{"town":"acme/project","task":"issue:1","limit":1}`, `{"town":"acme/project","task":"issue:2"}`} {
		if r := call(t, h.URL, "POST", "/api/history", body, "test-key", ""); r.StatusCode != http.StatusBadRequest {
			t.Fatal(body, r.Status)
		}
	}
	r := call(t, h.URL, "POST", "/api/history", `{"town":"Acme/Project","limit":1}`, "test-key", "")
	var page town.HistoryPage
	if r.StatusCode != 200 || json.NewDecoder(r.Body).Decode(&page) != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Title != "Saved answer" {
		t.Fatal(r.Status, page)
	}
	r = call(t, h.URL, "POST", "/api/history", `{"town":"acme/project","task":"issue:1"}`, "test-key", "")
	var task town.Task
	if r.StatusCode != 200 || json.NewDecoder(r.Body).Decode(&task) != nil || task.Title != "Saved answer" {
		t.Fatal(r.Status, task)
	}
	if len(s.Store.Snapshot().Towns["acme/project"].Tasks) != 0 {
		t.Fatal("history reads restored hot tasks")
	}
}

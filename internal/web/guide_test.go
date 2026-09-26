package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestGuideAPIAuthStrictInputsAndIdempotentConversation(t *testing.T) {
	s, h := fixture(t)
	if err := s.Store.Update(func(st *town.State) error { _, err := st.Add(town.DefaultConfig("acme/project")); return err }); err != nil {
		t.Fatal(err)
	}
	ask := `{"town":"acme/project","action":"ask","id":"question-one","sequence":0,"question":"What is blocked?"}`
	if r := call(t, h.URL, "POST", "/api/guide", ask, "", ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatal(r.Status)
	}
	for _, body := range []string{`null`, `{"town":"acme/project","action":"read","extra":1}`, `{"town":"acme/project","action":"read","question":"mutate"}`, `{"town":"acme/project","action":"execute"}`, `{"town":"acme/project","action":"ask","id":"question-one","question":"x","digest":"confirm"}`, `{"town":"acme/project","action":"confirm","id":"question-one","digest":"none"}`} {
		if r := call(t, h.URL, "POST", "/api/guide", body, "test-key", ""); r.StatusCode != 400 {
			t.Fatal(body, r.Status)
		}
	}
	for i := 0; i < 2; i++ {
		r := call(t, h.URL, "POST", "/api/guide", ask, "test-key", "")
		var g town.GuideConversation
		if r.StatusCode != 200 || json.NewDecoder(r.Body).Decode(&g) != nil || g.Next != 1 || len(g.Turns) != 1 {
			t.Fatal(r.Status, g)
		}
	}
	r := call(t, h.URL, "POST", "/api/guide", `{"town":"acme/project","action":"cancel","id":"question-one"}`, "test-key", "")
	if r.StatusCode != 200 {
		t.Fatal(r.Status)
	}
	r = call(t, h.URL, "POST", "/api/guide", `{"town":"Acme/Project","action":"read"}`, "test-key", "")
	var g town.GuideConversation
	if json.NewDecoder(r.Body).Decode(&g) != nil || g.Turns[0].Status != "cancelled" {
		t.Fatal(g)
	}
	x := s.Store.Snapshot().Towns["acme/project"]
	if x.Workers[town.Issue].Enabled || len(x.Intents) != 0 {
		t.Fatal("conversation dispatched automation")
	}
}

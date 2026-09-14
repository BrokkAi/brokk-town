package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func TestCapacityAPIIsAuthenticatedStrictValidatedAndPersisted(t *testing.T) {
	s, h := fixture(t)
	if r := call(t, h.URL, "POST", "/api/capacity", `{"max_workers":8}`, "", ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing capacity authorization: %d", r.StatusCode)
	}
	for _, body := range []string{
		`{"max_workers":8,"injected":true}`,
		`{"max_workers":0}`,
		`{"max_workers":65}`,
		`{}`,
		`null`,
		`{"max_workers":1.5}`,
		`{"max_workers":"8"}`,
		`{"max_workers":8}`,
		`{"max_workers":8} {}`,
	} {
		want := http.StatusBadRequest
		if body == `{"max_workers":8}` {
			want = http.StatusOK
		}
		r := call(t, h.URL, "POST", "/api/capacity", body, "test-key", "")
		if r.StatusCode != want {
			data, _ := io.ReadAll(r.Body)
			t.Fatalf("capacity body %s got %d want %d: %s", body, r.StatusCode, want, data)
		}
		if want == http.StatusOK {
			var capacity town.Capacity
			if err := json.NewDecoder(r.Body).Decode(&capacity); err != nil {
				t.Fatal(err)
			}
			if capacity.Active != 0 || capacity.Limit != 8 {
				t.Fatalf("unexpected capacity response: %+v", capacity)
			}
		}
	}
	state := s.Store.Snapshot()
	if state.ServiceConfig.MaxWorkers != 8 || state.Capacity == nil || state.Capacity.Limit != 8 {
		t.Fatalf("capacity was not persisted: %+v", state)
	}
}

func TestCapacityUpdateAppearsInEventStream(t *testing.T) {
	_, h := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-key")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	reader := bufio.NewReader(r.Body)
	read := func() town.State {
		t.Helper()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				var state town.State
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &state); err != nil {
					t.Fatal(err)
				}
				return state
			}
		}
	}
	initial := read()
	if initial.ServiceConfig.MaxWorkers != town.DefaultMaxWorkers || initial.Capacity == nil || initial.Capacity.Active != 0 {
		t.Fatalf("unexpected initial capacity snapshot: %+v", initial)
	}
	response := call(t, h.URL, http.MethodPost, "/api/capacity", `{"max_workers":12}`, "test-key", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("capacity update failed: %s", response.Status)
	}
	next := read()
	if next.ServiceConfig.MaxWorkers != 12 || next.Capacity == nil || next.Capacity.Limit != 12 || next.Capacity.Active != 0 {
		t.Fatalf("event stream omitted capacity update: %+v", next)
	}
}

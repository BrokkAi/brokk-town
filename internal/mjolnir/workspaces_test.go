package mjolnir

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestRepeatedDispatchPlacementReusesOneWorkspaceAndConfiguredBundle(t *testing.T) {
	var creates atomic.Int32
	workspace := Workspace{"stable-workspace", "Town acme/app"}
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		artifactHeaders(w, "application/json")
		switch r.URL.Path {
		case "/api/v1/options":
			fmt.Fprint(w, `{"revision":1,"profiles":[],"targets":[],"bundles":[{"id":"product","primary_repository":"app","repositories":[{"id":"app","github":"acme/app","destination":"app"}]}]}`)
		case "/api/v1/workspaces":
			if r.Method == "POST" {
				creates.Add(1)
				var input struct {
					Name string `json:"name"`
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil || input.Name != workspace.Name {
					t.Error("used a per-dispatch workspace name")
				}
				w.WriteHeader(201)
				json.NewEncoder(w).Encode(map[string]any{"workspace": workspace})
			} else {
				list := []Workspace{}
				if creates.Load() > 0 {
					list = append(list, workspace)
				}
				json.NewEncoder(w).Encode(map[string]any{"workspaces": list})
			}
		default:
			t.Fatalf("unexpected quick bundle or lifecycle operation: %s %s", r.Method, r.URL)
		}
	})
	for i := 0; i < 3; i++ {
		p, err := c.ResolvePlacement(t.Context(), "acme/app", workspace.Name, "")
		if err != nil || p.Workspace != workspace || p.Bundle != "product" || p.Repository != "app" {
			t.Fatalf("placement: %+v %v", p, err)
		}
	}
	if creates.Load() != 1 {
		t.Fatal("accumulated workspace records")
	}
}

func TestPlacementRefusesAmbiguousForeignOrSiblingRepositoriesBeforeCreatingWorkspace(t *testing.T) {
	for _, scenario := range []string{"ambiguous", "foreign", "sibling", "wrong primary", "missing bundle"} {
		t.Run(scenario, func(t *testing.T) {
			var unexpected atomic.Int32
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				artifactHeaders(w, "application/json")
				if r.URL.Path != "/api/v1/options" {
					unexpected.Add(1)
					return
				}
				bundle := Bundle{"product", "app", []Repository{{"app", "acme/app", "app"}}}
				bundles := []Bundle{bundle}
				switch scenario {
				case "ambiguous":
					other := bundle
					other.ID = "other"
					bundles = append(bundles, other)
				case "foreign":
					bundles[0].Repositories[0].GitHub = "acme/other"
				case "sibling":
					bundles[0].Repositories = append(bundles[0].Repositories, Repository{"sibling", "acme/other", "other"})
				case "wrong primary":
					bundles[0].PrimaryRepository = "unknown"
				case "missing bundle":
					bundles = []Bundle{}
				}
				json.NewEncoder(w).Encode(Options{Profiles: []Profile{}, Targets: []Target{}, Bundles: bundles})
			})
			if _, err := c.ResolvePlacement(t.Context(), "acme/app", "Town acme/app", ""); err == nil || unexpected.Load() != 0 {
				t.Fatal("unsafe mapping reached workspace provisioning")
			}
		})
	}
}

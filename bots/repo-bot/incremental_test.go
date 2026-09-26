package repobot

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestIncrementalInventoryUsesCursorKeepsOpenWorkAndFetchesClosedPR(t *testing.T) {
	answers := repositoryAnswers()
	answers["*/issues\\?state=all*"] = `[{"number":7,"title":"Closed since last scan","state":"closed"},{"number":9,"state":"closed","pull_request":{}},{"number":8,"state":"open","pull_request":{}}]`
	answers["*/issues\\?state=open*"] = `[{"number":8,"state":"open","pull_request":{}},{"number":10,"title":"Still open","state":"open","pull_request":null}]`
	answers["repos/acme/orchard/pulls/9"] = `{"number":9,"state":"closed","base":{"ref":"main","sha":"` + headSHA + `"},"head":{"sha":"` + mergedSHA + `"},"merged_at":"2026-09-01T00:00:00Z"}`
	requests := fakeGitHub(t, answers)
	since := time.Now().Add(-time.Hour)
	out, err := (githubClient{config: inventoryConfig(t)}).snapshotSince(context.Background(), &since, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !out.Incremental || out.StartedAt.Before(since) || len(out.Issues) != 4 || len(out.Pulls) != 2 {
		t.Fatalf("%+v", out)
	}
	joined := strings.Join(requests(), "\n")
	if !strings.Contains(joined, "since="+url.QueryEscape(since.Add(-5*time.Minute).UTC().Format(time.RFC3339))) || strings.Contains(joined, "/pulls?state=all") || strings.Contains(joined, "/pulls/8") || !strings.Contains(joined, "/pulls/9") {
		t.Fatal(joined)
	}
}
func TestIncrementalInventoryRefusesIncompletePageOrMismatchedPR(t *testing.T) {
	for _, scenario := range []string{"page", "identity"} {
		t.Run(scenario, func(t *testing.T) {
			answers := repositoryAnswers()
			since := time.Now().Add(-time.Hour)
			if scenario == "identity" {
				answers["*/issues\\?state=all*"] = `[{"number":9,"pull_request":{}}]`
				answers["repos/acme/orchard/pulls/9"] = `{"number":99}`
			} else {
				batch := make([]string, 100)
				for i := range batch {
					batch[i] = fmt.Sprintf(`{"number":%d,"state":"closed"}`, i+1)
				}
				answers["*/issues\\?state=all*page=1"] = "[" + strings.Join(batch, ",") + "]"
				answers["*/issues\\?state=all*page=2"] = "invalid-json"
			}
			fakeGitHub(t, answers)
			out, err := (githubClient{config: inventoryConfig(t)}).snapshotSince(context.Background(), &since, "main")
			if err == nil || out.Head != "" || out.Incremental {
				t.Fatal("reported an incomplete scan", out, err)
			}
		})
	}
}
func TestInventoryFallsBackToFullOnMissingStaleFutureOrChangedBranch(t *testing.T) {
	for _, scenario := range []string{"missing", "stale", "future", "branch"} {
		t.Run(scenario, func(t *testing.T) {
			since := time.Now().Add(-time.Hour)
			var cursor *time.Time = &since
			branch := "main"
			switch scenario {
			case "missing":
				cursor = nil
			case "stale":
				since = time.Now().Add(-48 * time.Hour)
			case "future":
				since = time.Now().Add(time.Hour)
			case "branch":
				branch = "previous"
			}
			requests := fakeGitHub(t, repositoryAnswers())
			out, err := (githubClient{config: inventoryConfig(t)}).snapshotSince(context.Background(), cursor, branch)
			if err != nil || out.Incremental {
				t.Fatal(out, err)
			}
			joined := strings.Join(requests(), "\n")
			if !strings.Contains(joined, "/pulls?state=all") || strings.Contains(joined, "since=") {
				t.Fatal(joined)
			}
		})
	}
}

func TestIncrementalMissingPRDetailRestartsAsFullScan(t *testing.T) {
	answers := repositoryAnswers()
	answers["*/issues\\?state=all*"] = `[{"number":9,"state":"closed","pull_request":{}}]`
	answers["repos/acme/orchard/pulls/9"] = "unavailable"
	requests := fakeGitHub(t, answers)
	since := time.Now().Add(-time.Hour)
	out, err := (githubClient{config: inventoryConfig(t)}).snapshotSince(context.Background(), &since, "main")
	if err != nil || out.Incremental || out.Head != headSHA {
		t.Fatal(out, err)
	}
	if requestsMatching(requests(), "/pulls?state=all") != 1 || requestsMatching(requests(), "/pulls/9") != 1 {
		t.Fatal(requests())
	}
}

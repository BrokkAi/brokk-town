package mjolnir

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var evidenceBase = strings.Repeat("a", 40)
var evidenceHead = strings.Repeat("b", 40)

func artifactHeaders(w http.ResponseWriter, media string) {
	w.Header().Set("Mj-Api-Version", "1")
	w.Header().Set("Content-Type", media)
}

func TestSessionEvidenceChecksSavedIdentityAndOmitsPrivateFields(t *testing.T) {
	expected := SessionIdentity{"session/a?b", "workspace", "bundle", Selection{"target", "profile"}}
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.EscapedPath() != "/api/v1/sessions/session%2Fa%3Fb" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("unexpected session identity request")
		}
		artifactHeaders(w, "application/json; charset=utf-8")
		fmt.Fprint(w, `{"id":"session/a?b","workspace_id":"workspace","bundle_id":"bundle","target_id":"target","profile_id":"profile","state":"running","is_idle":true,"error":"private-token","config_options":["private-path"]}`)
	})
	got, err := c.ReadSession(t.Context(), expected)
	if err != nil || got.Identity != expected || !got.Idle {
		t.Fatalf("session evidence: %+v %v", got, err)
	}
	public, _ := json.Marshal(got)
	if strings.Contains(string(public), "private-") {
		t.Fatal("retained private session fields")
	}
	for _, change := range []func(*SessionIdentity){
		func(s *SessionIdentity) { s.Workspace = "other" },
		func(s *SessionIdentity) { s.Bundle = "other" },
		func(s *SessionIdentity) { s.Target = "other" },
		func(s *SessionIdentity) { s.Profile = "other" },
	} {
		wrong := expected
		change(&wrong)
		if got, err := c.ReadSession(t.Context(), wrong); err == nil || got != (SessionState{}) {
			t.Fatal("accepted a mismatched session receipt")
		}
	}
}

func TestDiffEvidenceRequiresExactRevisionsAndAffirmativeRepairAncestry(t *testing.T) {
	for _, scenario := range []struct {
		name                 string
		fields               map[string]any
		read, review, repair bool
	}{
		{"unchanged review", map[string]any{"diff": "", "base": evidenceBase, "head": evidenceBase}, true, true, false},
		{"repair", map[string]any{"diff": "private-patch", "base": evidenceBase, "head": evidenceHead, "head_descends_from_base": true}, true, false, true},
		{"older worker", map[string]any{"diff": "private-patch", "base": evidenceBase, "head": evidenceHead}, true, false, false},
		{"rewritten history", map[string]any{"diff": "private-patch", "base": evidenceBase, "head": evidenceHead, "head_descends_from_base": false}, true, false, false},
		{"dirty review", map[string]any{"diff": "private-patch", "base": evidenceBase, "head": evidenceBase}, true, false, false},
		{"empty repair", map[string]any{"diff": "", "base": evidenceBase, "head": evidenceHead, "head_descends_from_base": true}, true, false, false},
		{"missing patch", map[string]any{"base": evidenceBase, "head": evidenceBase}, false, false, false},
		{"wrong base", map[string]any{"diff": "", "base": evidenceHead, "head": evidenceBase}, false, false, false},
		{"abbreviated head", map[string]any{"diff": "", "base": evidenceBase, "head": "abcdef"}, false, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/sessions/session/diff" || r.URL.Query().Get("base") != evidenceBase || r.URL.Query().Get("json") != "true" {
					t.Error("unexpected diff request")
				}
				artifactHeaders(w, "application/json")
				json.NewEncoder(w).Encode(scenario.fields)
			})
			diff, err := c.ReadDiff(t.Context(), "session", evidenceBase)
			if (err == nil) != scenario.read {
				t.Fatalf("read: %v", err)
			}
			if err != nil {
				if diff != (DiffEvidence{}) {
					t.Fatal("returned partial evidence after failure")
				}
				return
			}
			if (diff.CheckReviewTree("session", evidenceBase) == nil) != scenario.review || (diff.CheckRepairHistory("session", evidenceBase) == nil) != scenario.repair {
				t.Fatal("incorrect review/repair evidence decision")
			}
			if diff.CheckReviewTree("other", evidenceBase) == nil || diff.CheckRepairHistory("other", evidenceBase) == nil {
				t.Fatal("accepted another session's evidence")
			}
			public, _ := json.Marshal(diff)
			if strings.Contains(string(public), "private-patch") {
				t.Fatal("private patch enters JSON projection")
			}
		})
	}
}

func TestArtifactFailuresNeverReturnEmptySuccessOrPrivateErrors(t *testing.T) {
	for _, status := range []int{404, 409, 500, 503, 401, 403, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				artifactHeaders(w, "application/octet-stream")
				w.Header().Set("Location", "/private-token")
				w.WriteHeader(status)
				fmt.Fprint(w, "private-token private-path")
			})
			got, err := c.ReadFile(t.Context(), "session", "result.json")
			if err == nil || got != nil || strings.Contains(err.Error(), "private-") || calls.Load() != 1 {
				t.Fatalf("unsafe artifact failure: %v", err)
			}
			if status == 404 || status == 409 || status >= 500 {
				var failure *ArtifactError
				if !errors.As(err, &failure) || failure.Status != status {
					t.Fatal("lost refusal/failure distinction")
				}
			}
		})
	}
	for _, scenario := range []string{"version", "media", "size", "truncated"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				artifactHeaders(w, "application/octet-stream")
				switch scenario {
				case "version":
					w.Header().Set("Mj-Api-Version", "2")
				case "media":
					w.Header().Set("Content-Type", "text/html")
				case "truncated":
					w.Header().Set("Content-Length", "100")
				}
				fmt.Fprint(w, "private-token")
			})
			limit := int64(100)
			if scenario == "size" {
				limit = 8
			}
			got, err := c.artifact(t.Context(), "GET", "/sessions/session/files", "file", "application/octet-stream", nil, limit, time.Second)
			if err == nil || got != nil || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("unsafe partial artifact: %v", err)
			}
		})
	}
}

func TestArtifactValidationAndDemoNeverContactDaemon(t *testing.T) {
	var calls atomic.Int32
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, filename := range []string{"", "/etc/passwd", "../sibling", "a/../b", "a\\b", "result.json\n", " result.json", "."} {
		if _, err := c.ReadFile(t.Context(), "session", filename); err == nil {
			t.Fatal("accepted an unsafe or ambiguous path")
		}
	}
	for _, id := range []string{"", ".", "..", "bad\n"} {
		if _, err := c.ReadDiff(t.Context(), id, evidenceBase); err == nil {
			t.Fatal("accepted an invalid session ID")
		}
	}
	if _, err := c.ReadDiff(t.Context(), "session", "HEAD~2"); err == nil {
		t.Fatal("accepted a mutable revision")
	}
	demo := New(t.TempDir(), true, c.connection)
	_, e1 := demo.ReadFile(t.Context(), "session", "result.json")
	_, e2 := demo.ReadDiff(t.Context(), "session", evidenceBase)
	_, e3 := demo.ExportBundle(t.Context(), "session")
	_, e4 := demo.ReadTranscript(t.Context(), "session", 0)
	_, e5 := demo.ReadSession(t.Context(), SessionIdentity{"session", "workspace", "bundle", Selection{"target", "profile"}})
	if e1 == nil || e2 == nil || e3 == nil || e4 == nil || e5 == nil || calls.Load() != 0 {
		t.Fatal("invalid evidence request or demo contacted the daemon")
	}
}

func TestFileEvidenceEscapesItsPathAndPreservesBytes(t *testing.T) {
	filename := "receipts/review #1?.json"
	want := []byte{0, 1, 2, 255}
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.EscapedPath() != "/api/v1/sessions/session%2Fone/files" || r.URL.Query().Get("path") != filename || len(r.URL.Query()) != 1 {
			t.Error("file path or session escaped into another request")
		}
		artifactHeaders(w, "application/octet-stream")
		w.Write(want)
	})
	got, err := c.ReadFile(t.Context(), "session/one", filename)
	if err != nil || string(got) != string(want) {
		t.Fatalf("file evidence changed: %v", err)
	}
}

func TestArtifactDeadlineAndEmptyBundleRemainFailures(t *testing.T) {
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		artifactHeaders(w, "application/octet-stream")
		if r.Method == "POST" {
			return // A successful response without a Git bundle proves nothing.
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	if data, err := c.ExportBundle(t.Context(), "session"); err == nil || data != nil {
		t.Fatal("accepted an empty bundle")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	data, err := c.artifact(ctx, "GET", "/sessions/session/files", "file", "application/octet-stream", nil, 10, 20*time.Millisecond)
	if err == nil || data != nil || ctx.Err() != nil {
		t.Fatal("artifact deadline did not independently bound the body read")
	}
}

func TestArtifactCancellationRetainsUnconfirmedExportWithoutRetry(t *testing.T) {
	for _, sendHeaders := range []bool{false, true} {
		t.Run(fmt.Sprint(sendHeaders), func(t *testing.T) {
			started := make(chan struct{})
			var calls atomic.Int32
			c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.Copy(io.Discard, r.Body)
				if sendHeaders {
					artifactHeaders(w, "application/octet-stream")
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				data, err := c.ExportBundle(ctx, "session")
				if data != nil {
					t.Error("returned an incomplete bundle")
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "not confirmed") || calls.Load() != 1 {
					t.Fatalf("lost cancellation or uncertainty: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("artifact read did not cancel")
			}
		})
	}
}

func TestTranscriptPagesKeepSequenceTiesAndStreamingUpdates(t *testing.T) {
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("limit") != "200" {
			t.Error("unexpected transcript request")
		}
		artifactHeaders(w, "application/json")
		items := []map[string]any{}
		next := 1
		if r.URL.Query().Get("after_seq") == "0" {
			// The daemon includes all items at the boundary sequence, even when
			// that makes the page larger than the requested limit.
			for i := 0; i < 201; i++ {
				items = append(items, map[string]any{"stable_id": fmt.Sprint(i), "position": 1, "seq": 1, "role": "agent", "text": "private-text", "body": "private-body"})
			}
		} else {
			next = 2
			items = append(items, map[string]any{"stable_id": "0", "position": 1, "seq": 2, "role": "agent", "text": "private-updated-text"})
		}
		json.NewEncoder(w).Encode(map[string]any{"session_id": "session", "latest_seq": 2, "next_after_seq": next, "items": items})
	})
	first, err := c.ReadTranscript(t.Context(), "session", 0)
	if err != nil || first.Complete() || first.Next != 1 || len(first.Items) != 201 {
		t.Fatalf("incomplete page: items=%d %v", len(first.Items), err)
	}
	last, err := c.ReadTranscript(t.Context(), "session", first.Next)
	if err != nil || !last.Complete() || last.Items[0].Position != 1 || last.Items[0].Sequence != 2 || last.Items[0].Text != "private-updated-text" {
		t.Fatalf("streaming update: %v", err)
	}
	public, _ := json.Marshal(last)
	if strings.Contains(string(public), "private-") {
		t.Fatal("private transcript enters JSON projection")
	}
}

func TestTranscriptRefusesMissingOrSkippedEvidence(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"session_id":"other","latest_seq":0,"items":[]}`,
		`{"session_id":"session","latest_seq":3,"items":[]}`,
		`{"session_id":"session","latest_seq":0,"items":null}`,
		`{"session_id":"session","latest_seq":3,"next_after_seq":3,"items":[{"stable_id":"a","position":1,"seq":1,"role":"agent","text":"x"}]}`,
		`{"session_id":"session","latest_seq":1,"items":[{"stable_id":"a","position":1,"seq":2,"role":"agent","text":"x"}]}`,
		`{"session_id":"session","latest_seq":1,"items":[{"stable_id":"a","position":1,"seq":1,"role":"agent"}]}`,
	} {
		c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
			artifactHeaders(w, "application/json")
			fmt.Fprint(w, body)
		})
		if page, err := c.ReadTranscript(t.Context(), "session", 0); err == nil || page.Complete() || page.Items != nil {
			t.Fatal("accepted incomplete transcript evidence")
		}
	}
}

func TestExportedRepairBundleCanBeVerifiedAtItsExactHead(t *testing.T) {
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git failed: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	source, destination := t.TempDir(), t.TempDir()
	git(source, "init", "-b", "main")
	git(source, "commit", "--allow-empty", "-m", "base")
	base := git(source, "rev-parse", "HEAD")
	git(source, "switch", "-c", "town/fixture")
	if err := os.WriteFile(filepath.Join(source, "result.txt"), []byte("verified fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(source, "add", "result.txt")
	git(source, "commit", "-m", "repair")
	head := git(source, "rev-parse", "HEAD")
	bundle := filepath.Join(t.TempDir(), "work.bundle")
	git(source, "bundle", "create", bundle, base+"..HEAD")
	bytes, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := catalogFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/diff") {
			artifactHeaders(w, "application/json")
			json.NewEncoder(w).Encode(map[string]any{"base": base, "head": head, "diff": "new result.txt", "head_descends_from_base": true})
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/api/v1/sessions/session/export" || string(body) != `{"kind":"bundle"}` || r.Header.Get("Content-Type") != "application/json" {
			t.Error("export must only request a bundle; never push a branch")
		}
		artifactHeaders(w, "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="../../ignored.bundle"`)
		w.Write(bytes)
	})
	diff, err := c.ReadDiff(t.Context(), "session", base)
	if err != nil || diff.CheckRepairHistory("session", base) != nil {
		t.Fatalf("repair evidence: %v", err)
	}
	data, err := c.ExportBundle(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "evidence.bundle")
	if err := os.WriteFile(local, data, 0600); err != nil {
		t.Fatal(err)
	}
	git(destination, "init", "-b", "verification")
	git(destination, "fetch", "--no-tags", source, base)
	git(destination, "bundle", "verify", local)
	git(destination, "fetch", "--no-tags", local, "HEAD")
	if git(destination, "rev-parse", "FETCH_HEAD") != diff.Head {
		t.Fatal("bundle does not contain the recorded repair head")
	}
	git(destination, "merge-base", "--is-ancestor", base, head)
	if git(destination, "show", head+":result.txt") != "verified fixture" {
		t.Fatal("repair content not available for independent verification")
	}
}

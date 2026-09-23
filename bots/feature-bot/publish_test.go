package featurebot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// proposalFinding returns a distinct finding the fake reviewer does not treat as
// a duplicate of the others.
func proposalFinding(n int) Finding {
	f := finding()
	f.Title = fmt.Sprintf("Proposal %d: export tasks", n)
	f.UserProblem = fmt.Sprintf("Coordinators in workflow %d cannot export filtered tasks", n)
	return f
}

// dryRunProposals saves n completed dry-run proposals, then returns the fixture
// reloaded from disk as a restarted process would see it.
func dryRunProposals(t *testing.T, n int) (engine, *State, *fakeSource, *fakeAgent, string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", canonicalTestDir(t))
	e, s, f, a, source := fixture(t)
	for i := range n {
		a.findings = append(a.findings, proposalFinding(i))
	}
	e.config.MaxIssues = max(n, 1)
	e.config.DryRun = true
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if len(s.Completed) != n || f.creates != 0 {
		t.Fatalf("dry run: completed=%d creates=%d", len(s.Completed), f.creates)
	}
	saved, err := ReadState(e.config)
	if err != nil {
		t.Fatal(err)
	}
	return e, saved, f, a, source
}

func publishOutput(t *testing.T, e engine, selector string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := e.publishSaved(context.Background(), selector, &out)
	return out.String(), err
}

func savedState(t *testing.T, e engine) *State {
	t.Helper()
	s, err := ReadState(e.config)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func candidateJSON(t *testing.T, c *Candidate) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProposalListShowsSelectorsWithoutSideEffects(t *testing.T) {
	e, s, f, a, _ := dryRunProposals(t, 3)
	s.Completed[2].Commit = "" // saved by an older release
	if err := writeState(e.config, s); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(e.config.StateDirectory, "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	scans, reviews, reads := a.scans, a.reviews, f.reads
	var out strings.Builder
	if err := WriteProposalList(&out, savedState(t, e)); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for i, c := range s.Completed {
		line := ""
		for _, l := range strings.Split(got, "\n") {
			if strings.HasPrefix(l, ProposalSelector(c)+" ") {
				line = l
			}
		}
		if !strings.Contains(line, c.Finding.Title) {
			t.Fatalf("proposal %d missing: %s", i, got)
		}
		want := "eligible"
		if i == 2 {
			want = "ineligible: no recorded source commit"
		} else if !strings.Contains(line, c.Commit[:12]) {
			t.Fatalf("commit missing: %s", line)
		}
		if !strings.Contains(line, want) {
			t.Fatalf("eligibility of %d: %s", i, line)
		}
	}
	if strings.Contains(got, s.Completed[0].RequestID) {
		t.Fatal("listing exposed the publication marker")
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) || a.scans != scans || a.reviews != reviews || f.reads != reads || f.creates != 0 {
		t.Fatal("listing changed state or contacted an agent or GitHub")
	}
	// Selectors are stable across restarts.
	var again strings.Builder
	if err := WriteProposalList(&again, savedState(t, e)); err != nil || again.String() != got {
		t.Fatalf("unstable listing: %v", err)
	}
	var empty strings.Builder
	if err := WriteProposalList(&empty, nil); err != nil || !strings.Contains(empty.String(), "No saved dry-run proposals") {
		t.Fatalf("empty listing: %q %v", empty.String(), err)
	}
}

func TestPublishSelectedProposalOnly(t *testing.T) {
	e, s, f, a, _ := dryRunProposals(t, 3)
	selected := s.Completed[1]
	selector := ProposalSelector(selected)
	originalFinding := candidateJSON(t, &Candidate{Finding: selected.Finding})
	others := []string{candidateJSON(t, s.Completed[0]), candidateJSON(t, s.Completed[2])}
	scans, reviews := a.scans, a.reviews
	out, err := publishOutput(t, e, selector)
	if err != nil {
		t.Fatal(err)
	}
	saved := savedState(t, e)
	c := saved.Completed[1]
	if a.scans != scans || a.reviews <= reviews || f.creates != 1 {
		t.Fatalf("scans=%d reviews=%d creates=%d", a.scans-scans, a.reviews-reviews, f.creates)
	}
	if c.Status != "submitted" || c.URL != f.items[0].URL || !strings.Contains(out, c.URL) || f.items[0].Title != selected.Finding.Title {
		t.Fatalf("outcome not recorded: %+v %q", c, out)
	}
	if candidateJSON(t, &Candidate{Finding: c.Finding}) != originalFinding || !strings.Contains(f.items[0].Body, marker(selected.RequestID)) || !strings.Contains(f.items[0].Body, selected.Commit) {
		t.Fatal("finding fields or request identity changed")
	}
	if candidateJSON(t, saved.Completed[0]) != others[0] || candidateJSON(t, saved.Completed[2]) != others[1] {
		t.Fatal("other saved proposals changed")
	}
	if saved.Publication != nil || saved.Scan != nil || len(saved.Workspaces) != 2 || saved.Workspaces[1].Commit != selected.Commit {
		t.Fatalf("publication not retired: %+v", saved)
	}
	// Repeating a successful selection returns its saved outcome.
	reviews = a.reviews
	out, err = publishOutput(t, e, selector)
	if err != nil || f.creates != 1 || a.reviews != reviews || !strings.Contains(out, c.URL) {
		t.Fatalf("repeat created or reviewed again: %v creates=%d %q", err, f.creates, out)
	}
}

func TestPublishClearsDryRunCheckpointAndReviewsCurrentHistory(t *testing.T) {
	e, s, f, a, _ := dryRunProposals(t, 1)
	if s.Completed[0].Checkpoint == nil || !s.Completed[0].Checkpoint.Validated {
		t.Fatal("fixture lacks a dry-run checkpoint")
	}
	f.items = []Issue{{Number: 3, Title: "Closed idea", Body: "unrelated", State: "closed", Comments: []string{"discussion"}}}
	var reviewed []Issue
	a.onReview = func(issues []Issue) Review {
		reviewed = append(reviewed, issues...)
		return Review{Verdict: "new", Reason: "Fresh independent review against current history"}
	}
	if _, err := publishOutput(t, e, ProposalSelector(s.Completed[0])); err != nil {
		t.Fatal(err)
	}
	c := savedState(t, e).Completed[0]
	if len(reviewed) != 1 || !strings.Contains(reviewed[0].Body, "discussion") || c.Review != "Fresh independent review against current history" || !strings.Contains(f.items[1].Body, c.Review) {
		t.Fatalf("fresh review missing: %+v %+v", reviewed, c)
	}
}

func TestPublishLegacyProposalRefused(t *testing.T) {
	e, s, f, a, _ := dryRunProposals(t, 1)
	s.Completed[0].Commit = ""
	if err := writeState(e.config, s); err != nil {
		t.Fatal(err)
	}
	reviews := a.reviews
	_, err := publishOutput(t, e, ProposalSelector(s.Completed[0]))
	if err == nil || !strings.Contains(err.Error(), "without its source commit") || !strings.Contains(err.Error(), "--dry-run") {
		t.Fatalf("legacy refusal: %v", err)
	}
	if a.reviews != reviews || f.creates != 0 || savedState(t, e).Publication != nil {
		t.Fatal("legacy proposal was processed")
	}
	if _, err := publishOutput(t, e, "000000000000"); err == nil || !strings.Contains(err.Error(), "--list") {
		t.Fatalf("unknown selector: %v", err)
	}
}

func TestPublishReviewGatesPreserveExplanation(t *testing.T) {
	for _, verdict := range []string{"duplicate", "uncertain", "invalid"} {
		t.Run(verdict, func(t *testing.T) {
			e, s, f, a, _ := dryRunProposals(t, 1)
			f.items = []Issue{{Number: 4, Title: "Spreadsheet export", Body: "Filed after the dry run", State: "open", URL: "https://github.com/o/r/issues/4"}}
			reason := "Fresh review: " + verdict
			a.onReview = func([]Issue) Review {
				r := Review{Verdict: verdict, Reason: reason}
				if verdict == "duplicate" {
					r.Duplicate = 4
				}
				return r
			}
			out, err := publishOutput(t, e, ProposalSelector(s.Completed[0]))
			if err == nil || !strings.Contains(err.Error(), verdict) || !strings.Contains(out, reason) {
				t.Fatalf("verdict not reported: %v %q", err, out)
			}
			c := savedState(t, e).Completed[0]
			if f.creates != 0 || c.Status != verdict || c.Review != reason {
				t.Fatalf("gate failed: creates=%d %+v", f.creates, c)
			}
			if verdict == "duplicate" && c.URL != "https://github.com/o/r/issues/4" {
				t.Fatal("duplicate URL lost")
			}
		})
	}
	t.Run("incomplete", func(t *testing.T) {
		e, s, f, a, _ := dryRunProposals(t, 1)
		f.items = []Issue{{Number: 1}, {Number: 2}}
		e.agent = func(Config, string) Agent {
			return reviewAgentFunc(func(context.Context, string) (string, error) {
				a.reviews++
				return "FEATURE_REVIEW " + jsonContextCompact(Review{Verdict: "new", Reason: "Reviewed", Checked: []int{1}}), nil
			})
		}
		_, err := publishOutput(t, e, ProposalSelector(s.Completed[0]))
		saved := savedState(t, e)
		if err == nil || f.creates != 0 || saved.Completed[0].Status != "dry_run" || saved.Publication == nil || !strings.Contains(saved.Publication.Failure, "correction exhausted") {
			t.Fatalf("incomplete review: %v creates=%d %+v", err, f.creates, saved.Publication)
		}
	})
}

func TestPublishVerifiesSourceAndEvidence(t *testing.T) {
	for _, mode := range []string{"verifier", "tracked", "missing-file"} {
		t.Run(mode, func(t *testing.T) {
			e, s, f, _, _ := dryRunProposals(t, 1)
			switch mode {
			case "verifier":
				e.config.Verify = []string{"sh", "-c", `test "$FEATURE_COMMIT" = "` + s.Completed[0].Commit + `" && exit 7`}
			case "tracked":
				e.config.Verify = []string{"sh", "-c", "echo changed > README.md"}
			case "missing-file":
				s.Completed[0].Finding.Files = []string{"missing.go"}
				if err := writeState(e.config, s); err != nil {
					t.Fatal(err)
				}
			}
			_, err := publishOutput(t, e, ProposalSelector(s.Completed[0]))
			want := map[string]string{"verifier": "operator verification", "tracked": "modified tracked source", "missing-file": "does not exist"}[mode]
			if err == nil || !strings.Contains(err.Error(), want) || f.creates != 0 {
				t.Fatalf("expected %q: %v creates=%d", want, err, f.creates)
			}
			saved := savedState(t, e)
			if saved.Completed[0].Status != "dry_run" || saved.Publication == nil || !strings.Contains(saved.Publication.Failure, want) {
				t.Fatalf("failure not retained: %+v", saved.Publication)
			}
		})
	}
}

func TestPublishRefusesSourceAdvancementWithoutDiscovery(t *testing.T) {
	advance := func(t *testing.T, source string) {
		localGit(t, source, "switch", "main")
		writeTestFile(t, filepath.Join(source, "next.txt"), "new commit")
		localGit(t, source, "add", ".")
		localGit(t, source, "commit", "-m", "advance")
		localGit(t, source, "push", "origin", "main")
	}
	for _, when := range []string{"before", "during"} {
		t.Run(when, func(t *testing.T) {
			e, s, f, a, source := dryRunProposals(t, 1)
			scans, reviews := a.scans, a.reviews
			if when == "before" {
				advance(t, source)
			} else {
				a.onReview = func([]Issue) Review {
					advance(t, source)
					return Review{Verdict: "new", Reason: "Reviewed"}
				}
			}
			_, err := publishOutput(t, e, ProposalSelector(s.Completed[0]))
			if err == nil || !strings.Contains(err.Error(), "no replacement discovery") {
				t.Fatalf("advancement accepted: %v", err)
			}
			if when == "before" && a.reviews != reviews {
				t.Fatal("reviewed a proposal at an outdated commit")
			}
			saved := savedState(t, e)
			if a.scans != scans || f.creates != 0 || saved.Scan != nil || saved.Completed[0].Status != "dry_run" {
				t.Fatalf("scans=%d creates=%d scan=%+v", a.scans-scans, f.creates, saved.Scan)
			}
		})
	}
}

func TestPublishRejectsUnrelatedActiveScan(t *testing.T) {
	e, s, f, a, _ := dryRunProposals(t, 1)
	s.Scan = &Scan{Commit: s.Completed[0].Commit, Directory: filepath.Join(e.config.Directory+"-scans", "scan-active")}
	if err := writeState(e.config, s); err != nil {
		t.Fatal(err)
	}
	reviews := a.reviews
	_, err := publishOutput(t, e, ProposalSelector(s.Completed[0]))
	if err == nil || !strings.Contains(err.Error(), "unrelated scan") || a.reviews != reviews || f.creates != 0 {
		t.Fatalf("active scan not rejected: %v", err)
	}
	var out strings.Builder
	if err := WriteProposalList(&out, savedState(t, e)); err != nil || !strings.Contains(out.String(), "blocked: unrelated active scan") {
		t.Fatalf("listing: %q %v", out.String(), err)
	}
}

func TestPublishLostCreateReconcilesAfterRestart(t *testing.T) {
	e, s, f, a, _ := dryRunProposals(t, 2)
	selector := ProposalSelector(s.Completed[0])
	f.lost = true
	if _, err := publishOutput(t, e, selector); err == nil {
		t.Fatal("expected lost response")
	}
	saved := savedState(t, e)
	if saved.Completed[0].Status != "posting" || saved.Publication == nil || saved.Publication.RequestID != s.Completed[0].RequestID {
		t.Fatalf("publication intent lost: %+v", saved.Publication)
	}
	// Another selection may not start while the outcome is unknown.
	if _, err := publishOutput(t, e, ProposalSelector(s.Completed[1])); err == nil || !strings.Contains(err.Error(), "unknown outcome") {
		t.Fatalf("second selection allowed: %v", err)
	}
	var out strings.Builder
	if err := WriteProposalList(&out, savedState(t, e)); err != nil || !strings.Contains(out.String(), "unknown outcome") {
		t.Fatalf("listing: %q", out.String())
	}
	f.lost = false
	reviews := a.reviews
	if _, err := publishOutput(t, e, selector); err != nil {
		t.Fatal(err)
	}
	saved = savedState(t, e)
	if f.creates != 1 || a.reviews != reviews || saved.Completed[0].Status != "submitted" || saved.Completed[0].URL != f.items[0].URL || saved.Publication != nil {
		t.Fatalf("not reconciled: creates=%d %+v", f.creates, saved.Completed[0])
	}
	if _, err := publishOutput(t, e, selector); err != nil || f.creates != 1 {
		t.Fatalf("repeat: %v creates=%d", err, f.creates)
	}
}

func TestPublishUnknownOutcomeNeverReposts(t *testing.T) {
	e, s, f, _, _ := dryRunProposals(t, 1)
	selector := ProposalSelector(s.Completed[0])
	f.createErr = errors.New("timeout")
	for range 3 {
		if _, err := publishOutput(t, e, selector); err == nil {
			t.Fatal("unknown outcome accepted")
		}
	}
	if f.creates != 1 || savedState(t, e).Completed[0].Status != "posting" {
		t.Fatalf("reposted: creates=%d", f.creates)
	}
}

func TestPublishInterruptedReviewResumesSameRequest(t *testing.T) {
	e, s, f, a, _ := dryRunProposals(t, 1)
	selector := ProposalSelector(s.Completed[0])
	requestID := s.Completed[0].RequestID
	f.items = []Issue{{Number: 7, Body: strings.Repeat("a", 24000) + strings.Repeat("b", 24000)}}
	calls := 0
	e.agent = func(Config, string) Agent {
		return reviewAgentFunc(func(ctx context.Context, prompt string) (string, error) {
			calls++
			if calls == 2 {
				return "", errors.New("interrupted")
			}
			return a.Execute(ctx, prompt)
		})
	}
	if _, err := publishOutput(t, e, selector); err == nil {
		t.Fatal("expected interruption")
	}
	saved := savedState(t, e)
	c := saved.Completed[0]
	if saved.Publication == nil || c.Status != "dry_run" || c.Checkpoint == nil || len(c.Checkpoint.Batches) != 1 || !c.Checkpoint.Validated {
		t.Fatalf("interrupted selection not retained: %+v %+v", saved.Publication, c.Checkpoint)
	}
	// The resumed operation keeps its fresh checkpoint instead of clearing it again.
	calls = 10
	if _, err := publishOutput(t, e, selector); err != nil {
		t.Fatal(err)
	}
	if calls != 11 || f.creates != 1 || !strings.Contains(f.items[1].Body, marker(requestID)) {
		t.Fatalf("resume: calls=%d creates=%d", calls-10, f.creates)
	}
}

func TestPublishRejectedCreateKeepsDryRunAndRetries(t *testing.T) {
	e, s, f, _, _ := dryRunProposals(t, 1)
	selector := ProposalSelector(s.Completed[0])
	f.createErr = &rejectedCreateError{errors.New("validation failed")}
	if _, err := publishOutput(t, e, selector); err == nil {
		t.Fatal("expected rejection")
	}
	saved := savedState(t, e)
	if saved.Completed[0].Status != "dry_run" || saved.Publication == nil {
		t.Fatalf("rejection: %+v", saved.Completed[0])
	}
	f.createErr = nil
	if _, err := publishOutput(t, e, selector); err != nil || f.creates != 2 || savedState(t, e).Completed[0].Status != "submitted" {
		t.Fatalf("retry: %v creates=%d", err, f.creates)
	}
}

func TestPublishRefusesExistingMarker(t *testing.T) {
	e, s, f, a, _ := dryRunProposals(t, 1)
	c := s.Completed[0]
	// Someone filed the logged dry-run body by hand, marker included.
	f.items = []Issue{{Number: 9, Title: c.Finding.Title, Body: issueBody(c, c.Commit), State: "open", URL: "https://github.com/o/r/issues/9"}}
	a.onReview = func([]Issue) Review { return Review{Verdict: "new", Reason: "Reviewer missed it"} }
	_, err := publishOutput(t, e, ProposalSelector(c))
	if err == nil || !strings.Contains(err.Error(), "publication marker") || f.creates != 0 {
		t.Fatalf("marker not guarded: %v creates=%d", err, f.creates)
	}
}

func TestDryRunProvenanceSurvivesRestart(t *testing.T) {
	e, s, _, _, _ := dryRunProposals(t, 2)
	head := localGit(t, e.config.Directory, "rev-parse", "refs/remotes/origin/main")
	for _, c := range s.Completed {
		if c.Commit != head {
			t.Fatalf("commit %q, want %q", c.Commit, head)
		}
	}
	var report strings.Builder
	if err := WriteReport(&report, e.config, s, "dry_run"); err != nil || !strings.Contains(report.String(), "Researched at commit: "+head) {
		t.Fatalf("report: %v", err)
	}
	s.Completed[0].Commit = "not-a-commit"
	if err := writeState(e.config, s); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(e.config); err == nil {
		t.Fatal("invalid provenance accepted")
	}
}

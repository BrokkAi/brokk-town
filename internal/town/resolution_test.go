package town

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// secondReview returns a certified audit of the fixture revision that still
// has the given open findings after issue-bot's fix round.
func secondReview(findings ...Finding) *Audit {
	a := clean()
	a.Verdict = "changes_needed"
	a.Findings = findings
	return a
}

func settle(t *testing.T, s *Store, id string, audit *Audit, severities map[string]string) *Town {
	t.Helper()
	sup := NewSupervisor(s, newGH(1), workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		return RunResult{PR: 1, Audit: audit, Severities: severities}, nil
	}))
	sup.execute(context.Background(), s.Snapshot().Towns[id], Review)
	return s.Snapshot().Towns[id]
}

func TestSecondReviewClosesOnBlockingFindingsAndDefersTheRest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold string
		findings  []Finding
		closes    bool
		deferred  int
	}{
		{"P2 still open closes under the default threshold", "", []Finding{{ID: "finding:a", State: "open", Detail: "[P2] wrong result", Severity: "P2"}, {ID: "finding:b", State: "open", Detail: "[P3] naming", Severity: "P3"}}, true, 0},
		{"only P3 left merges with follow-ups", "", []Finding{{ID: "finding:b", State: "open", Detail: "[P3] naming", Severity: "P3"}, {ID: "new:c", State: "uncertain", Detail: "logging could be tighter", Severity: "P3"}}, false, 2},
		{"P2 becomes a follow-up when the town only closes on P1", "P1", []Finding{{ID: "finding:a", State: "open", Detail: "[P2] wrong result", Severity: "P2"}}, false, 1},
		{"a finding nobody rated blocks", "", []Finding{{ID: "new:d", State: "open", Detail: "unknown impact"}}, true, 0},
		{"the reviewer's rating stands when the certifier gave none", "", []Finding{{ID: "finding:a", State: "open", Detail: "wrong result"}}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t, false)
			x := setupPR(t, s, 1)
			update(t, s, func(st *State) {
				town := st.Towns[x.ID]
				town.Config.ReviewCloseSeverity = tc.threshold
				town.Workers[Review].Enabled = true
				task := town.Tasks["pr:1"]
				task.Stage, task.House, task.Audit, task.Cycles = "queued", Review, nil, 1
			})
			town := settle(t, s, x.ID, secondReview(tc.findings...), map[string]string{"finding:a": "P2", "finding:b": "P3"})
			task := town.Tasks["pr:1"]
			if tc.closes {
				if task.Stage != "closing" || task.House != Hall || !strings.Contains(task.Detail, "closing this pull request") {
					t.Fatalf("blocking findings did not close the pull request: %+v", task)
				}
				if !town.Workers[Repo].Next.IsZero() {
					t.Fatal("inventory not woken to perform the close")
				}
				return
			}
			if task.Stage != "ready" || task.House != Review || len(task.FollowUps) != tc.deferred {
				t.Fatalf("follow-up work did not let the pull request merge: %+v", task)
			}
			if !task.Audit.Clean(baseSHA, headSHA) {
				t.Fatalf("deferred findings still block the merge gate: %+v", task.Audit)
			}
			for _, f := range task.Audit.Findings {
				if f.State != "deferred" {
					t.Fatalf("open finding not deferred: %+v", f)
				}
			}
		})
	}
}

func TestFollowUpsAreFiledOnceThePullRequestMerges(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		task := town.Tasks["pr:1"]
		task.FollowUps = []Finding{{ID: "finding:b", State: "deferred", Detail: "[P3] Rename the helper\nIt shadows a builtin.", Severity: "P3"}, {ID: "new:c", State: "deferred", Detail: "Tighten logging", Severity: "P3"}}
	})
	gh := newGH(1)
	sup := NewSupervisor(s, gh, workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		return RunResult{}, nil
	}))
	// Nothing is filed while the pull request is still open.
	if err := sup.fileFollowUps(context.Background(), s.Snapshot().Towns[x.ID]); err != nil || len(gh.filed) != 0 {
		t.Fatalf("follow-ups filed before the merge: %v %d", err, len(gh.filed))
	}
	update(t, s, func(st *State) { st.Towns[x.ID].Tasks["pr:1"].Stage = "merged" })
	if err := sup.fileFollowUps(context.Background(), s.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.filed) != 2 || gh.filed[0].Title != "Follow-up: Rename the helper" || !strings.Contains(gh.filed[0].Body, "<!-- review-bot:follow-up pr=1") {
		t.Fatalf("follow-ups not filed as issues: %+v", gh.filed)
	}
	town := s.Snapshot().Towns[x.ID]
	if len(town.Tasks["pr:1"].FollowUps) != 0 {
		t.Fatalf("filed follow-ups were kept: %+v", town.Tasks["pr:1"].FollowUps)
	}
	filed := 0
	for _, o := range town.Outcomes {
		if o.Kind == "followup_filed" {
			filed++
		}
	}
	if filed != 2 {
		t.Fatalf("follow-up outcomes: %d", filed)
	}
	// Idempotent: a second pass files nothing more.
	if err := sup.fileFollowUps(context.Background(), town); err != nil || len(gh.filed) != 2 {
		t.Fatalf("follow-ups filed twice: %v %d", err, len(gh.filed))
	}
	// A filed follow-up arrives as Town's own review work, not an outside issue.
	update(t, s, func(st *State) {
		Reconcile(st, st.Towns[x.ID], RepoSnapshot{Branch: "main", Head: baseSHA, Issues: gh.filed}, time.Now())
	})
	if task := s.Snapshot().Towns[x.ID].Tasks["issue:900"]; task == nil || task.External || task.House != Simplifier {
		t.Fatalf("follow-up issue not routed as Town's own work: %+v", task)
	}
}

func TestInconclusiveReviewCountsAsAFailedAttempt(t *testing.T) {
	for _, external := range []bool{false, true} {
		s := testStore(t, false)
		x := setupPR(t, s, 1)
		update(t, s, func(st *State) {
			town := st.Towns[x.ID]
			town.Workers[Review].Enabled = true
			task := town.Tasks["pr:1"]
			task.Stage, task.House, task.Audit, task.External = "queued", Review, nil, external
		})
		inconclusive := clean()
		inconclusive.Verdict = "inconclusive"
		inconclusive.Complete = false
		town := settle(t, s, x.ID, inconclusive, nil)
		if task := town.Tasks["pr:1"]; task.Stage != "queued" || task.Attempts != 1 || task.Audit != nil || task.Blocked {
			t.Fatalf("first inconclusive review did not earn one more attempt: %+v", task)
		}
		update(t, s, func(st *State) { st.Towns[x.ID].Tasks["pr:1"].RetryAt = time.Time{} })
		town = settle(t, s, x.ID, inconclusive, nil)
		task := town.Tasks["pr:1"]
		if external {
			if task.Stage != "awaiting_mayor" || task.House != Hall || task.MayoralDecision != "pending" {
				t.Fatalf("external PR did not reach the Mayor: %+v", task)
			}
		} else if task.Stage != "closing" || task.House != Hall {
			t.Fatalf("owned PR was not retired: %+v", task)
		}
	}
}

func TestNextTaskPrefersPullRequestsThatHaveNotFailed(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	update(t, s, func(st *State) {
		town := st.Towns[x.ID]
		task := town.Tasks["pr:1"]
		task.Stage, task.House, task.Audit, task.Attempts = "queued", Review, nil, 1
		town.Tasks["pr:7"] = &Task{ID: "pr:7", Kind: "pr", Number: 7, Stage: "queued", House: Review, Base: baseSHA, Head: headSHA}
		town.Tasks["pr:9"] = &Task{ID: "pr:9", Kind: "pr", Number: 9, Stage: "queued", House: Review, Base: baseSHA, Head: headSHA}
	})
	town := s.Snapshot().Towns[x.ID]
	if next := nextTask(town, Review, "queued"); next == nil || next.Number != 7 {
		t.Fatalf("a failed pull request was picked ahead of untried ones: %+v", next)
	}
	update(t, s, func(st *State) {
		st.Towns[x.ID].Tasks["pr:7"].Attempts = 1
		st.Towns[x.ID].Tasks["pr:9"].Attempts = 1
	})
	if next := nextTask(s.Snapshot().Towns[x.ID], Review, "queued"); next == nil || next.Number != 1 {
		t.Fatalf("ties are not broken by number: %+v", next)
	}
}

func TestRequeuedIssueStaysQueuedUntilIssueBotStartsOver(t *testing.T) {
	root := t.TempDir()
	store := testStore(t, false)
	town := addTown(t, store)
	update(t, store, func(st *State) {
		st.Towns[town.ID].Tasks["issue:8"] = &Task{ID: "issue:8", Kind: "issue", Number: 8, Title: "Redo", House: Issue, Stage: "queued", Requeue: 18}
	})
	town = store.Snapshot().Towns[town.ID]
	job := &issueJobSummary{Branch: "issue-bot/8", Status: "submitted", Tries: 1, URL: "https://github.com/acme/orchard/pull/18"}
	writeIssueBotState(t, root, town, map[int]*issueJobSummary{8: job})
	workers := &BotWorkers{Root: root, Store: store, jobsQuery: fixtureIssueQuery(root)}
	if err := workers.SyncIssues(town); err != nil {
		t.Fatal(err)
	}
	if task := store.Snapshot().Towns[town.ID].Tasks["issue:8"]; task.Stage != "queued" || task.Requeue != 18 || task.Attempts != 0 {
		t.Fatalf("the closed PR's job put the issue back to implemented: %+v", task)
	}
	if next := nextIssue(store.Snapshot().Towns[town.ID]); next == nil || next.Number != 8 || next.Requeue != 18 {
		t.Fatalf("requeued issue is not dispatched with its superseded PR: %+v", next)
	}
	// Once issue-bot has started over, the requeue marker is spent.
	job.Status, job.URL, job.Tries = "pending", "", 0
	writeIssueBotState(t, root, town, map[int]*issueJobSummary{8: job})
	if err := workers.SyncIssues(store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if task := store.Snapshot().Towns[town.ID].Tasks["issue:8"]; task.Requeue != 0 || task.Stage != "queued" {
		t.Fatalf("requeue marker not cleared: %+v", task)
	}
}

func TestReviewCloseSeverityIsATownSetting(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	sup := NewSupervisor(s, newGH(1), workerFunc(func(context.Context, *Town, Role, func(Progress), *slog.Logger) (RunResult, error) {
		return RunResult{}, nil
	}))
	bad := "P9"
	if err := sup.SettingsForRoleAndPolicy(x.ID, "", AgentSettings{}, nil, nil, &bad); err == nil {
		t.Fatal("accepted an unknown severity")
	}
	p1 := "P1"
	if err := sup.SettingsForRoleAndPolicy(x.ID, "", AgentSettings{}, nil, nil, &p1); err != nil {
		t.Fatal(err)
	}
	cfg := s.Snapshot().Towns[x.ID].Config
	if cfg.ReviewCloseSeverityOrDefault() != "P1" || cfg.Public().ReviewCloseSeverity != "P1" {
		t.Fatalf("setting not applied: %+v", cfg)
	}
	if DefaultConfig("acme/orchard").ReviewCloseSeverityOrDefault() != "P2" {
		t.Fatal("default threshold changed")
	}
	if !Blocking("P1", "P2") || !Blocking("P2", "P2") || Blocking("P3", "P2") || !Blocking("", "P2") || Blocking("P2", "P1") {
		t.Fatal("blocking rule wrong")
	}
}

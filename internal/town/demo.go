package town

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

// Demo is a closed simulation: it never receives a GitHub client or worker runner.
func Demo(ctx context.Context, store *Store) error { return runDemo(ctx, store, 8*time.Second) }
func runDemo(ctx context.Context, store *Store, interval time.Duration) error {
	if !store.Snapshot().Demo {
		return fmt.Errorf("demo requires an isolated demo store")
	}
	if err := store.Update(seedDemo); err != nil {
		return err
	}
	// The closed simulation owns its own fake Guide loop as well. No GitHub
	// client, worker runner or harness catalog enters this path.
	ctx, cancel := context.WithCancel(ctx)
	guideDone := make(chan struct{})
	guide := &Supervisor{Store: store, now: time.Now, fatal: make(chan error, 1)}
	go func() { defer close(guideDone); guide.runGuide(ctx) }()
	defer func() { cancel(); <-guideDone }()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	step := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-guide.fatal:
			return err
		case now := <-ticker.C:
			if err := store.Update(func(s *State) error {
				// A snooze ends on the demo's own clock, exactly as the live
				// scheduler ends it, so the resumption is visible in the demo.
				expireDeferrals(s, now)
				if demoStep(s, step, now) {
					step++
				}
				return nil
			}); err != nil {
				return err
			}
		}
	}
}

// seedDemo creates the two exercise precincts the first time a demo store
// starts. A store that already holds them is left as it is.
func seedDemo(s *State) error {
	if len(s.Towns) > 0 {
		return nil
	}
	for _, repo := range []string{"BrokkAi/orchard", "BrokkAi/paper-trail"} {
		c := DefaultConfig(repo)
		c.Branch = "main"
		// Demo never launches an agent, but the village only tells the truth
		// about profiles if the houses actually differ: a town default, one
		// house on another harness, and one that raises only the effort.
		c.Agent.Model, c.Agent.Effort = "gpt-5-codex", "medium"
		c.BotAgents = map[Role]BotAgentConfig{
			Review:  {Harness: "claude-acp", Agent: runner.AgentConfig{Model: "claude-opus-5", Effort: "high"}},
			Release: {Agent: runner.AgentConfig{Model: "gpt-5-codex", Effort: "xhigh"}},
		}
		c.Funnels = FunnelConfigs{{ID: "demo-github", Provider: "github", Location: SourceLocation{"repository": repo}, Enabled: true, PriorityPolicy: "demo-explicit", ReadOnly: true}, {ID: "demo-slack", Provider: "slack", Location: SourceLocation{"channel": "CDEMO"}, Enabled: true, PriorityPolicy: "demo-explicit", ReadOnly: true}}
		t, _ := s.Add(c)
		t.Initialized = true
		t.Head = strings.Repeat("a", 40)
		t.LastRelease = "v0.8.2"
		for _, w := range t.Workers {
			w.Enabled = true
			w.Status = "waiting"
			w.Task = "Standing by for the next call"
		}
		if repo == "BrokkAi/orchard" {
			t.Report("Shift briefing: orchard", "Training exercise. All units are on patrol. The Bug Detective and the Feature Detective have investigations open, and civilian reports are expected shortly. This is simulated activity.", time.Now())
			seedDemoBoard(s, t, time.Now())
			now := time.Now()
			id := WorkIdentity{Funnel: "demo-slack", Provider: "slack", Item: "1712345678.000100"}
			item := WorkItem{Identity: id, Title: "Investigate the customer import report", Body: "Synthetic Slack intake for demo mode.", Status: WorkQueued, Eligible: true, Eligibility: "matched demo channel marker", Priority: Priority{Value: 20, Policy: "demo-explicit", Reason: "operator-set example"}, Provenance: Provenance{Identity: id, URL: "https://app.slack.com/client/TDEMO/CDEMO/thread", Revision: "demo-r1", ObservedAt: now, ExternalState: "message"}, Capabilities: CapabilitySet{{Action: ActionClaim, State: CapabilityReadOnly, Reason: "demo funnel is read-only"}}}
			doneSlackID := WorkIdentity{Funnel: "demo-slack", Provider: "slack", Item: "1712345678.000200"}
			doneSlack := WorkItem{Identity: doneSlackID, Title: "Document the import workaround", Body: "Synthetic Slack item carrying a check-mark reaction.", Status: WorkComplete, Eligible: false, Eligibility: "done reaction observed at source", Priority: Priority{Policy: "demo-explicit"}, Provenance: Provenance{Identity: doneSlackID, URL: "https://app.slack.com/client/TDEMO/CDEMO/done", Revision: "demo-r2", ObservedAt: now, ExternalState: "message"}, Capabilities: CapabilitySet{{Action: ActionClaim, State: CapabilityReadOnly, Reason: "demo funnel is read-only"}}}
			_ = ReconcileFunnelPage(s, t, DiscoveryPage{Funnel: "demo-slack", Provider: "slack", Items: []WorkItem{item, doneSlack}, Complete: true, Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, ObservedAt: now}, now)
			doneGitHubID := WorkIdentity{Funnel: "demo-github", Provider: "github", Item: "706"}
			doneGitHub := WorkItem{Identity: doneGitHubID, Title: "Retire the legacy import endpoint", Status: WorkClosed, Eligible: false, Eligibility: "issue is closed at source", Priority: Priority{Policy: "demo-explicit"}, Provenance: Provenance{Identity: doneGitHubID, URL: "https://github.com/BrokkAi/orchard/issues/706", Revision: "demo-r3", ObservedAt: now, ExternalState: "closed"}, Capabilities: CapabilitySet{{Action: ActionClaim, State: CapabilityReadOnly, Reason: "demo funnel is read-only"}}}
			_ = ReconcileFunnelPage(s, t, DiscoveryPage{Funnel: "demo-github", Provider: "github", Items: []WorkItem{doneGitHub}, Complete: true, Outcome: Outcome{Kind: OutcomeComplete, Covered: true}, ObservedAt: now}, now)
		} else {
			t.Report("Paper-trail: Patrol is stuck", "Training exercise. The last inventory failed and waits for the captain to retry it. The other units hold their posts. This is simulated activity.", time.Now())
			t.Workers[Repo].Status = "failed"
			t.Workers[Repo].Error = "Exercise inventory failed and waits for the captain to retry it"
			t.Workers[Repo].Task = "Inspect Patrol's last report"
		}
	}
	return nil
}

// demoSlopTitle is the deliberately sloppy civilian report the Magistrate
// dismisses on every lap of the exercise.
const demoSlopTitle = "Rewrite the config loader as a plugin framework"

// demoStep plays one beat of the exercise on the orchard precinct. It reports
// false when the beat could not run (the precinct is gone, or the unit that
// owns this beat or every unit is stood down), so the same beat runs on the
// next tick. Case numbers cycle, so a long exercise reuses them.
func demoStep(s *State, step int, now time.Time) bool {
	t := s.Towns["brokkai/orchard"]
	if t == nil || t.Deleted {
		return false
	}
	role := []Role{Bug, Bug, Issue, Review, Issue, Review, Review, Release, Repo, Feature, Feature}[step%11]
	if !t.Workers[role].Enabled {
		return false
	}
	number := 142 + (step/11)%20
	active := false
	for _, w := range t.Workers {
		if w.Enabled {
			active = true
		}
		if w.Enabled {
			w.Status = "waiting"
			w.Phase = "idle"
			w.Task = "Standing by for the next call"
		}
		w.Agent = nil
	}
	if !active {
		return false
	}
	issueID := fmt.Sprintf("issue:%d", number)
	prID := fmt.Sprintf("pr:%d", number+100)
	slopID := fmt.Sprintf("issue:%d", number+600)
	switch step % 11 {
	case 0:
		w := t.Workers[Bug]
		p := t.Config.Public().BotAgents[Bug]
		w.Agent = &p
		w.Status = "working"
		w.Task = "Investigating a dropped event during reconnect"
		w.Phase = "investigating"
		s.Event(t.ID, "activity", "bug", "bug", "", "The Bug Detective is checking a reconnect race", now)
	case 1:
		task := &Task{ID: issueID, Kind: "issue", Number: number, Title: "Recover events after a dropped connection", Stage: "queued", House: Issue, Updated: now, Detail: "The reproducer drops the connection between receiving an event and saving its cursor."}
		t.Tasks[issueID] = task
		s.Event(t.ID, "delivery", "bug", "issue", issueID, "The Bug Detective filed a verified issue; case transferred to the Caseworker", now)
		t.Workers[Issue].Status = "working"
		p := t.Config.Public().BotAgents[Issue]
		t.Workers[Issue].Agent = &p
		t.Workers[Issue].Task = "Building a regression test and a focused fix"
	case 2:
		t.Tasks[issueID].Stage = "implemented"
		t.Tasks[prID] = &Task{ID: prID, Kind: "pr", Number: number + 100, Title: "Preserve the reconnect cursor", Stage: "queued", House: Review, Head: strings.Repeat("b", 40), Base: t.Head, Updated: now}
		s.Event(t.ID, "delivery", "issue", "review", prID, "New PR transferred to Forensics", now)
		t.Workers[Review].Status = "working"
		p := t.Config.Public().BotAgents[Review]
		t.Workers[Review].Agent = &p
		t.Workers[Review].Task = "Examining the evidence independently"
	case 3:
		task := t.Tasks[prID]
		task.Detail = "The reconnect path still skips one event when the queue is full."
		s.Move(t, task, "fixes", Issue, "Forensics sent one finding back to the Caseworker", now)
		t.Workers[Issue].Status = "working"
		p := t.Config.Public().BotAgents[Issue]
		t.Workers[Issue].Agent = &p
		t.Workers[Issue].Task = "Repairing the remaining queue-full case"
	case 4:
		task := t.Tasks[prID]
		task.Head = strings.Repeat("c", 40)
		task.Cycles++
		s.Move(t, task, "queued", Review, "Revised PR is back at Forensics", now)
		t.Workers[Review].Status = "working"
		p := t.Config.Public().BotAgents[Review]
		t.Workers[Review].Agent = &p
		t.Workers[Review].Task = "Verifying the fix and all previous findings"
	case 5:
		task := t.Tasks[prID]
		task.Audit = &Audit{Base: t.Head, Head: task.Head, Verdict: "clean", Complete: true, Summary: "The lost-event regression is fixed. Both reconnect paths and the queue-full case passed.", Checks: []string{"Reconnect regression tests passed"}, Findings: []Finding{}, At: now}
		s.Move(t, task, "ready", Review, "Review complete; waiting for required checks", now)
	case 6:
		task := t.Tasks[prID]
		s.Move(t, task, "merged", Release, "Merged change transferred to Release", now)
		t.Tasks["commit:"+task.Head] = &Task{ID: "commit:" + task.Head, Kind: "commit", Title: task.Title, Head: task.Head, House: Release, Stage: "unreleased", Updated: now}
		t.Workers[Release].Status = "working"
		p := t.Config.Public().BotAgents[Release]
		t.Workers[Release].Agent = &p
		t.Workers[Release].Task = "Processing the next release"
	case 7:
		t.LastRelease = fmt.Sprintf("v0.8.%d", 3+(number-142))
		for _, task := range t.Tasks {
			if task.Kind == "commit" {
				task.Stage = "shipped"
			}
		}
		s.Event(t.ID, "delivery", "release", "outside", "release:"+t.LastRelease, "Release "+t.LastRelease+" is out", now)
		t.Report("Case closed: the reconnect fix shipped", "The reconnect fix cleared Forensics and shipped. One case went from slop to reformed: investigation, rehab with the Caseworker, Forensics, merge and release.", now)
		s.Event(t.ID, "report", "repo", "hall", "", "Patrol filed the latest report", now)
		// The next beat files its slop under a number an earlier lap or an
		// earlier run may already hold. Patrol closes that dismissed issue
		// first, as the live closer does, so the reused case is arrested
		// again instead of sitting in the tank unchanged.
		if old := t.Tasks[slopID]; old != nil && old.Stage == "declined" {
			old.Stage = "closed"
			old.Detail = "The Magistrate sent this low-value complex issue to the Slop Tank. The Squad closed it."
			old.Updated = now
			s.Event(t.ID, "decision", string(Simplifier), string(Repo), slopID, "Magistrate closed: "+old.Title, now)
		}
	case 9:
		w := t.Workers[Feature]
		p := t.Config.Public().BotAgents[Feature]
		w.Agent = &p
		w.Status = "working"
		w.Phase = "investigating"
		w.Task = "Working leads on saved report workflows and comparing feature ideas"
		s.Event(t.ID, "activity", "feature", "feature", "", "The Feature Detective is working a lead on a useful new capability", now)
	case 10:
		id := fmt.Sprintf("issue:%d", number+900)
		t.Tasks[id] = &Task{ID: id, Kind: "issue", Number: number + 900, Title: "Save reusable report views", House: Hall, Stage: "awaiting_mayor", MayoralDecision: "pending", Updated: now, Detail: "Let operators save filters as named views. Acceptance: create, select, rename and delete views; restore the selected view after restart."}
		s.Event(t.ID, "delivery", "feature", "hall", id, "The Feature Detective filed a proposal with the Probation Judge", now)
		t.Workers[Feature].Task = "Proposal filed; standing by for the next lead"
	case 8:
		id := fmt.Sprintf("issue:%d", number+500)
		t.Tasks[id] = &Task{ID: id, Kind: "issue", Number: number + 500, Title: "Support a custom report schedule", House: Hall, Stage: "awaiting_mayor", MayoralDecision: "pending", External: true, Updated: now}
		s.Event(t.ID, "delivery", "outside", "hall", id, "Civilian report filed; awaiting a ruling", now)
		external := fmt.Sprintf("pr:%d", number+700)
		t.Tasks[external] = &Task{ID: external, Kind: "pr", Number: number + 700, Title: "Clarify setup instructions", House: Hall, Stage: "awaiting_mayor", MayoralDecision: "pending", External: true, Updated: now}
		s.Event(t.ID, "delivery", "outside", "hall", external, "Contributor PR filed; awaiting a ruling", now)
		// A sloppy civilian report the Magistrate arraigns in auto mode and
		// dismisses outright. It carries the state the live service records
		// for that dismissal, so the browser arrests it and books it into the
		// Slop Tank, and the court can still admit it anyway.
		slop := &Task{ID: slopID, Kind: "issue", Number: number + 600, Title: demoSlopTitle, House: Hall, Stage: "declined", External: true, Detail: "The Magistrate sent this case to the Slop Tank. The Squad is closing the issue.", Updated: now}
		slop.Simplification = &Simplification{Mode: "auto", Decision: "decline", Summary: "Disproportionate complexity for no reported problem.", Detail: "The report asks to replace a working config loader with a plugin framework, a plugin registry and a new extension API. It names no bug and no need the current loader fails, and the rewrite would touch every unit's startup path."}
		t.Tasks[slopID] = slop
		s.Event(t.ID, "delivery", "outside", "simplifier", slopID, "Civilian report booked for arraignment", now)
		s.Event(t.ID, "decision", string(Simplifier), "hall", slopID, "Magistrate sent a sloppy civilian report to the tank: "+demoSlopTitle, now)
	}
	for _, w := range t.Workers {
		w.Updated = now
		if !w.Enabled {
			w.Status = "paused"
		}
		if OccupiesAgentSlot(w.Role) && (w.Status == "working" || w.Status == "pausing") {
			p := t.Config.Public().BotAgents[w.Role]
			w.Agent = &p
		}
		if w.Status == "working" {
			w.Logs = append(w.Logs, Log{now, "INFO", w.Task})
			if len(w.Logs) > 30 {
				w.Logs = w.Logs[len(w.Logs)-30:]
			}
		}
	}
	t.LastSync = now
	return true
}

// seedDemoBoard gives a new demo an immediately inspectable operations board.
// These are durable-looking fixtures only: Demo never supplies GitHub or agent
// handles, and the normal scheduler is disabled for demo stores.
func seedDemoBoard(s *State, t *Town, now time.Time) {
	base := strings.Repeat("a", 40)
	sha := func(ch string) string { return strings.Repeat(ch, 40) }
	t.Tasks["issue:701"] = &Task{ID: "issue:701", Kind: "issue", Number: 701, Title: "Recover a dropped event cursor", Stage: "queued", House: Issue, Blocked: true, Detail: "Blocked on a reproducible race report.", Updated: now}
	t.Tasks["pr:702"] = &Task{ID: "pr:702", Kind: "pr", Number: 702, Title: "Clarify reconnect ownership", Stage: "inconclusive", House: Review, Head: sha("b"), Base: base, Audit: &Audit{Base: base, Head: sha("b"), Verdict: "inconclusive", Complete: false, Summary: "The review session could not establish complete coverage.", Checks: []string{"Review evidence is incomplete"}, Findings: []Finding{}}, Updated: now}
	t.Tasks["pr:703"] = &Task{ID: "pr:703", Kind: "pr", Number: 703, Title: "Persist the reconnect cursor", Stage: "queued", House: Review, Head: sha("c"), Base: base, Updated: now}
	t.Intents[703] = &Intent{Kind: "merge", PR: 703, Base: base, Head: sha("c"), Status: "uncertain", Detail: "Demo merge response was lost; reconcile before retrying.", At: now}
	t.Tasks["issue:704"] = &Task{ID: "issue:704", Kind: "issue", Number: 704, Title: "Add saved report views", Stage: "queued", House: Issue, Detail: "A queued case waiting on the Caseworker.", Updated: now}
	t.Tasks["pr:705"] = &Task{ID: "pr:705", Kind: "pr", Number: 705, Title: "Make reconnect tests deterministic", Stage: "ready", House: Review, Head: sha("d"), Base: base, Audit: &Audit{Base: base, Head: sha("d"), Verdict: "clean", Complete: true, Summary: "The full change passed the independent review.", Checks: []string{"Reconnect regression tests passed"}, Findings: []Finding{}}, Updated: now}
	t.Tasks["issue:707"] = &Task{ID: "issue:707", Kind: "issue", Number: 707, Title: "Adopt the vendor's new import API", Stage: "queued", House: Issue, Detail: "Snoozed by the captain; the rest of the Caseworker's caseload keeps moving.", DeferredUntil: now.Add(6 * time.Hour).UTC(), DeferReason: "Waiting for the vendor's API release", Updated: now}
	t.Tasks["commit:"+sha("e")] = &Task{ID: "commit:" + sha("e"), Kind: "commit", Title: "Ship the cursor recovery", Stage: "shipped", House: Release, Head: sha("e"), Updated: now}

	// Active profiles make dispatch provenance visible in the first frame. The
	// profile is public by design; private command and environment values never
	// enter a demo snapshot.
	profile := t.Config.Public().BotAgents[Bug]
	t.Workers[Bug].Agent = &profile
	t.Workers[Bug].Enabled = true
	t.Workers[Bug].Status = "working"
	t.Workers[Bug].Phase = "investigating"
	t.Workers[Bug].Task = "Investigating a dropped event during reconnect"
	t.Workers[Review].Agent = func() *PublicBotAgentConfig { p := t.Config.Public().BotAgents[Review]; return &p }()
	t.Workers[Review].Enabled = true
	t.Workers[Review].Status = "pausing"
	t.Workers[Review].Phase = "waiting"
	t.Workers[Review].Task = "Holding the Forensics slot for the captain to inspect"
	s.Event(t.ID, "delivery", "outside", "issue", "issue:704", "Queued case waiting on the Caseworker", now)
	s.Event(t.ID, "delivery", "review", "hall", "pr:702", "Inconclusive review is stuck", now)
	s.Event(t.ID, "delivery", "merge", "hall", "pr:703", "Uncertain merge awaits reconciliation", now)
	s.Event(t.ID, "delivery", "release", "outside", "release:v0.8.3", "Previous release v0.8.3 is out", now)
}

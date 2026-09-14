package town

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Demo is a closed simulation: it never receives a GitHub client or worker runner.
func Demo(ctx context.Context, store *Store) error { return runDemo(ctx, store, 8*time.Second) }
func runDemo(ctx context.Context, store *Store, interval time.Duration) error {
	if !store.Snapshot().Demo {
		return fmt.Errorf("demo requires an isolated demo store")
	}
	if err := store.Update(func(s *State) error {
		if len(s.Towns) > 0 {
			return nil
		}
		for _, repo := range []string{"BrokkAi/orchard", "BrokkAi/paper-trail"} {
			c := DefaultConfig(repo)
			c.Branch = "main"
			c.Funnels = FunnelConfigs{{ID: "demo-github", Provider: "github", Location: SourceLocation{"repository": repo}, Enabled: true, PriorityPolicy: "demo-explicit", ReadOnly: true}, {ID: "demo-slack", Provider: "slack", Location: SourceLocation{"channel": "CDEMO"}, Enabled: true, PriorityPolicy: "demo-explicit", ReadOnly: true}}
			t, _ := s.Add(c)
			t.Initialized = true
			t.Head = strings.Repeat("a", 40)
			t.LastRelease = "v0.8.2"
			for _, w := range t.Workers {
				w.Enabled = true
				w.Status = "waiting"
				w.Task = "Watching the village"
			}
			t.Report("A good morning in orchard", "The town is awake. Bug and feature investigations are underway, and a delivery of external work is expected shortly. This is simulated activity.", time.Now())
			if repo == "BrokkAi/orchard" {
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
				t.Workers[Repo].Status = "failed"
				t.Workers[Repo].Error = "Demo inventory is waiting for an operator retry"
				t.Workers[Repo].Task = "Inspect the watchtower report"
			}
		}
		return nil
	}); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	step := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if err := store.Update(func(s *State) error {
				t := s.Towns["brokkai/orchard"]
				if t == nil || t.Deleted {
					return nil
				}
				role := []Role{Bug, Bug, Issue, Review, Issue, Review, Review, Release, Repo, Feature, Feature}[step%11]
				if !t.Workers[role].Enabled {
					return nil
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
						w.Task = "Watching for the next delivery"
					}
					w.Agent = nil
				}
				if !active {
					return nil
				}
				issueID := fmt.Sprintf("issue:%d", number)
				prID := fmt.Sprintf("pr:%d", number+100)
				switch step % 11 {
				case 0:
					w := t.Workers[Bug]
					p := t.Config.Public().BotAgents[Bug]
					w.Agent = &p
					w.Status = "working"
					w.Task = "Investigating a dropped event during reconnect"
					w.Phase = "investigating"
					s.Event(t.ID, "activity", "bug", "bug", "", "Bug-bot is checking a reconnect race", now)
				case 1:
					task := &Task{ID: issueID, Kind: "issue", Number: number, Title: "Recover events after a dropped connection", Stage: "queued", House: Issue, Updated: now, Detail: "The reproducer drops the connection between receiving an event and saving its cursor."}
					t.Tasks[issueID] = task
					s.Event(t.ID, "delivery", "bug", "issue", issueID, "Bug-bot filed a verified issue", now)
					t.Workers[Issue].Status = "working"
					p := t.Config.Public().BotAgents[Issue]
					t.Workers[Issue].Agent = &p
					t.Workers[Issue].Task = "Building a regression test and a focused fix"
				case 2:
					t.Tasks[issueID].Stage = "implemented"
					t.Tasks[prID] = &Task{ID: prID, Kind: "pr", Number: number + 100, Title: "Preserve the reconnect cursor", Stage: "queued", House: Review, Head: strings.Repeat("b", 40), Base: t.Head, Updated: now}
					s.Event(t.ID, "delivery", "issue", "review", prID, "A new PR arrived at the observatory", now)
					t.Workers[Review].Status = "working"
					p := t.Config.Public().BotAgents[Review]
					t.Workers[Review].Agent = &p
					t.Workers[Review].Task = "Independently checking the change"
				case 3:
					task := t.Tasks[prID]
					task.Detail = "The reconnect path still skips one event when the queue is full."
					s.Move(t, task, "fixes", Issue, "Review-bot returned one finding", now)
					t.Workers[Issue].Status = "working"
					p := t.Config.Public().BotAgents[Issue]
					t.Workers[Issue].Agent = &p
					t.Workers[Issue].Task = "Repairing the remaining queue-full case"
				case 4:
					task := t.Tasks[prID]
					task.Head = strings.Repeat("c", 40)
					task.Cycles++
					s.Move(t, task, "queued", Review, "The revised PR is back for review", now)
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
					s.Move(t, task, "merged", Release, "The merged change reached the release depot", now)
					t.Tasks["commit:"+task.Head] = &Task{ID: "commit:" + task.Head, Kind: "commit", Title: task.Title, Head: task.Head, House: Release, Stage: "unreleased", Updated: now}
					t.Workers[Release].Status = "working"
					p := t.Config.Public().BotAgents[Release]
					t.Workers[Release].Agent = &p
					t.Workers[Release].Task = "Preparing the next shipment"
				case 7:
					t.LastRelease = fmt.Sprintf("v0.8.%d", 3+(number-142))
					for _, task := range t.Tasks {
						if task.Kind == "commit" {
							task.Stage = "shipped"
						}
					}
					s.Event(t.ID, "delivery", "release", "outside", "release:"+t.LastRelease, "Shipment "+t.LastRelease+" left town", now)
					t.Report("A shipment is on its way", "The reconnect fix passed its review loop and shipped. One issue moved from discovery through implementation, verification, merge, and release.", now)
					s.Event(t.ID, "report", "repo", "hall", "", "Repo-bot brought the latest town report", now)
				case 9:
					w := t.Workers[Feature]
					p := t.Config.Public().BotAgents[Feature]
					w.Agent = &p
					w.Status = "working"
					w.Phase = "investigating"
					w.Task = "Studying saved report workflows and comparing feature ideas"
					s.Event(t.ID, "activity", "feature", "feature", "", "Feature-bot is researching a useful new capability", now)
				case 10:
					id := fmt.Sprintf("issue:%d", number+900)
					t.Tasks[id] = &Task{ID: id, Kind: "issue", Number: number + 900, Title: "Save reusable report views", House: Hall, Stage: "awaiting_mayor", MayoralDecision: "pending", Updated: now, Detail: "Let operators save filters as named views. Acceptance: create, select, rename and delete views; restore the selected view after restart."}
					s.Event(t.ID, "delivery", "feature", "hall", id, "Feature-bot brought a proposal to the Mayor", now)
					t.Workers[Feature].Task = "Proposal filed; waiting for the next research session"
				case 8:
					id := fmt.Sprintf("issue:%d", number+500)
					t.Tasks[id] = &Task{ID: id, Kind: "issue", Number: number + 500, Title: "Support a custom report schedule", House: Hall, Stage: "awaiting_mayor", MayoralDecision: "pending", External: true, Updated: now}
					s.Event(t.ID, "delivery", "outside", "hall", id, "An external issue arrived for a Mayoral decision", now)
					external := fmt.Sprintf("pr:%d", number+700)
					t.Tasks[external] = &Task{ID: external, Kind: "pr", Number: number + 700, Title: "Clarify setup instructions", House: Hall, Stage: "awaiting_mayor", MayoralDecision: "pending", External: true, Updated: now}
					s.Event(t.ID, "delivery", "outside", "hall", external, "A contributor PR arrived for a Mayoral decision", now)
				}
				for _, w := range t.Workers {
					w.Updated = now
					if !w.Enabled {
						w.Status = "paused"
					}
					if ValidAgentRole(w.Role) && (w.Status == "working" || w.Status == "pausing") {
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
				step++
				return nil
			}); err != nil {
				return err
			}
		}
	}
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
	t.Tasks["issue:704"] = &Task{ID: "issue:704", Kind: "issue", Number: 704, Title: "Add saved report views", Stage: "queued", House: Issue, Detail: "A queued issue waiting for the workshop.", Updated: now}
	t.Tasks["pr:705"] = &Task{ID: "pr:705", Kind: "pr", Number: 705, Title: "Make reconnect tests deterministic", Stage: "ready", House: Review, Head: sha("d"), Base: base, Audit: &Audit{Base: base, Head: sha("d"), Verdict: "clean", Complete: true, Summary: "The full change passed the independent review.", Checks: []string{"Reconnect regression tests passed"}, Findings: []Finding{}}, Updated: now}
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
	t.Workers[Review].Task = "Holding the review slot for operator inspection"
	s.Event(t.ID, "delivery", "outside", "issue", "issue:704", "A queued issue is waiting in the workshop", now)
	s.Event(t.ID, "delivery", "review", "hall", "pr:702", "An inconclusive review needs attention", now)
	s.Event(t.ID, "delivery", "merge", "hall", "pr:703", "An uncertain merge is awaiting reconciliation", now)
	s.Event(t.ID, "delivery", "release", "outside", "release:v0.8.3", "A previous shipment left town", now)
}

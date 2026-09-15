package town

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Bot upgrades are offered, never floated. A published stable bot version that is
// newer than a town's pin becomes a Mayoral decision in Town Hall unless the
// town has opted into automatic updates. The pin changes only when the Mayor
// approves the upgrade, the delay elapses and the Mayor then approves it, or the
// town auto-updates; a declined version is never offered again.
const (
	BotUpgradeDelay           = 24 * time.Hour
	DefaultBotVersionInterval = 6 * time.Hour
)

var botDisplayNames = map[Role]string{Bug: "Bug Bot", Feature: "Feature Bot", Issue: "Issue Bot", Review: "Review Bot", Release: "Release Bot"}

func botDisplayName(role Role) string {
	if name, ok := botDisplayNames[role]; ok {
		return name
	}
	return string(role)
}

func upgradeTaskID(role Role) string { return "upgrade:" + string(role) }

func upgradeTitle(u *BotUpgrade) string {
	return fmt.Sprintf("%s %s is available (pinned %s)", botDisplayName(u.Role), u.To, u.From)
}

// OfferBotUpgrades compares npm's stable versions with every live town's pins.
// Towns that auto-update take the newer pin immediately; other towns receive or
// refresh a pending Mayoral decision. Offers that are no longer newer than the
// pin are withdrawn.
func (s *State) OfferBotUpgrades(versions map[Role]string, now time.Time) {
	for _, t := range s.Towns {
		if t.Deleted {
			continue
		}
		for _, role := range AgentRoles {
			latest := versions[role]
			if latest == "" || !workerVersionPattern.MatchString(latest) {
				continue
			}
			s.offerBotUpgrade(t, role, latest, now)
		}
	}
}

func (s *State) offerBotUpgrade(t *Town, role Role, latest string, now time.Time) {
	id := upgradeTaskID(role)
	current := t.Config.BotVersion(role)
	task := t.Tasks[id]
	if !NewerVersion(current, latest) {
		delete(t.Tasks, id)
		return
	}
	if t.Config.AutoUpdateBots {
		s.applyBotUpgrade(t, &BotUpgrade{Role: role, From: current, To: latest}, "auto-update", now)
		return
	}
	if task != nil && task.Upgrade != nil && task.Upgrade.To == latest {
		task.Upgrade.From = current
		if task.Stage == "delayed" && !now.Before(task.RetryAt) {
			s.pendBotUpgrade(t, task, "The delay has passed; this upgrade is waiting for your decision again.", now)
		}
		return
	}
	task = &Task{ID: id, Kind: "upgrade", Title: "", House: Hall, Upgrade: &BotUpgrade{Role: role, From: current, To: latest}}
	t.Tasks[id] = task
	s.pendBotUpgrade(t, task, "", now)
}

func (s *State) pendBotUpgrade(t *Town, task *Task, note string, now time.Time) {
	task.Title = upgradeTitle(task.Upgrade)
	task.Stage = "awaiting_mayor"
	task.MayoralDecision = "pending"
	task.RetryAt = time.Time{}
	task.Detail = fmt.Sprintf("npm published %s %s as the latest stable release; this town runs %s. Approve to pin the new version for the bot's next run, delay to be asked again in a day, or decline to keep %s until a newer release appears.", botDisplayName(task.Upgrade.Role), task.Upgrade.To, task.Upgrade.From, task.Upgrade.From)
	if note != "" {
		task.Detail = note + " " + task.Detail
	}
	task.Updated = now
	s.Event(t.ID, "delivery", "outside", "hall", task.ID, "Bot update needs a Mayoral decision: "+task.Title, now)
}

// applyBotUpgrade pins the newer version and withdraws the offer. The running
// worker is not interrupted; the pin takes effect on the bot's next dispatch.
func (s *State) applyBotUpgrade(t *Town, u *BotUpgrade, by string, now time.Time) {
	if t.Config.BotVersions == nil {
		t.Config.BotVersions = map[Role]string{}
	}
	t.Config.BotVersions[u.Role] = u.To
	delete(t.Tasks, upgradeTaskID(u.Role))
	title := fmt.Sprintf("Upgraded %s from %s to %s for its next run", botDisplayName(u.Role), u.From, u.To)
	if by == "auto-update" {
		title = fmt.Sprintf("Auto-updated %s from %s to %s for its next run", botDisplayName(u.Role), u.From, u.To)
	}
	s.Event(t.ID, "upgrade", "hall", string(u.Role), upgradeTaskID(u.Role), title, now)
}

// decideBotUpgrade resolves one upgrade decision: admit applies the pin, decline
// keeps the current pin until a newer version is published, and delay asks
// again after BotUpgradeDelay.
func (s *State) decideBotUpgrade(t *Town, task *Task, action string, now time.Time) error {
	if task.Kind != "upgrade" || task.Upgrade == nil {
		return errors.New("delay applies to bot upgrade decisions")
	}
	if task.Stage != "awaiting_mayor" || task.MayoralDecision != "pending" {
		return errors.New("bot upgrade is not awaiting a Mayoral decision")
	}
	u := task.Upgrade
	switch action {
	case "admit":
		s.applyBotUpgrade(t, u, "mayor", now)
		s.Event(t.ID, "decision", "hall", string(u.Role), task.ID, "Mayor approved: "+task.Title, now)
	case "decline":
		task.MayoralDecision = "declined"
		task.Stage = "declined"
		task.Detail = fmt.Sprintf("The Mayor declined %s %s. Town keeps %s until a newer stable release is published.", botDisplayName(u.Role), u.To, u.From)
		task.Updated = now
		s.Event(t.ID, "decision", "hall", "outside", task.ID, "Mayor declined: "+task.Title, now)
	case "delay":
		task.MayoralDecision = ""
		task.Stage = "delayed"
		task.RetryAt = now.Add(BotUpgradeDelay)
		task.Detail = fmt.Sprintf("The Mayor delayed %s %s. Town keeps %s and will ask again after %s.", botDisplayName(u.Role), u.To, u.From, task.RetryAt.Format(time.RFC1123))
		task.Updated = now
		s.Event(t.ID, "decision", "hall", "hall", task.ID, "Mayor delayed a day: "+task.Title, now)
	default:
		return errors.New("unknown action")
	}
	return nil
}

// reviveDelayedBotUpgrades returns delayed offers whose day has passed to Town
// Hall. It reports whether anything changed so callers can skip a store write.
func (s *State) reviveDelayedBotUpgrades(now time.Time) bool {
	changed := false
	for _, t := range s.Towns {
		if t.Deleted {
			continue
		}
		for _, task := range t.Tasks {
			if task.Kind == "upgrade" && task.Stage == "delayed" && !now.Before(task.RetryAt) {
				s.pendBotUpgrade(t, task, "The delay has passed; this upgrade is waiting for your decision again.", now)
				changed = true
			}
		}
	}
	return changed
}

// applyOfferedBotUpgrades takes every outstanding offer for a town that has just
// opted into automatic updates, so the setting acts now rather than at the next
// registry check.
func (s *State) applyOfferedBotUpgrades(t *Town, now time.Time) {
	for _, role := range AgentRoles {
		task := t.Tasks[upgradeTaskID(role)]
		if task == nil || task.Upgrade == nil {
			continue
		}
		if NewerVersion(t.Config.BotVersion(role), task.Upgrade.To) {
			s.applyBotUpgrade(t, task.Upgrade, "auto-update", now)
		} else {
			delete(t.Tasks, task.ID)
		}
	}
}

// checkBotUpgrades reads the registry once and records the outcome. Registry
// failures are quiet: an unreachable npm must never stop a local town.
func (s *Supervisor) checkBotUpgrades(ctx context.Context) {
	if s.BotVersions == nil {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	versions, err := s.BotVersions(checkCtx)
	if err != nil || len(versions) == 0 || ctx.Err() != nil {
		return
	}
	s.update(func(st *State) error {
		if st.Demo {
			return nil
		}
		st.OfferBotUpgrades(versions, s.now())
		return nil
	})
}

func (s *Supervisor) watchBotVersions(ctx context.Context) {
	interval := s.BotVersionInterval
	if interval <= 0 {
		interval = DefaultBotVersionInterval
	}
	s.checkBotUpgrades(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkBotUpgrades(ctx)
		}
	}
}

func (s *Supervisor) reviveDelayedBotUpgrades() {
	now := s.now()
	due := false
	for _, t := range s.Store.Snapshot().Towns {
		for _, task := range t.Tasks {
			if !t.Deleted && task.Kind == "upgrade" && task.Stage == "delayed" && !now.Before(task.RetryAt) {
				due = true
			}
		}
	}
	if !due {
		return
	}
	s.update(func(st *State) error {
		st.reviveDelayedBotUpgrades(s.now())
		return nil
	})
}

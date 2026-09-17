package town

import "strings"

// A profile only reads at a glance when it is short, so these labels drop the
// registry decoration (vendor prefix, -acp suffix, provider path) and keep the
// part an operator actually chose. The browser applies the same rules.
var harnessNames = map[string]string{
	"codex-acp":        "codex",
	"claude-acp":       "claude",
	"brokkai/anvil":    "anvil",
	"brokkai/muse-acp": "muse",
	"foundev/draupnir": "draupnir",
}

func HarnessLabel(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return "codex"
	}
	if short, ok := harnessNames[id]; ok {
		return short
	}
	trimmed := strings.TrimRight(id, "/")
	short := strings.TrimSuffix(trimmed[strings.LastIndex(trimmed, "/")+1:], "-acp")
	if short == "" {
		return id
	}
	return short
}

func ModelLabel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "default"
	}
	if short := model[strings.LastIndex(model, "/")+1:]; short != "" {
		return short
	}
	return "default"
}

func EffortLabel(effort string) string {
	if effort = strings.TrimSpace(effort); effort != "" {
		return strings.ReplaceAll(effort, "_", " ")
	}
	return "default"
}

// Label is the one-line profile shown wherever a house is listed.
func (p PublicBotAgentConfig) Label() string {
	return HarnessLabel(p.Harness) + " · " + ModelLabel(p.Model) + " · " + EffortLabel(p.Effort)
}

// WorkerProfile reports the profile a house is running now, or the one its next
// dispatch will use. A running worker owns the profile captured at dispatch; an
// idle one reflects the current configuration, and therefore future work.
func WorkerProfile(t *Town, role Role) (profile PublicBotAgentConfig, live bool) {
	if t == nil {
		return PublicBotAgentConfig{}, false
	}
	if w := t.Workers[role]; w != nil && w.Agent != nil {
		return *w.Agent, true
	}
	return t.Config.Public().BotAgents[role], false
}

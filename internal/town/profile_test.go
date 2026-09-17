package town

import "testing"

func TestProfileLabelsTolerateTrailingSlashes(t *testing.T) {
	if got := ModelLabel("acme/"); got != "default" {
		t.Fatalf("ModelLabel(%q) = %q, want %q", "acme/", got, "default")
	}
	if got := HarnessLabel("acme/custom/"); got != "custom" {
		t.Fatalf("HarnessLabel(%q) = %q, want %q", "acme/custom/", got, "custom")
	}
	if got := (PublicBotAgentConfig{Harness: "codex-acp", Model: "acme/"}).Label(); got != "codex · default · default" {
		t.Fatalf("Label with trailing-slash model = %q, want %q", got, "codex · default · default")
	}
}

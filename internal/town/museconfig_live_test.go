package town

import (
	"context"
	"os/exec"
	"testing"
)

// End-to-end against the operator's real Muse install: the adapter must open a
// discovery session even when the installed settings name a profile whose
// reviewer `muse serve` cannot reach.
func TestProbeMuseLive(t *testing.T) {
	if testing.Short() {
		t.Skip("live harness probe")
	}
	if _, err := exec.LookPath("muse-acp"); err != nil {
		t.Skip("muse-acp is not installed")
	}
	choices, err := ProbeAgent(context.Background(), Config{Harness: "muse-acp"}, t.TempDir())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if len(choices.Models) == 0 {
		t.Fatal("no models came back")
	}
	t.Logf("models=%d efforts=%d first=%s", len(choices.Models), len(choices.Efforts), choices.Models[0].Value)
}

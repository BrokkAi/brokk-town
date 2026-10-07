package town

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// End-to-end against the operator's real Muse install: the adapter must open a
// discovery session even when the installed settings name a profile whose
// reviewer `muse serve` cannot reach.
func TestProbeMuseLive(t *testing.T) {
	if testing.Short() || os.Getenv("SLOPCOP_SQUAD_LIVE_TESTS") != "1" {
		t.Skip("live harness probe requires explicit SLOPCOP_SQUAD_LIVE_TESTS=1")
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx is not installed")
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

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// An install from before the rename to SlopCop Squad keeps its brokk-town state
// directory until the operator moves it; a fresh or moved install uses
// slopcop-squad.
func TestStateHomeKeepsPreRenameState(t *testing.T) {
	for _, test := range []struct {
		name   string
		exists []string
		want   string
	}{
		{name: "fresh install", want: "slopcop-squad"},
		{name: "pre-rename install", exists: []string{"brokk-town"}, want: "brokk-town"},
		{name: "moved install", exists: []string{"slopcop-squad"}, want: "slopcop-squad"},
		{name: "both present", exists: []string{"brokk-town", "slopcop-squad"}, want: "slopcop-squad"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("XDG_STATE_HOME", base)
			for _, dir := range test.exists {
				if err := os.Mkdir(filepath.Join(base, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if got, want := stateHome(), filepath.Join(base, test.want); got != want {
				t.Fatalf("stateHome() = %q, want %q", got, want)
			}
		})
	}
}

// A file that happens to carry the old name is not a state directory.
func TestStateHomeIgnoresPreRenameFile(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	if err := os.WriteFile(filepath.Join(base, "brokk-town"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got, want := stateHome(), filepath.Join(base, "slopcop-squad"); got != want {
		t.Fatalf("stateHome() = %q, want %q", got, want)
	}
}

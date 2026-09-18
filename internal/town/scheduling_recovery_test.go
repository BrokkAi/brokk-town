package town

import (
	"testing"
	"time"
)

func TestRestartShortensOnlySuccessfulSimplifierIntakeDelay(t *testing.T) {
	for _, scenario := range []string{"queued", "paused", "failed", "blocked", "retry", "empty", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			x := addTown(t, s)
			at := time.Now().Add(-time.Minute)
			next := at.Add(30 * time.Minute)
			update(t, s, func(st *State) {
				town := st.Towns[x.ID]
				w := town.Workers[Simplifier]
				w.Enabled, w.Status, w.Updated, w.Next = true, "waiting", at, next
				task := &Task{ID: "issue:7", Kind: "issue", Number: 7, House: Simplifier, Stage: "simplifying"}
				town.Tasks[task.ID] = task
				switch scenario {
				case "paused":
					w.Enabled = false
				case "failed":
					w.Status, w.Error = "failed", "assessment failed"
				case "blocked":
					task.Blocked = true
				case "retry":
					task.RetryAt = time.Now().Add(time.Hour)
				case "empty":
					delete(town.Tasks, task.ID)
				case "deleted":
					town.Deleted = true
				}
			})
			s.Close()
			reopened, err := Open(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			got := reopened.Snapshot().Towns[x.ID]
			want := next
			if scenario == "queued" {
				want = at.Add(time.Duration(got.Config.PollSeconds) * time.Second)
			}
			if !got.Workers[Simplifier].Next.Equal(want) {
				t.Fatalf("next = %v, want %v", got.Workers[Simplifier].Next, want)
			}
		})
	}
}

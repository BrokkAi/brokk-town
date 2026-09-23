package bugbot

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clock lets daemon polls pass NextScan and retry delays without sleeping.
type clock struct{ now time.Time }

func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

func changeFixture(t *testing.T, only bool) (engine, *State, *fakeSource, *fakeAgent, string, *clock) {
	t.Helper()
	e, s, f, a, source := fixture(t)
	e.config.OnlyOnChange = only
	c := &clock{now: time.Now()}
	e.now = func() time.Time { return c.now }
	return e, s, f, a, source, c
}

func scanWorktrees(t *testing.T, cfg Config) int {
	t.Helper()
	entries, err := os.ReadDir(cfg.Directory + "-scans")
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// poll runs one daemon step after the completed scan's poll interval.
func poll(e engine, s *State, c *clock) error {
	c.advance(time.Duration(e.config.Poll) + time.Second)
	return e.step(context.Background(), s, false)
}

func advanceBranch(t *testing.T, source string) {
	t.Helper()
	localGit(t, source, "commit", "--allow-empty", "-m", "advance")
	localGit(t, source, "push", "origin", "HEAD:main")
}

func TestUnchangedCommitRescannedByDefault(t *testing.T) {
	e, s, _, a, _, c := changeFixture(t, false)
	for i := 1; i <= 2; i++ {
		if err := poll(e, s, c); err != nil {
			t.Fatal(err)
		}
		if a.scans != i || len(s.History) != i || scanWorktrees(t, e.config) != i {
			t.Fatalf("poll %d: scans %d, history %d", i, a.scans, len(s.History))
		}
	}
}

func TestOnlyOnChangeSkipsCompletedRevision(t *testing.T) {
	e, s, f, a, source, c := changeFixture(t, true)
	a.findings = []Finding{} // Zero findings still completes the revision.
	if err := poll(e, s, c); err != nil {
		t.Fatal(err)
	}
	head, err := (checkout{e.config}).head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.scans != 1 || s.LastCompleted == nil || *s.LastCompleted != (LastCompleted{Commit: head}) {
		t.Fatalf("zero-finding scan did not establish a baseline: %+v", s.LastCompleted)
	}
	check := func(s *State) {
		t.Helper()
		if err := poll(e, s, c); !errors.Is(err, errUnchanged) {
			t.Fatalf("expected unchanged poll, got %v", err)
		}
		if s.Scan != nil || a.scans != 1 || a.reviews != 0 || f.reads != 1 || len(s.History) != 1 || scanWorktrees(t, e.config) != 1 {
			t.Fatalf("unchanged poll investigated: scans %d, reads %d, history %d", a.scans, f.reads, len(s.History))
		}
	}
	check(s)
	// Restart from saved state.
	saved, err := ReadState(e.config)
	if err != nil {
		t.Fatal(err)
	}
	check(saved)
	check(saved)

	advanceBranch(t, source)
	if err := poll(e, saved, c); err != nil {
		t.Fatal(err)
	}
	next, err := (checkout{e.config}).head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.scans != 2 || len(saved.History) != 2 || saved.LastCompleted.Commit != next || next == head {
		t.Fatalf("changed branch was not scanned: %+v", saved.LastCompleted)
	}
	if err := poll(e, saved, c); !errors.Is(err, errUnchanged) || a.scans != 2 {
		t.Fatalf("new baseline not used: %v", err)
	}
}

func TestOnceIgnoresOnlyOnChange(t *testing.T) {
	e, s, _, a, _, c := changeFixture(t, true)
	if err := poll(e, s, c); err != nil {
		t.Fatal(err)
	}
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if a.scans != 2 || len(s.History) != 2 {
		t.Fatalf("once did not rescan unchanged revision: scans %d", a.scans)
	}
}

func TestOnlyOnChangeSeparatesDryRun(t *testing.T) {
	e, s, f, a, _, c := changeFixture(t, true)
	e.config.DryRun = true
	if err := poll(e, s, c); err != nil {
		t.Fatal(err)
	}
	if !s.LastCompleted.DryRun || f.creates != 0 {
		t.Fatalf("dry run marker: %+v", s.LastCompleted)
	}
	if err := poll(e, s, c); !errors.Is(err, errUnchanged) {
		t.Fatalf("dry run rescanned: %v", err)
	}
	e.config.DryRun = false
	if err := poll(e, s, c); err != nil {
		t.Fatal(err)
	}
	if a.scans != 2 || f.creates != 1 || s.LastCompleted.DryRun {
		t.Fatalf("real publication did not scan the revision: scans %d, creates %d", a.scans, f.creates)
	}
	if err := poll(e, s, c); !errors.Is(err, errUnchanged) {
		t.Fatalf("real publication rescanned: %v", err)
	}
}

// A scan resumed in real mode after dry-run findings did not publish them.
func TestDryRunFindingsKeepDryRunMarker(t *testing.T) {
	e, s, _, _, _, c := changeFixture(t, true)
	a := &fakeAgent{findings: []Finding{finding()}}
	e.agent = func(Config, string) Agent { return a }
	e.config.DryRun = true
	if err := e.step(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	// Simulate a scan whose dry-run finding completes under real configuration.
	e.config.DryRun = false
	s.Scan = &Scan{Commit: s.LastCompleted.Commit, Directory: filepath.Join(e.config.Directory+"-scans", "scan-x"), Discovered: true, Candidates: []*Candidate{{RequestID: strings.Repeat("a", 32), Finding: finding(), Status: "dry_run"}}}
	if err := e.finish(s); err != nil {
		t.Fatal(err)
	}
	if !s.LastCompleted.DryRun {
		t.Fatal("unpublished dry-run finding recorded as real completion")
	}
	if err := poll(e, s, c); err != nil || a.scans != 2 {
		t.Fatalf("real mode did not rescan: %v", err)
	}
}

func TestOnlyOnChangeKeepsRetriesAndReconciliation(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		e, s, f, a, _, c := changeFixture(t, true)
		a.findings = []Finding{}
		if err := poll(e, s, c); err != nil {
			t.Fatal(err)
		}
		// An explicit once scan of the same revision fails before publication.
		a.findings = nil
		f.createErr = &rejectedCreateError{errors.New("confirmed rejection")}
		if err := e.step(context.Background(), s, true); err == nil {
			t.Fatal("expected rejection")
		}
		if s.Scan == nil || s.Scan.Commit != s.LastCompleted.Commit {
			t.Fatal("expected pending scan at the completed revision")
		}
		f.createErr = nil
		c.now = s.Scan.RetryAt.Add(time.Second)
		if err := e.step(context.Background(), s, false); err != nil {
			t.Fatal(err)
		}
		if s.Scan != nil || f.creates != 2 || s.Completed[0].Status != "submitted" {
			t.Fatalf("pending retry was gated: creates %d", f.creates)
		}
	})
	t.Run("reconcile", func(t *testing.T) {
		e, s, f, a, _, c := changeFixture(t, true)
		a.findings = []Finding{}
		if err := poll(e, s, c); err != nil {
			t.Fatal(err)
		}
		a.findings = nil
		f.lost = true
		if err := e.step(context.Background(), s, true); err == nil {
			t.Fatal("expected lost response")
		}
		f.lost = false
		if err := poll(e, s, c); err != nil {
			t.Fatal(err)
		}
		if s.Scan != nil || f.creates != 1 || s.Completed[0].Status != "submitted" || len(s.History) != 2 {
			t.Fatal("unknown publication was not reconciled before gating")
		}
	})
	t.Run("exhausted", func(t *testing.T) {
		e, s, f, a, _, c := changeFixture(t, true)
		e.config.Attempts = 1
		a.findings = []Finding{}
		if err := poll(e, s, c); err != nil {
			t.Fatal(err)
		}
		a.findings = nil
		f.createErr = &rejectedCreateError{errors.New("confirmed rejection")}
		if err := e.step(context.Background(), s, true); err == nil {
			t.Fatal("expected rejection")
		}
		err := poll(e, s, c)
		if err == nil || !strings.Contains(err.Error(), "scan attempt budget exhausted") {
			t.Fatalf("exhausted scan hidden by gate: %v", err)
		}
	})
}

func TestDiscardedScanDoesNotCompleteRevision(t *testing.T) {
	e, s, f, _, source, c := changeFixture(t, true)
	f.createErr = &rejectedCreateError{errors.New("confirmed rejection")}
	if err := poll(e, s, c); err == nil {
		t.Fatal("expected rejection")
	}
	advanceBranch(t, source)
	// The old scan is discarded as stale and the new revision also fails.
	c.now = s.Scan.RetryAt.Add(time.Second)
	if err := e.step(context.Background(), s, false); err == nil {
		t.Fatal("expected rejection")
	}
	if s.LastCompleted != nil || s.Completed[0].Status != "stale" {
		t.Fatalf("stale scan recorded as completed: %+v", s.LastCompleted)
	}
}

func TestLegacyStateScansOnce(t *testing.T) {
	e, s, _, a, _, c := changeFixture(t, true)
	if err := poll(e, s, c); err != nil {
		t.Fatal(err)
	}
	s.LastCompleted = nil
	if err := e.save(s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(e.config.StateDirectory, "state.json"))
	if err != nil || strings.Contains(string(data), "last_completed") {
		t.Fatalf("legacy state fixture: %v", err)
	}
	saved, err := ReadState(e.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := poll(e, saved, c); err != nil || a.scans != 2 {
		t.Fatalf("legacy state did not scan: %v", err)
	}
	if err := poll(e, saved, c); !errors.Is(err, errUnchanged) || a.scans != 2 {
		t.Fatalf("legacy scan did not establish baseline: %v", err)
	}
}

func TestMalformedCompletedCommitRejected(t *testing.T) {
	e, s, _, _, _, _ := changeFixture(t, true)
	for _, commit := range []string{"", "abc", strings.Repeat("A", 40), strings.Repeat("g", 40), strings.Repeat("a", 41)} {
		s.LastCompleted = &LastCompleted{Commit: commit}
		if err := writeState(e.config, s); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadState(e.config); err == nil || !strings.Contains(err.Error(), "completed commit") {
			t.Fatalf("%q accepted: %v", commit, err)
		}
	}
	s.LastCompleted = &LastCompleted{Commit: strings.Repeat("a", 64), DryRun: true}
	if err := writeState(e.config, s); err != nil {
		t.Fatal(err)
	}
	if saved, err := ReadState(e.config); err != nil || *saved.LastCompleted != *s.LastCompleted {
		t.Fatalf("valid marker rejected: %v", err)
	}
}

func TestUnchangedProgressWaitsWithoutCompleting(t *testing.T) {
	e, s, _, _, _, c := changeFixture(t, true)
	if err := poll(e, s, c); err != nil {
		t.Fatal(err)
	}
	var phases []string
	e.observe = func(p Progress) {
		if p.Phase != "" {
			phases = append(phases, p.Phase)
		}
	}
	if err := poll(e, s, c); !errors.Is(err, errUnchanged) {
		t.Fatal(err)
	}
	for _, phase := range phases {
		if phase == "complete" || phase == "preparing" || phase == "investigating" {
			t.Fatalf("unchanged poll reported %s", phase)
		}
	}
}

// levels records the level of every log record.
type levels struct{ seen *[]slog.Level }

func (h levels) Enabled(context.Context, slog.Level) bool { return true }
func (h levels) Handle(_ context.Context, r slog.Record) error {
	*h.seen = append(*h.seen, r.Level)
	return nil
}
func (h levels) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h levels) WithGroup(string) slog.Handler      { return h }

func TestDaemonLoopWaitsOnUnchangedPoll(t *testing.T) {
	e, s, f, a, _, c := changeFixture(t, true)
	if err := poll(e, s, c); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Duration(e.config.Poll) + time.Second)
	reads := f.reads
	var seen []slog.Level
	e.log = slog.New(levels{&seen})
	var last Progress
	var phases []string
	e.observe = func(p Progress) {
		if p.Phase != "" {
			phases = append(phases, p.Phase)
			last = p
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waits []time.Duration
	e.sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		cancel()
		return ctx.Err()
	}
	if err := e.loop(ctx, s, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("loop: %v", err)
	}
	if len(waits) != 1 || waits[0] != time.Duration(e.config.Poll) {
		t.Fatalf("loop did not wait one poll interval: %v", waits)
	}
	if last.Phase != "waiting" || last.Task != "Branch unchanged since the last completed scan" || !last.WakeAt.Equal(c.now.Add(time.Duration(e.config.Poll))) {
		t.Fatalf("unchanged poll reported %+v (phases %v)", last, phases)
	}
	for _, phase := range phases {
		if phase == "paused" || phase == "blocked" || phase == "complete" {
			t.Fatalf("unchanged poll reported %s", phase)
		}
	}
	for _, level := range seen {
		if level > slog.LevelDebug {
			t.Fatalf("unchanged poll logged at %s", level)
		}
	}
	if a.scans != 1 || f.reads != reads || len(s.History) != 1 || scanWorktrees(t, e.config) != 1 {
		t.Fatal("unchanged poll investigated")
	}
}

func TestZeroFindingDryRunThenRealRescans(t *testing.T) {
	e, s, f, a, _, c := changeFixture(t, true)
	e.config.DryRun = true
	a.findings = []Finding{}
	if err := poll(e, s, c); err != nil {
		t.Fatal(err)
	}
	if s.LastCompleted == nil || !s.LastCompleted.DryRun {
		t.Fatalf("zero-finding dry run marker: %+v", s.LastCompleted)
	}
	if err := poll(e, s, c); !errors.Is(err, errUnchanged) {
		t.Fatalf("dry run rescanned: %v", err)
	}
	e.config.DryRun = false
	a.findings = nil
	if err := poll(e, s, c); err != nil {
		t.Fatal(err)
	}
	if a.scans != 2 || f.creates != 1 || s.LastCompleted.DryRun {
		t.Fatalf("real mode did not rescan: scans %d, creates %d", a.scans, f.creates)
	}
}

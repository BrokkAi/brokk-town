package town

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// Monday 21 September 2026 is the week these tests are anchored to.
func local(day, hour, minute int) time.Time {
	return time.Date(2026, 9, day, hour, minute, 0, 0, time.Local)
}

func evenings() []QuietWindow {
	return []QuietWindow{{Days: []string{"mon", "tue", "wed", "thu", "fri"}, Start: "18:00", End: "08:00"}}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func TestQuietWindowValidationNamesTheProblem(t *testing.T) {
	for _, tc := range []struct {
		windows []QuietWindow
		want    string
	}{
		{[]QuietWindow{{Start: "18:00", End: "08:00"}}, "quiet window 1: needs at least one day"},
		{[]QuietWindow{{Days: []string{"monday"}, Start: "18:00", End: "08:00"}}, `day "monday" is not one of`},
		{[]QuietWindow{{Days: []string{"mon", "mon"}, Start: "18:00", End: "08:00"}}, `day "mon" is listed twice`},
		{[]QuietWindow{{Days: []string{"mon"}, Start: "8:00", End: "09:00"}}, `start "8:00" must be HH:MM`},
		{[]QuietWindow{{Days: []string{"mon"}, Start: "24:00", End: "09:00"}}, `start "24:00" must be HH:MM from 00:00 to 23:59`},
		{[]QuietWindow{{Days: []string{"mon"}, Start: "08:00", End: "25:00"}}, `end "25:00" must be HH:MM`},
		{[]QuietWindow{{Days: []string{"mon"}, Start: "08:00", End: "08:60"}}, `end "08:60"`},
		{[]QuietWindow{{Days: []string{"mon"}, Start: "08:00", End: "08:00"}}, "start and end are both 08:00"},
		{append(evenings(), QuietWindow{Days: []string{"sun"}, Start: "xx", End: "01:00"}), "quiet window 2: start"},
	} {
		err := ValidateQuietHours(tc.windows)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: error %v, want %q", tc.windows, err, tc.want)
		}
	}
	many := make([]QuietWindow, MaxQuietWindows+1)
	for i := range many {
		many[i] = evenings()[0]
	}
	if err := ValidateQuietHours(many); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("an unbounded schedule was accepted: %v", err)
	}
	for _, ok := range [][]QuietWindow{nil, {}, evenings(), {{Days: []string{"sat", "sun"}, Start: "00:00", End: "24:00"}}} {
		if err := ValidateQuietHours(ok); err != nil {
			t.Errorf("valid schedule %+v rejected: %v", ok, err)
		}
	}
	// A town config and the service config both refuse a bad schedule.
	bad := []QuietWindow{{Days: []string{"mon"}, Start: "09:00", End: "09:00"}}
	cfg := DefaultConfig("acme/quiet")
	cfg.QuietHours = &bad
	if err := cfg.Validate(); err == nil {
		t.Fatal("town config accepted an empty window")
	}
	if err := (ServiceConfig{MaxWorkers: 2, QuietHours: bad}).Validate(); err == nil {
		t.Fatal("service config accepted an empty window")
	}
}

func TestParseQuietHoursReadsTheCompactForm(t *testing.T) {
	windows, err := ParseQuietHours("weekdays 18:00-08:00; sat,sun 00:00-24:00; fri-mon 12:00-13:00")
	if err != nil {
		t.Fatal(err)
	}
	want := []QuietWindow{
		{Days: []string{"mon", "tue", "wed", "thu", "fri"}, Start: "18:00", End: "08:00"},
		{Days: []string{"sat", "sun"}, Start: "00:00", End: "24:00"},
		{Days: []string{"fri", "sat", "sun", "mon"}, Start: "12:00", End: "13:00"},
	}
	got, _ := json.Marshal(windows)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) {
		t.Fatalf("parsed %s, want %s", got, expected)
	}
	if FormatQuietHours(want[:1]) != "mon,tue,wed,thu,fri 18:00-08:00" {
		t.Fatalf("format = %q", FormatQuietHours(want[:1]))
	}
	if daily, err := ParseQuietHours("DAILY 01:00-02:00"); err != nil || len(daily[0].Days) != 7 {
		t.Fatalf("daily = %+v, %v", daily, err)
	}
	for spec, want := range map[string]string{
		"mon":                 "write DAYS HH:MM-HH:MM",
		"mon 18:00":           "must be HH:MM-HH:MM",
		"funday 18:00-19:00":  `day "funday"`,
		"mon 18:00to19:00":    "must be HH:MM-HH:MM",
		"mon 18:00-18:00":     "start and end are both",
		"mon-xyz 01:00-02:00": `day "xyz"`,
	} {
		if _, err := ParseQuietHours(spec); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want %q", spec, err, want)
		}
	}
}

func TestQuietWindowStartsEndsAndCrossesMidnight(t *testing.T) {
	windows := evenings()
	for _, tc := range []struct {
		at     time.Time
		active bool
		until  time.Time
		next   time.Time
	}{
		{local(21, 17, 59), false, time.Time{}, local(21, 18, 0)},
		{local(21, 18, 0), true, local(22, 8, 0), time.Time{}},
		{local(21, 23, 59), true, local(22, 8, 0), time.Time{}},
		{local(22, 0, 0), true, local(22, 8, 0), time.Time{}},
		{local(22, 7, 59), true, local(22, 8, 0), time.Time{}},
		{local(22, 8, 0), false, time.Time{}, local(22, 18, 0)},
		// Friday's window runs into Saturday morning; the weekend is free.
		{local(26, 7, 0), true, local(26, 8, 0), time.Time{}},
		{local(26, 9, 0), false, time.Time{}, local(28, 18, 0)},
	} {
		q := quietStateIn(windows, "town", tc.at)
		if q.Active != tc.active || !q.Until.Equal(tc.until) || !q.Next.Equal(tc.next) {
			t.Errorf("at %s: active=%v until=%s next=%s, want %v %s %s", tc.at.Format("Mon 15:04"), q.Active, q.Until, q.Next, tc.active, tc.until, tc.next)
		}
		if q.Active && !strings.Contains(q.Reason, "running work finishes") {
			t.Errorf("reason %q does not say running work finishes", q.Reason)
		}
	}
	// Saturday's late window wraps into Sunday, across the week boundary.
	late := []QuietWindow{{Days: []string{"sat"}, Start: "22:00", End: "02:00"}}
	if q := quietStateIn(late, "town", local(27, 1, 0)); !q.Active || !q.Until.Equal(local(27, 2, 0)) {
		t.Fatalf("Saturday's window did not reach Sunday: %+v", q)
	}
}

func TestOverlappingAndTouchingWindowsHoldUntilTheLastEnds(t *testing.T) {
	windows := []QuietWindow{
		{Days: []string{"mon"}, Start: "09:00", End: "12:00"},
		{Days: []string{"mon"}, Start: "11:00", End: "13:00"},
		{Days: []string{"mon"}, Start: "13:00", End: "14:00"},
	}
	q := quietStateIn(windows, "town", local(21, 10, 0))
	if !q.Active || !q.Until.Equal(local(21, 14, 0)) {
		t.Fatalf("overlapping windows ended early: %+v", q)
	}
	always := []QuietWindow{{Days: quietDays, Start: "00:00", End: "24:00"}}
	q = quietStateIn(always, "service", local(23, 12, 0))
	if !q.Active || !q.Until.IsZero() || !strings.Contains(q.Reason, "whole week") {
		t.Fatalf("a whole-week schedule = %+v", q)
	}
}

func TestQuietWindowsFollowTheWallClockAcrossDaylightSaving(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	// 8 March 2026: clocks jump from 02:00 to 03:00.
	spring := []QuietWindow{{Days: []string{"sun"}, Start: "01:00", End: "02:30"}}
	before := time.Date(2026, 3, 8, 1, 59, 0, 0, ny)
	if !quietStateIn(spring, "town", before).Active {
		t.Fatal("the window was not quiet before the jump")
	}
	if after := before.Add(time.Minute); quietStateIn(spring, "town", after).Active {
		t.Fatalf("the window stayed quiet at %s, a wall-clock time after its end", after)
	}
	// 1 November 2026: 01:00-02:00 happens twice.
	fall := []QuietWindow{{Days: []string{"sun"}, Start: "01:00", End: "01:30"}}
	first := time.Date(2026, 11, 1, 1, 15, 0, 0, ny)
	second := first.Add(time.Hour)
	if second.Hour() != 1 || second.Minute() != 15 {
		t.Fatalf("fixture did not land in the repeated hour: %s", second)
	}
	for _, at := range []time.Time{first, second} {
		if !quietStateIn(fall, "town", at).Active {
			t.Fatalf("the repeated hour was not quiet at %s", at)
		}
	}
	if quietStateIn(fall, "town", first.Add(30*time.Minute)).Active {
		t.Fatal("quiet past the window's end on the wall clock")
	}
	// A window spanning the change ends at its wall-clock time.
	night := []QuietWindow{{Days: []string{"sun"}, Start: "00:00", End: "03:00"}}
	start := time.Date(2026, 11, 1, 0, 30, 0, 0, ny)
	q := quietStateIn(night, "town", start)
	if !q.Active || q.Until.Hour() != 3 || q.Until.Sub(start) != 210*time.Minute {
		t.Fatalf("until = %s (%s later), want 03:00 three and a half hours on", q.Until, q.Until.Sub(start))
	}
}

func TestTownQuietHoursOverrideOrOptOutOfTheServiceDefault(t *testing.T) {
	service := evenings()
	x := &Town{Config: DefaultConfig("acme/quiet")}
	at := local(21, 20, 0)
	if q := x.QuietState(at, service); !q.Active || q.Source != "service" {
		t.Fatalf("a town without its own windows ignored the default: %+v", q)
	}
	none := []QuietWindow{}
	x.Config.QuietHours = &none
	if q := x.QuietState(at, service); q.Active || q.Source != "" {
		t.Fatalf("an opted-out town followed the default: %+v", q)
	}
	own := []QuietWindow{{Days: []string{"mon"}, Start: "21:00", End: "22:00"}}
	x.Config.QuietHours = &own
	if q := x.QuietState(at, service); q.Active || q.Source != "town" || !q.Next.Equal(local(21, 21, 0)) {
		t.Fatalf("the town's own windows did not replace the default: %+v", q)
	}
	x.Config.QuietHours = nil
	if q := x.QuietState(at, nil); q.Active || q.Source != "" || q.Windows == nil {
		t.Fatalf("no schedule anywhere reads as quiet: %+v", q)
	}
}

func TestQuietHoursHoldAgentDispatchButKeepTheInventoryRunning(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/quiet", nil)
	update(t, s, func(st *State) {
		st.ServiceConfig.QuietHours = evenings()
		st.Towns[x.ID].Workers[Repo].Enabled = true
	})
	inside, outside := local(21, 20, 0), local(22, 9, 0)
	if eligible, _ := s.dispatchEligibility(x.ID, Issue, inside); eligible {
		t.Fatal("quiet hours still dispatched agent work")
	}
	if eligible, _ := s.dispatchEligibility(x.ID, Repo, inside); !eligible {
		t.Fatal("quiet hours stopped the repository inventory")
	}
	if eligible, _ := s.dispatchEligibility(x.ID, Issue, outside); !eligible {
		t.Fatal("agent work did not resume after the window")
	}
	// A branch repair pushes to GitHub, so it waits as well.
	sup := NewSupervisor(s, nil, nil)
	sup.now = func() time.Time { return inside }
	if sup.claimRepair(x.ID) {
		t.Fatal("a branch repair started inside quiet hours")
	}
	sup.now = func() time.Time { return outside }
	if !sup.claimRepair(x.ID) {
		t.Fatal("a branch repair was held outside quiet hours")
	}
	sup.releaseRepair(x.ID)
}

func TestQuietHoursLetRunningWorkFinishAndResumeWithoutTheOperator(t *testing.T) {
	s := testStore(t, false)
	addCapacityTown(t, s, "acme/night", Issue, Bug)
	update(t, s, func(st *State) {
		st.Towns["acme/night"].Config.QuietHours = &[]QuietWindow{{Days: []string{"mon"}, Start: "18:00", End: "19:00"}}
		// Bug waits for its next poll; only Issue is due before the window.
		st.Towns["acme/night"].Workers[Bug].Next = local(21, 17, 58)
	})
	clock := &testClock{now: local(21, 17, 55)}
	w := newCapacityWorker(t, "acme/night:issue", "acme/night:bug")
	sup := NewSupervisor(s, nil, w)
	sup.now = clock.Now
	ctx := context.Background()
	sup.schedule(ctx)
	running := waitCapacityStarts(t, w.started, 1)[0]
	if running != "acme/night:issue" {
		t.Fatalf("started %s before the window, want the issue house", running)
	}
	// The window opens with Issue still at work and Bug now due.
	clock.Set(local(21, 18, 5))
	sup.schedule(ctx)
	select {
	case extra := <-w.started:
		t.Fatalf("dispatched %s inside quiet hours", extra)
	case <-time.After(100 * time.Millisecond):
	}
	town := s.Snapshot().Towns["acme/night"]
	if !town.Quiet {
		t.Fatal("entering quiet hours was not recorded")
	}
	if town.Workers[Issue].Status != "working" {
		t.Fatalf("quiet hours interrupted running work: %s", town.Workers[Issue].Status)
	}
	close(w.release[running])
	eventually(t, func() bool { return s.Snapshot().Towns["acme/night"].Workers[Issue].Status == "waiting" })
	if s.Snapshot().Towns["acme/night"].Workers[Issue].Error != "" {
		t.Fatal("the running attempt failed instead of finishing")
	}
	// After the window the scheduler resumes on its own.
	clock.Set(local(21, 19, 0))
	sup.schedule(ctx)
	// Both houses are due again; the one held through the window is among them.
	resumed := waitCapacityStarts(t, w.started, 2)
	if resumed[0] != "acme/night:bug" && resumed[1] != "acme/night:bug" {
		t.Fatalf("resumed %v, want the bug house that became due in the window", resumed)
	}
	close(w.release["acme/night:bug"])
	sup.wg.Wait()
	town = s.Snapshot().Towns["acme/night"]
	if town.Quiet {
		t.Fatal("leaving quiet hours was not recorded")
	}
	began, ended := 0, 0
	for _, e := range s.Snapshot().Events {
		if strings.HasPrefix(e.Title, "Quiet hours began") {
			began++
		}
		if strings.HasPrefix(e.Title, "Quiet hours ended") {
			ended++
		}
	}
	if began != 1 || ended != 1 {
		t.Fatalf("quiet transitions announced %d/%d times, want once each", began, ended)
	}
}

func TestQuietHoursHoldTownsOwnGitHubWritesDuringTheInventory(t *testing.T) {
	s := testStore(t, false)
	x := setupPR(t, s, 1)
	update(t, s, func(st *State) {
		task := st.Towns[x.ID].Tasks["pr:1"]
		task.Stage = "merged"
		task.FollowUps = []Finding{{ID: "finding:b", State: "deferred", Detail: "[P3] Rename the helper", Severity: "P3"}}
		st.Towns[x.ID].Config.QuietHours = &[]QuietWindow{{Days: []string{"mon"}, Start: "18:00", End: "19:00"}}
	})
	gh := newGH(1)
	merged := pull(1)
	at := time.Now()
	merged.State, merged.MergedAt = "closed", &at
	gh.snapshot = inventory(merged)
	sup := NewSupervisor(s, gh, &healthObserver{gh: gh})
	sup.now = func() time.Time { return local(21, 18, 30) }
	if err := sup.reconcileNow(context.Background(), s.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.filed) != 0 {
		t.Fatalf("filed follow-ups inside quiet hours: %+v", gh.filed)
	}
	if !s.Snapshot().Towns[x.ID].LastSync.After(time.Time{}) {
		t.Fatal("quiet hours stopped the inventory itself")
	}
	sup.now = func() time.Time { return local(21, 19, 30) }
	if err := sup.reconcileNow(context.Background(), s.Snapshot().Towns[x.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.filed) != 1 {
		t.Fatalf("follow-ups were not filed after the window: %+v", gh.filed)
	}
}

func TestQuietHoursReachClientsDistinctFromPausedWorkingAndFailed(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/public", nil)
	update(t, s, func(st *State) {
		st.ServiceConfig.QuietHours = evenings()
		town := st.Towns[x.ID]
		town.Workers[Repo].Enabled = true
		town.Workers[Repo].Status = "waiting"
		town.Workers[Issue].Status = "waiting"
		town.Workers[Review].Enabled, town.Workers[Review].Status = true, "working"
		town.Workers[Feature].Enabled, town.Workers[Feature].Status = true, "failed"
	})
	read := func(at time.Time) (map[string]string, QuietState, map[string]any) {
		raw, err := json.Marshal(s.Snapshot().PublicAt(at))
		if err != nil {
			t.Fatal(err)
		}
		var public struct {
			Towns map[string]struct {
				Workers map[string]struct {
					Status string `json:"status"`
				} `json:"workers"`
				Quiet  QuietState     `json:"quiet_hours"`
				Config map[string]any `json:"config"`
			} `json:"towns"`
		}
		if err := json.Unmarshal(raw, &public); err != nil {
			t.Fatal(err)
		}
		statuses := map[string]string{}
		for role, w := range public.Towns[x.ID].Workers {
			statuses[role] = w.Status
		}
		return statuses, public.Towns[x.ID].Quiet, public.Towns[x.ID].Config
	}
	statuses, quiet, config := read(local(21, 20, 0))
	if !quiet.Active || quiet.Source != "service" || quiet.Until.IsZero() || quiet.Reason == "" {
		t.Fatalf("public quiet hours = %+v", quiet)
	}
	for role, want := range map[string]string{"issue": "quiet", "bug": "paused", "review": "working", "feature": "failed", "repo": "waiting"} {
		if statuses[role] != want {
			t.Errorf("%s shows %q inside quiet hours, want %q", role, statuses[role], want)
		}
	}
	if value, present := config["quiet_hours"]; !present || value != nil {
		t.Fatalf("public config quiet_hours = %v (present %v), want null for a town following the default", value, present)
	}
	statuses, quiet, _ = read(local(22, 9, 0))
	if quiet.Active || quiet.Next.IsZero() || statuses["issue"] != "waiting" {
		t.Fatalf("outside the window: quiet=%+v issue=%q", quiet, statuses["issue"])
	}
}

func TestPausedAndQuietHoursSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	x := budgetTown(t, s, "acme/restart", nil)
	inside := local(21, 20, 0)
	update(t, s, func(st *State) {
		st.ServiceConfig.QuietHours = evenings()
		recordQuietTransitions(st, inside)
	})
	s.Close()

	reopened, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	state := reopened.Snapshot()
	if len(state.ServiceConfig.QuietHours) != 1 || !state.Towns[x.ID].Quiet {
		t.Fatalf("restart lost the quiet schedule: %+v quiet=%v", state.ServiceConfig, state.Towns[x.ID].Quiet)
	}
	workers := state.PublicAt(inside)["towns"].(map[string]any)[x.ID].(map[string]any)["workers"].(map[string]any)
	if got := workers["issue"].(map[string]any)["status"]; got != "quiet" {
		t.Fatalf("an enabled house reads %v after restart inside quiet hours", got)
	}
	if got := workers["bug"].(map[string]any)["status"]; got != "paused" {
		t.Fatalf("a paused house reads %v after restart inside quiet hours", got)
	}
	if eligible, _ := reopened.dispatchEligibility(x.ID, Issue, inside); eligible {
		t.Fatal("a restarted service dispatched inside quiet hours")
	}
	// The saved flag keeps the restart from announcing the window again.
	if quietTransitionDue(state, inside) {
		t.Fatal("a restart inside the window would announce it a second time")
	}
}

func TestQuietHoursSettingsRoundTrip(t *testing.T) {
	s := testStore(t, false)
	x := budgetTown(t, s, "acme/settings", nil)
	sup := NewSupervisor(s, nil, nil)
	own := []QuietWindow{{Days: []string{"sat", "sun"}, Start: "00:00", End: "24:00"}}
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{QuietHours: &QuietHoursEdit{Windows: &own}}); err != nil {
		t.Fatal(err)
	}
	if saved := s.Snapshot().Towns[x.ID].Config.QuietHours; saved == nil || len(*saved) != 1 {
		t.Fatalf("quiet hours were not saved: %+v", saved)
	}
	bad := []QuietWindow{{Days: []string{"sat"}, Start: "25:00", End: "01:00"}}
	err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{QuietHours: &QuietHoursEdit{Windows: &bad}})
	if err == nil || !strings.Contains(err.Error(), "quiet window 1: start") {
		t.Fatalf("an invalid window was not rejected clearly: %v", err)
	}
	manual := "manual"
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{MergePolicy: &manual}); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Towns[x.ID].Config.QuietHours == nil {
		t.Fatal("an unrelated edit discarded the quiet hours")
	}
	none := []QuietWindow{}
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{QuietHours: &QuietHoursEdit{Windows: &none}}); err != nil {
		t.Fatal(err)
	}
	if saved := s.Snapshot().Towns[x.ID].Config.QuietHours; saved == nil || len(*saved) != 0 {
		t.Fatalf("opting out did not save an empty schedule: %+v", saved)
	}
	if err := sup.ApplySettings(x.ID, "", AgentSettings{}, TownSettings{QuietHours: &QuietHoursEdit{}}); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Towns[x.ID].Config.QuietHours != nil {
		t.Fatal("following the default left a town schedule saved")
	}
	// The service default and capacity are edited independently.
	if err := sup.SetQuietHours(evenings()); err != nil {
		t.Fatal(err)
	}
	if err := sup.SetCapacity(7); err != nil {
		t.Fatal(err)
	}
	cfg := s.Snapshot().ServiceConfig
	if cfg.MaxWorkers != 7 || len(cfg.QuietHours) != 1 {
		t.Fatalf("service config = %+v", cfg)
	}
	if err := sup.SetQuietHours(bad); err == nil {
		t.Fatal("an invalid service default was accepted")
	}
	if err := sup.SetQuietHours(nil); err != nil || s.Snapshot().ServiceConfig.QuietHours != nil {
		t.Fatalf("clearing the service default: %v %+v", err, s.Snapshot().ServiceConfig)
	}
}

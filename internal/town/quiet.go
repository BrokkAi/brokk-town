package town

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Quiet hours are weekly wall-clock windows in which Town starts no new agent
// work and makes none of its own GitHub writes. Work already running finishes,
// the way it does after Pause, and the repository inventory keeps running so
// uncertain writes still reconcile. Nothing is replayed when a window ends:
// every house simply becomes eligible again on its usual schedule.
//
// Windows are read on the local wall clock of the machine running Town. A
// window names the days it starts on; an end at or before its start runs past
// midnight into the next day, and 24:00 ends a window at midnight. Membership
// is decided on the wall clock, so a window holds through a daylight-saving
// change by the clock on the wall: an hour the clock skips is never quiet, and
// an hour it repeats is quiet both times.

// QuietWindow is one weekly quiet range.
type QuietWindow struct {
	// Days are the days the window starts on: mon, tue, wed, thu, fri, sat, sun.
	Days []string `json:"days"`
	// Start and End are HH:MM on the local wall clock. End may be 24:00.
	Start string `json:"start"`
	End   string `json:"end"`
}

// MaxQuietWindows bounds how many windows one schedule may hold.
const MaxQuietWindows = 32

const minutesPerDay = 24 * 60
const minutesPerWeek = 7 * minutesPerDay

var quietDays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// clockMinutes reads HH:MM as minutes after midnight. 24:00 is accepted only
// where end is true.
func clockMinutes(value string, end bool) (int, bool) {
	h, m, ok := strings.Cut(value, ":")
	digits := func(s string) bool { return len(s) == 2 && strings.Trim(s, "0123456789") == "" }
	// Atoi would take a sign; HH:MM is two digits each, nothing else.
	if !ok || !digits(h) || !digits(m) {
		return 0, false
	}
	hours, err1 := strconv.Atoi(h)
	minutes, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hours < 0 || minutes < 0 || minutes > 59 {
		return 0, false
	}
	if hours == 24 && minutes == 0 && end {
		return minutesPerDay, true
	}
	if hours > 23 {
		return 0, false
	}
	return hours*60 + minutes, true
}

// Validate reports the first thing wrong with one window.
func (w QuietWindow) Validate() error {
	if len(w.Days) == 0 {
		return errors.New("needs at least one day (mon, tue, wed, thu, fri, sat, sun)")
	}
	seen := map[string]bool{}
	for _, d := range w.Days {
		if !slices.Contains(quietDays, d) {
			return fmt.Errorf("day %q is not one of mon, tue, wed, thu, fri, sat, sun", d)
		}
		if seen[d] {
			return fmt.Errorf("day %q is listed twice", d)
		}
		seen[d] = true
	}
	start, ok := clockMinutes(w.Start, false)
	if !ok {
		return fmt.Errorf("start %q must be HH:MM from 00:00 to 23:59", w.Start)
	}
	end, ok := clockMinutes(w.End, true)
	if !ok {
		return fmt.Errorf("end %q must be HH:MM from 00:00 to 24:00", w.End)
	}
	if start == end {
		return fmt.Errorf("start and end are both %s; use 00:00-24:00 for a whole day", w.Start)
	}
	return nil
}

// ValidateQuietHours checks a whole schedule. Overlapping windows are allowed;
// they are quiet for as long as any of them is.
func ValidateQuietHours(windows []QuietWindow) error {
	if len(windows) > MaxQuietWindows {
		return fmt.Errorf("quiet hours hold at most %d windows", MaxQuietWindows)
	}
	for i, w := range windows {
		if err := w.Validate(); err != nil {
			return fmt.Errorf("quiet window %d: %w", i+1, err)
		}
	}
	return nil
}

// span is a half-open range of minutes within the week, Sunday 00:00 = 0.
type span struct{ from, to int }

// quietSpans expands a valid schedule into week-minute spans. A window that
// runs past Saturday midnight wraps to Sunday.
func quietSpans(windows []QuietWindow) []span {
	var spans []span
	for _, w := range windows {
		start, ok1 := clockMinutes(w.Start, false)
		end, ok2 := clockMinutes(w.End, true)
		if !ok1 || !ok2 || start == end {
			continue
		}
		length := end - start
		if end < start {
			length += minutesPerDay
		}
		for _, d := range w.Days {
			day := slices.Index(quietDays, d)
			if day < 0 {
				continue
			}
			from := day*minutesPerDay + start
			to := from + length
			if to > minutesPerWeek {
				spans = append(spans, span{from, minutesPerWeek}, span{0, to - minutesPerWeek})
			} else {
				spans = append(spans, span{from, to})
			}
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	return spans
}

func covered(spans []span, minute int) (int, bool) {
	end, found := 0, false
	for _, s := range spans {
		if s.from <= minute && minute < s.to && s.to > end {
			end, found = s.to, true
		}
	}
	return end, found
}

// weekMinute is the wall-clock position of t in its week.
func weekMinute(t time.Time) int {
	return int(t.Weekday())*minutesPerDay + t.Hour()*60 + t.Minute()
}

// wallReading is t's wall-clock reading as a zone-free value, so readings in
// different offsets compare by what the clock showed.
func wallReading(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// wallClockAfter is the first instant after t at which the wall clock reads
// at least the minute lying delta wall-clock minutes after t's own minute.
// Arithmetic is on the wall clock, so a daylight-saving change moves the
// instant rather than the reading: a reading the clock skips resolves to the
// moment it jumps past it, and a reading it repeats to the occurrence still
// ahead of t.
func wallClockAfter(t time.Time, delta int) time.Time {
	loc := t.Location()
	target := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute()+delta, 0, 0, time.UTC)
	at := time.Date(target.Year(), target.Month(), target.Day(), target.Hour(), target.Minute(), 0, 0, loc)
	if !wallReading(at).Equal(target) {
		// The clock skips this reading. The answer is the transition at which
		// it jumps from before the target to after it.
		start, end := at.ZoneBounds()
		for _, edge := range []time.Time{start, end} {
			if !edge.IsZero() && !wallReading(edge).Before(target) && wallReading(edge.Add(-time.Second)).Before(target) {
				return edge
			}
		}
		return at
	}
	if !at.After(t) {
		// A repeated reading whose first occurrence is already past: take the
		// same reading under the offset that follows.
		if _, end := at.ZoneBounds(); !end.IsZero() {
			_, offset := end.Zone()
			later := time.Unix(target.Unix()-int64(offset), 0).In(loc)
			if later.After(t) && wallReading(later).Equal(target) {
				return later
			}
		}
	}
	return at
}

// QuietState is the operator-facing view of a town's quiet hours. It is
// computed for display and for the scheduling gate, never stored.
type QuietState struct {
	// Source is "town" when the town sets its own windows, "service" when it
	// follows the service default, and "" when no quiet hours apply.
	Source  string        `json:"source"`
	Windows []QuietWindow `json:"windows"`
	// Active holds new agent dispatch and Town's own GitHub writes.
	Active bool `json:"active"`
	// Until is when an active quiet period ends; zero when the windows cover
	// the whole week. Next is when the next one starts while none is active.
	Until  time.Time `json:"until,omitzero"`
	Next   time.Time `json:"next,omitzero"`
	Reason string    `json:"reason,omitempty"`
}

// quietWindows resolves the schedule a town follows: its own, when it set one
// (an empty list opts out), else the service default.
func (t *Town) quietWindows(service []QuietWindow) ([]QuietWindow, string) {
	if t.Config.QuietHours != nil {
		if len(*t.Config.QuietHours) == 0 {
			return nil, ""
		}
		return *t.Config.QuietHours, "town"
	}
	if len(service) == 0 {
		return nil, ""
	}
	return service, "service"
}

// QuietState reports whether this town is inside quiet hours at now, on the
// local wall clock.
func (t *Town) QuietState(now time.Time, service []QuietWindow) QuietState {
	windows, source := t.quietWindows(service)
	return quietStateIn(windows, source, now.In(time.Local))
}

func quietStateIn(windows []QuietWindow, source string, now time.Time) QuietState {
	state := QuietState{Source: source, Windows: windows}
	if state.Windows == nil {
		state.Windows = []QuietWindow{}
	}
	spans := quietSpans(windows)
	if len(spans) == 0 {
		return state
	}
	minute := weekMinute(now)
	if _, quiet := covered(spans, minute); quiet {
		state.Active = true
		// Follow touching and overlapping windows to the first minute none
		// of them covers.
		delta := 0
		for delta < minutesPerWeek {
			end, ok := covered(spans, (minute+delta)%minutesPerWeek)
			if !ok {
				break
			}
			delta += end - (minute+delta)%minutesPerWeek
		}
		if delta >= minutesPerWeek {
			state.Reason = "Quiet hours cover the whole week. No new agent work starts until they are changed; running work finishes."
			return state
		}
		state.Until = wallClockAfter(now, delta)
		state.Reason = fmt.Sprintf("Quiet hours until %s. No new agent work or GitHub writes by Town start until then; running work finishes and the repository is still watched.", state.Until.Format("Mon 15:04"))
		return state
	}
	next := minutesPerWeek
	for _, s := range spans {
		ahead := (s.from - minute + minutesPerWeek) % minutesPerWeek
		if ahead < next {
			next = ahead
		}
	}
	state.Next = wallClockAfter(now, next)
	return state
}

// quietActive reports whether a town's quiet hours hold at now. Running work
// is never interrupted by them.
func (s *Store) quietActive(id string, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := s.state.Towns[id]
	return t != nil && !t.Deleted && t.QuietState(now, s.state.ServiceConfig.QuietHours).Active
}

// recordQuietTransitions notes each town entering or leaving quiet hours, so
// the event feed says why work stopped and when it resumed. The scheduling
// gate reads the clock, not this flag: the flag only keeps the announcement
// from repeating.
func recordQuietTransitions(st *State, now time.Time) {
	for _, t := range st.Towns {
		if t.Deleted {
			continue
		}
		q := t.QuietState(now, st.ServiceConfig.QuietHours)
		if q.Active == t.Quiet {
			continue
		}
		t.Quiet = q.Active
		title := "Quiet hours ended; scheduling resumed"
		if q.Active {
			title = "Quiet hours began; running work finishes and new work waits"
			if !q.Until.IsZero() {
				title = "Quiet hours began; new work waits until " + q.Until.Format("Mon 15:04")
			}
		}
		st.Event(t.ID, "control", "hall", "hall", "", title, now)
	}
}

// quietTransitionDue reports whether any town's recorded quiet flag is stale.
func quietTransitionDue(st State, now time.Time) bool {
	for _, t := range st.Towns {
		if !t.Deleted && t.QuietState(now, st.ServiceConfig.QuietHours).Active != t.Quiet {
			return true
		}
	}
	return false
}

// SetQuietHours replaces the service default quiet hours. An empty schedule
// removes it; towns that set their own windows are unaffected.
func (s *Supervisor) SetQuietHours(windows []QuietWindow) error {
	if err := ValidateQuietHours(windows); err != nil {
		return err
	}
	if len(windows) == 0 {
		windows = nil
	}
	err := s.Store.Update(func(st *State) error {
		st.ServiceConfig.QuietHours = windows
		return nil
	})
	if err == nil {
		s.notifyScheduler()
	}
	return err
}

// ParseQuietHours reads the compact form the CLI and browser accept:
// windows separated by semicolons, each "DAYS HH:MM-HH:MM". DAYS is a comma
// list of day names or ranges (mon-fri), or daily, weekdays or weekends.
func ParseQuietHours(spec string) ([]QuietWindow, error) {
	windows := []QuietWindow{}
	for i, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		if len(fields) != 2 {
			return nil, fmt.Errorf("quiet window %d %q: write DAYS HH:MM-HH:MM, such as mon-fri 18:00-08:00", i+1, part)
		}
		days, err := parseQuietDays(fields[0])
		if err != nil {
			return nil, fmt.Errorf("quiet window %d: %w", i+1, err)
		}
		start, end, ok := strings.Cut(fields[1], "-")
		if !ok {
			return nil, fmt.Errorf("quiet window %d: time range %q must be HH:MM-HH:MM", i+1, fields[1])
		}
		windows = append(windows, QuietWindow{Days: days, Start: start, End: end})
	}
	if len(windows) == 0 {
		// An empty schedule must be asked for by name, never inferred from a
		// blank or stray separator.
		return nil, errors.New("no quiet windows given; write DAYS HH:MM-HH:MM, or none for no quiet hours")
	}
	return windows, ValidateQuietHours(windows)
}

func parseQuietDays(value string) ([]string, error) {
	value = strings.ToLower(value)
	switch value {
	case "daily":
		value = "mon-sun"
	case "weekdays":
		value = "mon-fri"
	case "weekends":
		value = "sat,sun"
	}
	// Monday-first order, so mon-sun and fri-mon read the way a week does.
	week := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	var days []string
	add := func(d string) {
		if !slices.Contains(days, d) {
			days = append(days, d)
		}
	}
	for _, item := range strings.Split(value, ",") {
		from, to, isRange := strings.Cut(item, "-")
		a := slices.Index(week, from)
		if a < 0 {
			return nil, fmt.Errorf("day %q is not one of mon, tue, wed, thu, fri, sat, sun", from)
		}
		if !isRange {
			add(from)
			continue
		}
		b := slices.Index(week, to)
		if b < 0 {
			return nil, fmt.Errorf("day %q is not one of mon, tue, wed, thu, fri, sat, sun", to)
		}
		for i := a; ; i = (i + 1) % 7 {
			add(week[i])
			if i == b {
				break
			}
		}
	}
	return days, nil
}

// FormatQuietHours writes a schedule back in the compact form.
func FormatQuietHours(windows []QuietWindow) string {
	parts := make([]string, len(windows))
	for i, w := range windows {
		parts[i] = strings.Join(w.Days, ",") + " " + w.Start + "-" + w.End
	}
	return strings.Join(parts, "; ")
}

// markQuiet shows each house that quiet hours are holding as "quiet" rather
// than waiting, so a client can tell a scheduled pause from an operator's
// Pause, from work in progress, and from a failure. Only enabled houses that
// would otherwise be waiting change: paused, working, pausing and failed
// houses keep their own status, and the repository inventory keeps running.
func (t *Town) markQuiet(public map[string]any) {
	workers, _ := public["workers"].(map[string]any)
	for role, w := range t.Workers {
		if !OccupiesAgentSlot(role) || !w.Enabled || w.Status != "waiting" || w.Recovery != nil {
			continue
		}
		if entry, ok := workers[string(role)].(map[string]any); ok {
			entry["status"] = "quiet"
		}
	}
}

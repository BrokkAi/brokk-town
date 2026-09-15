package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/BrokkAi/brokk-town/internal/town"
	"github.com/rivo/uniseg"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func cell(s string, width int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	var out strings.Builder
	n := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		w := g.Width()
		if n+w > width {
			break
		}
		out.WriteString(g.Str())
		n += w
	}
	if n < width {
		out.WriteString(strings.Repeat(" ", width-n))
	}
	return out.String()
}
func tui(ctx context.Context, c connection) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("TUI requires a terminal; use bt status for JSON")
	}
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), old)
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b[?2004h")
	defer fmt.Print("\x1b[0m\x1b[?25h\x1b[?2004l\x1b[?1049l")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type tuiSnapshot struct {
		town.State
		Version string `json:"version"`
	}
	snapshots := make(chan tuiSnapshot, 1)
	messages := make(chan string, 4)
	commands := make(chan map[string]string, 4)
	upgrades := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			var state tuiSnapshot
			err := request(ctx, c, "GET", "/api/state", nil, &state)
			if err != nil {
				select {
				case messages <- err.Error():
				default:
				}
			} else {
				select {
				case snapshots <- state:
				default:
				}
			}
			select {
			case <-ctx.Done():
				return
			case body := <-commands:
				var result any
				err := request(ctx, c, "POST", "/api/control", body, &result)
				message := "Command accepted"
				if err != nil {
					message = err.Error()
				}
				select {
				case messages <- message:
				default:
				}
			case <-upgrades:
				var result any
				err := request(ctx, c, "POST", "/api/update", map[string]any{}, &result)
				message := "Town upgraded · restart the service to use it"
				if err != nil {
					message = err.Error()
				}
				select {
				case messages <- message:
				default:
				}
			case <-ticker.C:
			}
		}
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}()
	var state town.State
	var serviceVersion string
	selectedTown, selectedRole := 0, 0
	overview := true
	message := "Connecting to town service…"
	pendingDelete := ""
	pendingUpgrade := false
	var keys keyDecoder
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case v := <-snapshots:
			state = v.State
			serviceVersion = v.Version
			message = ""
		case m := <-messages:
			message = m
		case <-ticker.C:
			fds := []unix.PollFd{{Fd: int32(os.Stdin.Fd()), Events: unix.POLLIN}}
			if _, err = unix.Poll(fds, 0); err != nil && !errors.Is(err, unix.EINTR) {
				return err
			}
			if fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
				return nil
			}
			if fds[0].Revents&unix.POLLIN != 0 {
				var buffer [128]byte
				n, e := unix.Read(int(os.Stdin.Fd()), buffer[:])
				if e != nil {
					return e
				}
				for _, key := range keys.feed(string(buffer[:n])) {
					if pendingUpgrade {
						pendingUpgrade = false
						if key != "u" {
							message = "Upgrade canceled"
							continue
						}
						select {
						case upgrades <- struct{}{}:
							message = "Upgrading Town…"
						default:
							message = "An upgrade is already pending"
						}
						continue
					}
					if key == "u" && state.Update != nil {
						pendingUpgrade = true
						message = fmt.Sprintf("Install Town %s? press u again to confirm · any key cancels", state.Update.Latest)
						continue
					}
					ids := townIDs(state)
					if pendingDelete != "" && key != "q" && key != "ctrl+c" {
						if key == "y" {
							select {
							case commands <- map[string]string{"town": pendingDelete, "role": "all", "action": "delete"}:
								message = "Deleting town…"
							default:
								message = "A command is already pending"
							}
							pendingDelete = ""
							continue
						}
						if key == "n" || key == "esc" {
							pendingDelete = ""
							message = "Deletion canceled"
						}
						continue
					}
					switch key {
					case "q", "ctrl+c":
						return nil
					case "0":
						overview = true
					case "d":
						if len(ids) > 0 && !overview {
							pendingDelete = ids[selectedTown%len(ids)]
						}
					case "tab":
						overview = false
						if len(ids) > 0 {
							selectedTown = (selectedTown + 1) % len(ids)
						}
					case "j", "down":
						overview = false
						selectedRole = (selectedRole + 1) % len(town.Roles)
					case "k", "up":
						overview = false
						selectedRole = (selectedRole + len(town.Roles) - 1) % len(town.Roles)
					case "1", "2", "3", "4", "5", "6":
						overview = false
						selectedRole = int(key[0] - '1')
					case "s", "p", "x", "a":
						if len(ids) == 0 || overview {
							continue
						}
						r := string(town.Roles[selectedRole])
						action := map[string]string{"s": "start", "p": "pause", "x": "stop", "a": "start"}[key]
						if key == "a" {
							r = "all"
						}
						select {
						case commands <- map[string]string{"town": ids[selectedTown%len(ids)], "role": r, "action": action}:
							message = "Sending command…"
						default:
							message = "A command is already pending"
						}
					}
				}
			}
		}
		width, height, e := term.GetSize(int(os.Stdout.Fd()))
		if e != nil {
			width, height = 80, 24
		}
		role := selectedRole
		if overview {
			role = -1
		}
		status := message
		if status == "" && state.Update != nil {
			status = fmt.Sprintf("Town %s available · press u to upgrade", state.Update.Latest)
		}
		if pendingDelete != "" {
			status = "Delete " + pendingDelete + "? y / n (GitHub stays intact)"
		}
		frame := renderTUI(state, serviceVersion, selectedTown, role, width, height, status)
		if frame != last {
			if _, err = fmt.Print("\x1b[H" + strings.ReplaceAll(frame, "\n", "\x1b[K\r\n") + "\x1b[K\x1b[J"); err != nil {
				return err
			}
			last = frame
		}
	}
}
func townIDs(s town.State) []string {
	ids := []string{}
	for id := range s.Towns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func displayWorker(t *town.Town, role town.Role) *town.Worker {
	if w := t.Workers[role]; w != nil {
		return w
	}
	// A new CLI can connect to a service that has not restarted since upgrade.
	return &town.Worker{Role: role, Status: "unavailable", Task: "Restart the town service to use this worker"}
}

func renderTUI(s town.State, version string, townIndex, roleIndex, width, height int, message string) string {
	width = max(1, width)
	height = max(1, height)
	lines := []string{}
	add := func(text string) { lines = append(lines, cell(text, width)) }
	mode := "LIVE"
	if s.Demo {
		mode = "DEMO · simulated"
	}
	active, limit := 0, town.DefaultMaxWorkers
	if s.Capacity != nil {
		active, limit = s.Capacity.Active, s.Capacity.Limit
	} else if s.ServiceConfig.MaxWorkers > 0 {
		limit = s.ServiceConfig.MaxWorkers
	}
	title := "BROKK TOWN"
	if version != "" {
		title += " " + version
	}
	add(fmt.Sprintf(" %s   %d/%d workers   %s", title, active, limit, mode))
	add(strings.Repeat("─", width))
	ids := townIDs(s)
	if len(ids) == 0 {
		add(" No towns yet. Use bt add --repo OWNER/REPO or visit bt web.")
	} else if roleIndex == -1 {
		add(" ALL TOWNS · one repository per town")
		add(" Tab: visit town   1–6: visit selected house   0: overview")
		add("")
		for _, id := range ids {
			t := s.Towns[id]
			busy, blocked, queued := 0, 0, 0
			for _, w := range t.Workers {
				if w.Status == "working" || w.Status == "pausing" {
					busy++
				}
				if w.Status == "failed" {
					blocked++
				}
			}
			for _, task := range t.Tasks {
				if task.Blocked {
					blocked++
				}
				if task.Stage != "complete" && task.Stage != "closed" && task.Stage != "merged" && task.Stage != "shipped" && task.Stage != "implemented" && task.Stage != "declined" {
					queued++
				}
			}
			add(" " + t.Config.Repo)
			mayoral := 0
			for _, task := range t.Tasks {
				if task.MayoralDecision == "pending" {
					mayoral++
				}
			}
			add(fmt.Sprintf("   %d working · %d queued · %d Mayoral decisions · %d need attention · %s", busy, queued, mayoral, blocked, t.LastRelease))
			if t.Error != "" {
				add("   " + t.Error)
			}
			add("")
		}
	} else {
		townIndex = townIndex % len(ids)
		t := s.Towns[ids[townIndex]]
		add(fmt.Sprintf(" %s   [%d/%d towns]   branch %s", t.Config.Repo, townIndex+1, len(ids), t.Config.Branch))
		add(" 0: all towns    Tab: next town    1–6 / j,k: select house")
		add(" Settings and new issues: bt settings / bt request, or bt web")
		add("")
		add("    HOUSE        STATUS       CURRENT WORK")
		for i, r := range town.Roles {
			w := displayWorker(t, r)
			mark := " "
			if i == roleIndex {
				mark = ">"
			}
			add(fmt.Sprintf(" %s  %-11s %-12s %s", mark, r, w.Status, w.Task))
		}
		add("")
		r := town.Roles[roleIndex]
		add(" AT " + strings.ToUpper(string(r)) + "'S DOOR")
		if r == town.Issue || r == town.Repo {
			funnelIDs := make([]string, 0, len(t.FunnelSyncs))
			for id := range t.FunnelSyncs {
				funnelIDs = append(funnelIDs, string(id))
			}
			sort.Strings(funnelIDs)
			for _, id := range funnelIDs {
				sync := t.FunnelSyncs[town.FunnelID(id)]
				add(fmt.Sprintf("   source %-14s %-10s %s", id, sync.Provider, sync.Outcome.Kind))
			}
		}
		if r != town.Repo {
			add(fmt.Sprintf(" Configure: bt settings --repo %s --role %s", t.Config.Repo, r))
		}
		tasks := []*town.Task{}
		for _, task := range t.Tasks {
			if task.House == r && task.Stage != "complete" && task.Stage != "closed" && task.Stage != "merged" && task.Stage != "shipped" && task.Stage != "implemented" && task.Stage != "declined" {
				tasks = append(tasks, task)
			}
		}
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].Number < tasks[j].Number })
		for i, task := range tasks {
			if i >= 4 {
				add(fmt.Sprintf("   … %d more queued", len(tasks)-i))
				break
			}
			source := ""
			if task.Source != nil {
				source = fmt.Sprintf(" [%s/%s; %s]", task.Source.Identity.Provider, task.Source.Identity.Funnel, task.Source.Priority.Policy)
			}
			add(fmt.Sprintf("   %-18s %s%s", task.Stage, task.Title, source))
		}
		if len(tasks) == 0 {
			add("   Nothing waiting.")
		}
		add("")
		w := displayWorker(t, r)
		if w.Error != "" {
			add(" ATTENTION: " + w.Error)
		}
		if len(w.Logs) > 0 {
			add(" WORKBENCH")
			start := max(0, len(w.Logs)-3)
			for _, l := range w.Logs[start:] {
				add("   " + l.Text)
			}
		}
		if len(t.Reports) > 0 {
			latest := t.Reports[len(t.Reports)-1]
			add("")
			add(" REPO-BOT: " + latest.Title)
			add(" " + latest.Body)
		}
	}
	if height < 5 {
		return strings.Join(lines[:min(len(lines), height)], "\n")
	}
	for len(lines) < height-3 {
		add("")
	}
	if len(lines) > height-3 {
		lines = lines[:height-3]
	}
	add(message)
	add(strings.Repeat("─", width))
	add(" s start · p pause · x stop · a wake town · d delete · u upgrade · q detach")
	return strings.Join(lines, "\n")
}

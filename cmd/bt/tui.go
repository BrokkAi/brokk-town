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
	snapshots := make(chan town.State, 1)
	messages := make(chan string, 4)
	commands := make(chan map[string]string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			var state town.State
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
	selectedTown, selectedRole := 0, 0
	overview := true
	message := "Connecting to town service…"
	var keys keyDecoder
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case v := <-snapshots:
			state = v
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
					ids := townIDs(state)
					switch key {
					case "q", "ctrl+c":
						return nil
					case "0":
						overview = true
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
					case "1", "2", "3", "4", "5":
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
		frame := renderTUI(state, selectedTown, role, width, height, message)
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
func renderTUI(s town.State, townIndex, roleIndex, width, height int, message string) string {
	width = max(1, width)
	height = max(1, height)
	lines := []string{}
	add := func(text string) { lines = append(lines, cell(text, width)) }
	mode := "LIVE"
	if s.Demo {
		mode = "DEMO · simulated"
	}
	add(" BROKK TOWN                                      " + mode)
	add(strings.Repeat("─", width))
	ids := townIDs(s)
	if len(ids) == 0 {
		add(" No towns yet. Use bt add --repo OWNER/REPO or visit bt web.")
	} else if roleIndex == -1 {
		add(" ALL TOWNS · one repository per town")
		add(" Tab: visit town   1–5: visit selected house   0: overview")
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
				if task.Stage != "closed" && task.Stage != "merged" && task.Stage != "shipped" && task.Stage != "implemented" {
					queued++
				}
			}
			add(" " + t.Config.Repo)
			add(fmt.Sprintf("   %d working · %d queued · %d need attention · %s", busy, queued, blocked, t.LastRelease))
			if t.Error != "" {
				add("   " + t.Error)
			}
			add("")
		}
	} else {
		townIndex = townIndex % len(ids)
		t := s.Towns[ids[townIndex]]
		add(fmt.Sprintf(" %s   [%d/%d towns]   branch %s", t.Config.Repo, townIndex+1, len(ids), t.Config.Branch))
		add(" 0: all towns    Tab: next town    1–5 / j,k: select house")
		add("")
		add("    HOUSE        STATUS       CURRENT WORK")
		for i, r := range town.Roles {
			w := t.Workers[r]
			mark := " "
			if i == roleIndex {
				mark = ">"
			}
			add(fmt.Sprintf(" %s  %-11s %-12s %s", mark, r, w.Status, w.Task))
		}
		add("")
		r := town.Roles[roleIndex]
		add(" AT " + strings.ToUpper(string(r)) + "'S DOOR")
		tasks := []*town.Task{}
		for _, task := range t.Tasks {
			if task.House == r && task.Stage != "closed" && task.Stage != "merged" && task.Stage != "shipped" && task.Stage != "implemented" {
				tasks = append(tasks, task)
			}
		}
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].Number < tasks[j].Number })
		for i, task := range tasks {
			if i >= 4 {
				add(fmt.Sprintf("   … %d more queued", len(tasks)-i))
				break
			}
			add(fmt.Sprintf("   %-18s %s", task.Stage, task.Title))
		}
		if len(tasks) == 0 {
			add("   Nothing waiting.")
		}
		add("")
		w := t.Workers[r]
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
	add(" s start · p pause · x stop · a wake town · q detach (workers continue)")
	return strings.Join(lines, "\n")
}

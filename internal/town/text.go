package town

import "unicode/utf8"

// StateTextLimit bounds every free-text field Town persists. A failing verify
// command or bot process can produce megabytes of captured output; the full text
// belongs in the worker's own log file, not in state.json and not in the public
// snapshot pushed to every connected client on each change.
const StateTextLimit = 4000

// clipText keeps the tail of an over-long value, cut on a rune boundary, because
// the end of command output is the part that explains a failure.
func clipText(v string) string {
	if len(v) <= StateTextLimit {
		return v
	}
	b := v[len(v)-StateTextLimit:]
	for len(b) > 0 && !utf8.RuneStart(b[0]) {
		b = b[1:]
	}
	return "…(earlier text omitted)\n" + b
}

// boundStateText enforces StateTextLimit on every persisted free-text field.
// It runs inside the store transaction so no call site can bypass it, including
// paths that persist a subprocess error verbatim. Audit evidence is deliberately
// untouched: its digests are compared against GitHub.
func boundStateText(t *Town) {
	if t == nil {
		return
	}
	t.Error = clipText(t.Error)
	for _, w := range t.Workers {
		if w == nil {
			continue
		}
		w.Error, w.Task, w.Phase = clipText(w.Error), clipText(w.Task), clipText(w.Phase)
		for i := range w.Logs {
			w.Logs[i].Text = clipText(w.Logs[i].Text)
		}
	}
	for _, task := range t.Tasks {
		if task == nil {
			continue
		}
		task.Detail = clipText(task.Detail)
	}
	for _, intent := range t.Intents {
		if intent != nil {
			intent.Detail = clipText(intent.Detail)
		}
	}
	for i := range t.Outcomes {
		t.Outcomes[i].Detail = clipText(t.Outcomes[i].Detail)
	}
	for i := range t.Reports {
		t.Reports[i].Body = clipText(t.Reports[i].Body)
		t.Reports[i].Title = clipText(t.Reports[i].Title)
	}
}

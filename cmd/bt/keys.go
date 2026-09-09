package main

// Decode split escape sequences and discard bracketed paste, so pasted text
// cannot accidentally act as dashboard shortcuts.
type keyDecoder struct {
	pending string
	paste   bool
}

func (d *keyDecoder) feed(text string) []string {
	d.pending += text
	var keys []string
	for len(d.pending) > 0 {
		if d.pending[0] == '\x1b' {
			if len(d.pending) == 1 {
				break
			}
			if d.pending[1] != '[' && d.pending[1] != 'O' {
				d.pending = d.pending[1:]
				if !d.paste {
					keys = append(keys, "esc")
				}
				continue
			}
			end := 2
			for end < len(d.pending) && (d.pending[end] < 0x40 || d.pending[end] > 0x7e) {
				end++
			}
			if end == len(d.pending) {
				if len(d.pending) > 32 {
					d.pending = ""
				}
				break
			}
			seq := d.pending[:end+1]
			d.pending = d.pending[end+1:]
			if seq == "\x1b[200~" {
				d.paste = true
				continue
			}
			if seq == "\x1b[201~" {
				d.paste = false
				continue
			}
			if d.paste {
				continue
			}
			key := map[string]string{"\x1b[A": "up", "\x1b[B": "down", "\x1bOA": "up", "\x1bOB": "down", "\x1b[5~": "pgup", "\x1b[6~": "pgdown"}[seq]
			if key != "" {
				keys = append(keys, key)
			}
			continue
		}
		key := string(d.pending[0])
		d.pending = d.pending[1:]
		if d.paste {
			continue
		}
		switch key {
		case "\x03":
			key = "ctrl+c"
		case "\r", "\n":
			key = "enter"
		case "\t":
			key = "tab"
		}
		keys = append(keys, key)
	}
	return keys
}

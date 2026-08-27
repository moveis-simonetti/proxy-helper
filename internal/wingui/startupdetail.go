package wingui

import (
	"encoding/json"
	"strings"
)

// summarizeLog picks the part of the daemon's log worth putting in front of
// someone whose proxy did not start.
//
// "O serviço do proxy foi instalado mas não respondeu" is true and useless:
// it names the symptom the person already saw. The daemon writes a
// structured line when it gives up — a port already taken, a config it
// cannot read — and that line is the answer. An empty result means the
// daemon never got far enough to write anything, which is itself worth
// saying rather than inventing a cause.
func summarizeLog(raw string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")

	// Latest first: a daemon that has failed before would otherwise hand
	// back an old failure as if it were this one.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Not JSON: the Go runtime's own panic output lands here, and
			// that is exactly the case worth showing verbatim.
			return truncate(line)
		}
		if entry.Level != "error" && entry.Error == "" {
			continue
		}
		msg := entry.Msg
		if entry.Error != "" {
			if msg != "" {
				msg += ": "
			}
			msg += entry.Error
		}
		return truncate(msg)
	}
	return ""
}

// truncate keeps the message inside a dialog box. The full line stays in the
// log file, which the message points at.
func truncate(s string) string {
	const max = 200
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

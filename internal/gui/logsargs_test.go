package gui

import (
	"fmt"
	"slices"
	"testing"
)

// The original bug: once Limpar recorded a cutoff, the refresh called
// journalctl with --since and WITHOUT -n — the read grew without bound and
// rebuilding the table on every tick froze the UI. The -n cap must be
// present ALWAYS, cutoff or not.
func TestLogsJournalArgsSempreLimitaLinhas(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cutoff string
	}{
		{"sem cutoff", ""},
		{"com cutoff do Limpar", "2026-08-31T17:15:08-03:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := logsJournalArgs(tc.cutoff)
			i := slices.Index(args, "-n")
			if i < 0 || i+1 >= len(args) {
				t.Fatalf("args sem \"-n\": %v", args)
			}
			if got, want := args[i+1], fmt.Sprint(logsBlockLines); got != want {
				t.Errorf("-n %s, esperava -n %s", got, want)
			}
		})
	}
}

func TestLogsJournalArgsCutoff(t *testing.T) {
	if i := slices.Index(logsJournalArgs(""), "--since"); i >= 0 {
		t.Error("sem cutoff não deveria haver --since")
	}
	cutoff := "2026-08-31T17:15:08-03:00"
	args := logsJournalArgs(cutoff)
	i := slices.Index(args, "--since")
	if i < 0 || i+1 >= len(args) || args[i+1] != cutoff {
		t.Errorf("esperava --since %s em %v", cutoff, args)
	}
}

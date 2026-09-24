// logsargs.go is tag-free for the same reason jobs.go is: it imports no GTK,
// so the journalctl argument building stays testable by plain `go test`.
package gui

import (
	"fmt"

	"proxy-helper/internal/serve"
)

// logsBlockLines is how many journal lines the logs block reads, the same
// default `proxy logs --lines` uses. It is deliberately not exposed as a
// GUI setting: the table is a peek at recent traffic, not a log browser —
// `proxy logs` with its flags is the tool for digging.
const logsBlockLines = 200

// logsJournalArgs builds the journalctl invocation for the logs table.
//
// The -n cap is unconditional, ON TOP of any --since. The cutoff recorded by
// Limpar (config's logs_since) never moves forward again, so "--since cutoff"
// alone is a read that grows without bound — days later it was returning
// thousands of entries, and rebuilding a GtkListStore that size on the UI
// thread on every live tick froze the whole window. journalctl applies -n
// after --since, keeping exactly the newest logsBlockLines of the range,
// which is all the table can usefully show anyway.
func logsJournalArgs(cutoff string) []string {
	args := []string{"--user", "-u", serve.UnitName, "-o", "json", "--no-pager"}
	if since := serve.EffectiveSince("", cutoff, false); since != "" {
		args = append(args, "--since", since)
	}
	return append(args, "-n", fmt.Sprint(logsBlockLines))
}

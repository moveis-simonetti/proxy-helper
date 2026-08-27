package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"

	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsLines  int
	logsSince  string
	logsHost   string
	logsErrors bool
	logsDirect bool
	logsJSON   bool
	logsAll    bool
)

var proxyLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show the local proxy's request log",
	Long: "Read the proxy-helper service log from the journal and render it. " +
		"The daemon stores structured JSON, so these filters match on real " +
		"fields rather than on text.",
	RunE: func(cmd *cobra.Command, args []string) error {
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		since := serve.EffectiveSince(logsSince, pf.LogsSince, logsAll)

		// The journal on Unix, the daemon's own file on Windows.
		out, wait, err := serve.OpenLogStream(serve.LogQuery{
			Follow: logsFollow,
			Since:  since,
			Lines:  logsLines,
		})
		if err != nil {
			return err
		}
		defer out.Close()

		if logsJSON {
			if _, err := io.Copy(os.Stdout, out); err != nil {
				return err
			}
			return wait()
		}

		opts := serve.FilterOptions{
			Host:       logsHost,
			ErrorsOnly: logsErrors,
			DirectOnly: logsDirect,
		}

		if logsFollow {
			// "journalctl -f" never reaches EOF, so nothing may be
			// buffered until the end: render each entry as it decodes.
			color := isTerminal(os.Stdout)
			err := serve.StreamEntries(out, func(e serve.LogEntry) error {
				if !serve.Match(e, opts) {
					return nil
				}
				return serve.RenderEntry(os.Stdout, e, color)
			})
			if err != nil {
				return err
			}
			return wait()
		}

		entries, err := serve.ParseEntries(out)
		if err != nil {
			return err
		}
		if err := wait(); err != nil {
			return err
		}

		entries = serve.Filter(entries, opts)

		return serve.RenderEntries(os.Stdout, entries, isTerminal(os.Stdout))
	},
}

// isTerminal reports whether f is a character device, which is when colored
// output is appropriate.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func init() {
	proxyLogsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "keep printing new entries as they arrive")
	proxyLogsCmd.Flags().IntVarP(&logsLines, "lines", "n", 200, "number of entries to show")
	proxyLogsCmd.Flags().StringVar(&logsSince, "since", "", "show entries newer than a duration or timestamp (e.g. 10m, \"2026-08-21 14:00\")")
	proxyLogsCmd.Flags().StringVar(&logsHost, "host", "", "only entries whose destination host contains this string")
	proxyLogsCmd.Flags().BoolVar(&logsErrors, "errors", false, "only failed requests")
	proxyLogsCmd.Flags().BoolVar(&logsDirect, "direct", false, "only requests that bypassed the proxy")
	proxyLogsCmd.Flags().BoolVar(&logsJSON, "json", false, "print raw journal JSON instead of the rendered view")
	proxyLogsCmd.Flags().BoolVar(&logsAll, "all", false, "ignore the cut-off set by \"proxy logs clear\" and show everything the journal still holds")
	proxyLogsCmd.AddCommand(proxyLogsClearCmd)
	proxyCmd.AddCommand(proxyLogsCmd)
}

// proxyLogsClearCmd starts the log view over. It deletes nothing: the
// journal cannot drop one unit's entries (journalctl's --vacuum-* flags act
// on journal FILES and ignore -u), so a real delete would take every user
// unit's logs with it. Recording where to start reading gives the same
// result for this unit without destroying anyone else's data.
var proxyLogsClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Hide entries older than now from \"proxy logs\"",
	Long: "Record the current time as the point \"proxy logs\" starts reading from. " +
		"Nothing is deleted: the journal keeps every entry until its own rotation " +
		"removes it, and \"proxy logs --all\" still shows everything.",
	RunE: func(cmd *cobra.Command, args []string) error {
		now := time.Now().Format(time.RFC3339)
		if err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
			pf.LogsSince = now
			return nil
		}); err != nil {
			return err
		}
		fmt.Fprintf(stdout(), "logs hidden before %s (use \"proxy logs --all\" to see them again)\n", now)
		return nil
	},
}

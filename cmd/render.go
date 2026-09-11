package cmd

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"proxy-helper/internal/app"
)

// renderReport prints a Report the way the CLI always has. The wording lives
// here, in English, because internal/app is shared with the GUI, which speaks
// Portuguese.
func renderReport(w io.Writer, rep *app.Report) {
	// Notices are already on screen: Deps.Notify (see cmd/proxy.go's deps)
	// prints each one at the moment app raises it, interleaved with the
	// executor's own dry-run output and before the sudo prompt it warns
	// about. Printing them again here would duplicate them and put them in
	// the wrong place.
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TARGET\tRESULT")
	for _, r := range rep.Results {
		switch r.Outcome {
		case app.OutcomeFailed:
			fmt.Fprintf(tw, "%s\tFAILED: %v\n", r.Target, r.Err)
		case app.OutcomeSkipped:
			fmt.Fprintf(tw, "%s\tskipped (not available on this system)\n", r.Target)
		case app.OutcomeCleared:
			fmt.Fprintf(tw, "%s\tcleared\n", r.Target)
		default:
			fmt.Fprintf(tw, "%s\tconfigured\n", r.Target)
		}
	}
	tw.Flush()
}

func renderNotice(w io.Writer, n app.Notice) {
	switch n.Kind {
	case app.NoticeUnreachablePassword:
		fmt.Fprintf(w, "  warning: this profile's password comes from %s, which only the local proxy reads.\n", n.Args["source"])
		fmt.Fprintln(w, "           The targets below get a username with no password, so they will fail to authenticate.")
		fmt.Fprintln(w, "           Use --via-local to keep the credential in one place (see \"proxy serve\").")
	case app.NoticeDockerLoopback:
		fmt.Fprintf(w, "  note: %s will point at 127.0.0.1, which containers cannot reach.\n", n.Target)
		fmt.Fprintln(w, "        Pulls will work, but build steps that need the network will fail.")
		fmt.Fprintln(w, "        Run \"proxy serve install --docker-bridge\" to also listen where containers can reach.")
	case app.NoticeDockerNeedsRestart:
		if n.Args["op"] == "apply" {
			fmt.Fprintln(w, "  note: run \"sudo systemctl restart docker\" to apply (not done automatically, it restarts running containers)")
		} else {
			fmt.Fprintln(w, "  note: run \"sudo systemctl restart docker\" to apply")
		}
	case app.NoticeDaemonStranded:
		fmt.Fprintf(w, "  WARNING: every target points at 127.0.0.1:%s, but nothing is listening there.\n", n.Args["port"])
		fmt.Fprintln(w, "           Until the daemon is back, this machine has no network access at all.")
		fmt.Fprintln(w, "           Restart it:  systemctl --user restart proxy-helper.service")
		fmt.Fprintln(w, "           Or take the targets off it:  proxy-helper proxy unset --targets all")
	case app.NoticeDaemonOutdated:
		fmt.Fprintln(w, "  WARNING: the running daemon is an older build than the one installed.")
		fmt.Fprintln(w, "           It ignores the routing mode, so switching it has no effect at all.")
		fmt.Fprintln(w, "           Restart it:  systemctl --user restart proxy-helper.service")
	case app.NoticeNeedsSudo:
		fmt.Fprintf(w, "  note: %s needs sudo, you may be prompted for your password\n", n.Target)
	case app.NoticeProfileAlreadyPlumbed:
		fmt.Fprintf(w, "would enable profile %q (targets already point at the local proxy; nothing to change there)\n", n.Args["profile"])
	}
}

// stdout resolves os.Stdout at call time. captureStdout in proxy_test.go
// replaces os.Stdout itself with a pipe after a command has already started,
// so reading it once and caching would write to the wrong place.
func stdout() io.Writer { return os.Stdout }

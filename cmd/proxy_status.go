package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"

	"github.com/spf13/cobra"
)

var (
	statusTargets []string
	statusYes     bool
	statusNoSudo  bool
)

var proxyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current proxy configuration across targets",
	RunE: func(cmd *cobra.Command, args []string) error {
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		active := daemonActive()
		switch {
		case active && pf.ActiveProfile == "":
			fmt.Printf("daemon: active, no profile selected (everything goes direct)\n\n")
		case active:
			fmt.Printf("daemon: active (profile %q)\n\n", pf.ActiveProfile)
		case pf.ViaLocal:
			fmt.Printf("daemon: INACTIVE - targets point at 127.0.0.1:%d and will fail; run \"systemctl --user start %s\"\n\n",
				pf.EffectiveLocalPort(), serve.UnitName)
		default:
			fmt.Printf("daemon: not in use\n\n")
		}

		ex := &proxy.Executor{}
		results, err := app.Collect(deps(), ex, statusTargets, false)
		if err != nil {
			return err
		}

		if app.NeedsElevation(results) && !proxy.IsRoot() && !statusNoSudo {
			var names []string
			for _, st := range results {
				if st.NeedsElevation {
					names = append(names, st.Name)
				}
			}
			if statusYes || confirmSudo(names) {
				results, err = app.Elevate(deps(), ex, results)
				if err != nil {
					return err
				}
			}
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "TARGET\tAVAILABLE\tENABLED\tDETAIL")
		for _, st := range results {
			fmt.Fprintf(w, "%s\t%v\t%v\t%s\n", st.Name, st.Available, st.Enabled, st.Detail)
		}
		return w.Flush()
	},
}

// confirmSudo asks once whether to retry the given targets with sudo. It
// defaults to "no" (rather than hanging) when stdin isn't an interactive
// terminal, e.g. when piped or run from a script/CI.
func confirmSudo(targetNames []string) bool {
	if !isInteractive() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s need sudo for an accurate status. Run with sudo now? [Y/n] ", strings.Join(targetNames, ", "))
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "" || line == "y" || line == "yes"
}

func isInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func init() {
	proxyStatusCmd.Flags().StringSliceVar(&statusTargets, "targets", []string{"all"}, "comma-separated targets (shell,session-env,system-env,git,npm,vscode,gnome,kde,dockerd,docker-config,lxd,snap,apt,all)")
	proxyStatusCmd.Flags().BoolVarP(&statusYes, "yes", "y", false, "don't ask, immediately elevate via sudo for targets that need it")
	proxyStatusCmd.Flags().BoolVar(&statusNoSudo, "no-sudo", false, "never elevate; show \"requires sudo to check\" instead of prompting")
	proxyCmd.AddCommand(proxyStatusCmd)
}

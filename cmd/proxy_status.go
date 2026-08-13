package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"proxy-helper/internal/proxy"

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
		targets, err := proxy.ByNames(statusTargets)
		if err != nil {
			return err
		}

		results := make([]proxy.Status, len(targets))
		var needsElevation []int
		for i, t := range targets {
			st, err := t.Status(false)
			if err != nil {
				st = proxy.Status{Name: t.Name(), Detail: fmt.Sprintf("error: %v", err)}
			}
			results[i] = st
			if st.NeedsElevation {
				needsElevation = append(needsElevation, i)
			}
		}

		if len(needsElevation) > 0 && !proxy.IsRoot() && !statusNoSudo {
			names := make([]string, len(needsElevation))
			for j, i := range needsElevation {
				names[j] = targets[i].Name()
			}
			if statusYes || confirmSudo(names) {
				for _, i := range needsElevation {
					st, err := targets[i].Status(true)
					if err != nil {
						st = proxy.Status{Name: targets[i].Name(), Detail: fmt.Sprintf("error: %v", err)}
					}
					results[i] = st
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
	proxyStatusCmd.Flags().StringSliceVar(&statusTargets, "targets", []string{"all"}, "comma-separated targets (shell,git,npm,gnome,dockerd,docker-config,snap,apt,all)")
	proxyStatusCmd.Flags().BoolVarP(&statusYes, "yes", "y", false, "don't ask, immediately elevate via sudo for targets that need it")
	proxyStatusCmd.Flags().BoolVar(&statusNoSudo, "no-sudo", false, "never elevate; show \"requires sudo to check\" instead of prompting")
	proxyCmd.AddCommand(proxyStatusCmd)
}

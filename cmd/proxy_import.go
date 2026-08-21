package cmd

import (
	"fmt"

	"proxy-helper/internal/proxy"

	"github.com/spf13/cobra"
)

var (
	importUser        string
	importPass        string
	importNoProxy     []string
	importIndex       int
	importTargets     []string
	importDryRun      bool
	importSaveProfile string
)

var proxyImportCmd = &cobra.Command{
	Use:   "import <pac-url>",
	Short: "Import proxy settings from a PAC (proxy auto-config) URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pacURL := args[0]

		entries, err := proxy.FetchPAC(pacURL)
		if err != nil {
			return err
		}

		idx := 0
		if len(entries) > 1 {
			if !cmd.Flags().Changed("index") {
				fmt.Printf("multiple proxy entries found in %s:\n", pacURL)
				for i, e := range entries {
					fmt.Printf("  [%d] %s\n", i, e)
				}
				return fmt.Errorf("multiple entries found, pick one with --index N")
			}
			idx = importIndex
		}
		if idx < 0 || idx >= len(entries) {
			return fmt.Errorf("--index %d out of range (found %d entries)", idx, len(entries))
		}
		chosen := entries[idx]
		fmt.Printf("using %s\n", chosen)

		cfg := proxy.Config{
			Scheme:   chosen.Scheme,
			Host:     chosen.Host,
			Port:     chosen.Port,
			Username: importUser,
			Password: importPass,
			NoProxy:  importNoProxy,
		}

		if importSaveProfile != "" {
			pf, err := proxy.LoadProfiles()
			if err != nil {
				return err
			}
			if _, exists := pf.Get(importSaveProfile); exists {
				return fmt.Errorf("profile %q already exists (use \"proxy profile edit\" to change it)", importSaveProfile)
			}
			pf.Profiles[importSaveProfile] = cfg
			if err := pf.Save(); err != nil {
				return err
			}
			fmt.Printf("profile %q saved\n", importSaveProfile)
			return nil
		}

		return applyConfig(cfg, importTargets, importDryRun, false)
	},
}

func init() {
	proxyImportCmd.Flags().StringVar(&importUser, "user", "", "proxy username")
	proxyImportCmd.Flags().StringVar(&importPass, "pass", "", "proxy password")
	proxyImportCmd.Flags().StringSliceVar(&importNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy, in addition to the global list (see \"proxy config\")")
	proxyImportCmd.Flags().IntVar(&importIndex, "index", 0, "which PAC proxy entry to use, when the file lists more than one")
	proxyImportCmd.Flags().StringVar(&importSaveProfile, "save-profile", "", "save the imported config as a named profile instead of applying it")
	proxyImportCmd.Flags().StringSliceVar(&importTargets, "targets", []string{"all"}, "comma-separated targets (shell,git,npm,vscode,gnome,kde,dockerd,docker-config,lxd,snap,apt,all)")
	proxyImportCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "print what would change without applying it")
	proxyCmd.AddCommand(proxyImportCmd)
}

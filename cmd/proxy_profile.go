package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"proxy-helper/internal/proxy"

	"github.com/spf13/cobra"
)

var proxyProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage saved proxy profiles",
}

// --- add ---

var (
	profileAddScheme  string
	profileAddHost    string
	profileAddPort    string
	profileAddUser    string
	profileAddPass    string
	profileAddNoProxy []string
)

var proxyProfileAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Save a new proxy profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if profileAddHost == "" {
			return fmt.Errorf("--host is required")
		}

		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		if _, exists := pf.Get(name); exists {
			return fmt.Errorf("profile %q already exists (use \"proxy profile edit\" to change it)", name)
		}

		pf.Profiles[name] = proxy.Config{
			Scheme:   profileAddScheme,
			Host:     profileAddHost,
			Port:     profileAddPort,
			Username: profileAddUser,
			Password: profileAddPass,
			NoProxy:  profileAddNoProxy,
		}
		if err := pf.Save(); err != nil {
			return err
		}
		fmt.Printf("profile %q saved\n", name)
		return nil
	},
}

// --- edit ---

var (
	profileEditScheme  string
	profileEditHost    string
	profileEditPort    string
	profileEditUser    string
	profileEditPass    string
	profileEditNoProxy []string
)

var proxyProfileEditCmd = &cobra.Command{
	Use:   "edit <name>",
	Short: "Update fields of an existing proxy profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		cfg, exists := pf.Get(name)
		if !exists {
			return fmt.Errorf("profile %q not found (see \"proxy profile list\")", name)
		}

		if cmd.Flags().Changed("scheme") {
			cfg.Scheme = profileEditScheme
		}
		if cmd.Flags().Changed("host") {
			cfg.Host = profileEditHost
		}
		if cmd.Flags().Changed("port") {
			cfg.Port = profileEditPort
		}
		if cmd.Flags().Changed("user") {
			cfg.Username = profileEditUser
		}
		if cmd.Flags().Changed("pass") {
			cfg.Password = profileEditPass
		}
		if cmd.Flags().Changed("no-proxy") {
			cfg.NoProxy = profileEditNoProxy
		}

		pf.Profiles[name] = cfg
		if err := pf.Save(); err != nil {
			return err
		}
		fmt.Printf("profile %q updated\n", name)
		return nil
	},
}

// --- remove ---

var proxyProfileRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Delete a saved proxy profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		if _, exists := pf.Get(name); !exists {
			return fmt.Errorf("profile %q not found (see \"proxy profile list\")", name)
		}

		delete(pf.Profiles, name)
		if pf.ActiveProfile == name {
			pf.ActiveProfile = ""
		}
		if err := pf.Save(); err != nil {
			return err
		}
		fmt.Printf("profile %q removed\n", name)
		return nil
	},
}

// --- list ---

var proxyProfileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved proxy profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}

		names := make([]string, 0, len(pf.Profiles))
		for name := range pf.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)

		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSCHEME\tHOST\tPORT\tENABLED")
		for _, name := range names {
			cfg := pf.Profiles[name]
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\n", name, cfg.Scheme, cfg.Host, cfg.Port, name == pf.ActiveProfile)
		}
		return w.Flush()
	},
}

// --- enable ---

var (
	profileEnableTargets []string
	profileEnableDryRun  bool
)

var proxyProfileEnableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Apply a saved profile's proxy settings and mark it active",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		cfg, exists := pf.Get(name)
		if !exists {
			return fmt.Errorf("profile %q not found (see \"proxy profile list\")", name)
		}

		if err := applyConfig(cfg, profileEnableTargets, profileEnableDryRun); err != nil {
			return err
		}
		if profileEnableDryRun {
			return nil
		}

		pf.ActiveProfile = name
		return pf.Save()
	},
}

// --- disable ---

var (
	profileDisableTargets []string
	profileDisableDryRun  bool
)

var proxyProfileDisableCmd = &cobra.Command{
	Use:   "disable [name]",
	Short: "Clear the active profile's proxy settings",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		if pf.ActiveProfile == "" {
			return fmt.Errorf("no profile is currently enabled")
		}
		if len(args) == 1 && args[0] != pf.ActiveProfile {
			return fmt.Errorf("profile %q is not the active one (active: %q)", args[0], pf.ActiveProfile)
		}

		if err := clearTargets(profileDisableTargets, profileDisableDryRun); err != nil {
			return err
		}
		if profileDisableDryRun {
			return nil
		}

		pf.ActiveProfile = ""
		return pf.Save()
	},
}

func init() {
	proxyProfileAddCmd.Flags().StringVar(&profileAddScheme, "scheme", "http", "proxy scheme: http, https, socks5")
	proxyProfileAddCmd.Flags().StringVar(&profileAddHost, "host", "", "proxy host (required)")
	proxyProfileAddCmd.Flags().StringVar(&profileAddPort, "port", "", "proxy port")
	proxyProfileAddCmd.Flags().StringVar(&profileAddUser, "user", "", "proxy username")
	proxyProfileAddCmd.Flags().StringVar(&profileAddPass, "pass", "", "proxy password")
	proxyProfileAddCmd.Flags().StringSliceVar(&profileAddNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy")

	proxyProfileEditCmd.Flags().StringVar(&profileEditScheme, "scheme", "", "proxy scheme: http, https, socks5")
	proxyProfileEditCmd.Flags().StringVar(&profileEditHost, "host", "", "proxy host")
	proxyProfileEditCmd.Flags().StringVar(&profileEditPort, "port", "", "proxy port")
	proxyProfileEditCmd.Flags().StringVar(&profileEditUser, "user", "", "proxy username")
	proxyProfileEditCmd.Flags().StringVar(&profileEditPass, "pass", "", "proxy password")
	proxyProfileEditCmd.Flags().StringSliceVar(&profileEditNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy")

	proxyProfileEnableCmd.Flags().StringSliceVar(&profileEnableTargets, "targets", []string{"all"}, "comma-separated targets (shell,git,npm,gnome,dockerd,docker-config,snap,apt,all)")
	proxyProfileEnableCmd.Flags().BoolVar(&profileEnableDryRun, "dry-run", false, "print what would change without applying it")

	proxyProfileDisableCmd.Flags().StringSliceVar(&profileDisableTargets, "targets", []string{"all"}, "comma-separated targets (shell,git,npm,gnome,dockerd,docker-config,snap,apt,all)")
	proxyProfileDisableCmd.Flags().BoolVar(&profileDisableDryRun, "dry-run", false, "print what would change without applying it")

	proxyProfileCmd.AddCommand(proxyProfileAddCmd, proxyProfileEditCmd, proxyProfileRemoveCmd, proxyProfileListCmd, proxyProfileEnableCmd, proxyProfileDisableCmd)
	proxyCmd.AddCommand(proxyProfileCmd)
}

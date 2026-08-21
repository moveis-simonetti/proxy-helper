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

// refuseReserved blocks the reserved "_current" slot from being managed like
// a user profile. It belongs to "proxy set --via-local", which rewrites it
// wholesale, so editing or deleting it by hand only creates confusion.
func refuseReserved(name, verb string) error {
	if name != proxy.CurrentProfileName {
		return nil
	}
	return fmt.Errorf("%q is reserved for \"proxy set --via-local\" and cannot be %s; save a named profile instead", name, verb)
}

// visibleProfileNames returns the profiles a user should see, sorted. The
// reserved slot is an implementation detail of "proxy set --via-local", not
// something the user saved, so it stays out of the listing.
func visibleProfileNames(pf *proxy.ProfileFile) []string {
	names := make([]string, 0, len(pf.Profiles))
	for name := range pf.Profiles {
		if name == proxy.CurrentProfileName {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
		if err := refuseReserved(name, "added"); err != nil {
			return err
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
		if err := refuseReserved(name, "edited"); err != nil {
			return err
		}

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
		if err := refuseReserved(name, "removed"); err != nil {
			return err
		}

		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		if _, exists := pf.Get(name); !exists {
			return fmt.Errorf("profile %q not found (see \"proxy profile list\")", name)
		}

		delete(pf.Profiles, name)
		// Both pointers have to let go of a profile that no longer exists:
		// a dangling last_profile makes "proxy on" fail with a name the
		// user can no longer see in "proxy profile list".
		wasActive := pf.ActiveProfile == name
		if wasActive {
			pf.ActiveProfile = ""
		}
		if pf.LastProfile == name {
			pf.LastProfile = ""
		}
		if err := pf.Save(); err != nil {
			return err
		}
		// Removing the profile the daemon is serving changes where traffic
		// goes, so it has to hear about it — otherwise it keeps proxying
		// through an upstream the user just deleted.
		if wasActive {
			if err := reloadDaemon(&proxy.Executor{}); err != nil {
				return err
			}
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

		names := visibleProfileNames(pf)

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
	profileEnableTargets  []string
	profileEnableDryRun   bool
	profileEnableViaLocal bool
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

		if profileEnableViaLocal {
			// Build the plumbing for a *named* profile: active_profile
			// gets the name (not the reserved "_current" copy, which
			// would go stale the moment the profile is edited), and the
			// targets only ever see the loopback address.
			cfg.NoProxy = proxy.MergeNoProxy(pf.EffectiveGlobalNoProxy(), cfg.NoProxy)
			pf.ActiveProfile = name
			return applyViaLocal(pf, cfg, profileEnableTargets, profileEnableDryRun)
		}

		if pf.ViaLocal {
			// The plumbing is already in place: the targets point at the
			// daemon, so switching profiles is pure state. Touching a
			// target here would tear the plumbing down and write the
			// upstream credential into every tool's config file.
			if profileEnableDryRun {
				fmt.Printf("would enable profile %q (targets already point at the local proxy; nothing to change there)\n", name)
				return nil
			}
			pf.ActiveProfile = name
			if err := pf.Save(); err != nil {
				return err
			}
			if err := reloadDaemon(&proxy.Executor{}); err != nil {
				return err
			}
			fmt.Printf("profile %q enabled\n", name)
			return nil
		}

		// Record the activation *before* touching any target. Targets fail
		// for mundane reasons — a tool with an unparsable config file, a
		// declined sudo prompt — and returning early used to leave the
		// machine with every target configured but no active profile: the
		// partial state this tool exists to prevent.
		if !profileEnableDryRun {
			pf.ActiveProfile = name
			if err := pf.Save(); err != nil {
				return err
			}
			if err := reloadDaemon(&proxy.Executor{}); err != nil {
				return err
			}
		}
		return applyConfig(cfg, profileEnableTargets, profileEnableDryRun, false)
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

		// Off, not a bare assignment: it remembers the profile so that
		// "proxy on" works after a disable exactly as it does after an
		// "proxy off".
		pf.Off()
		if err := pf.Save(); err != nil {
			return err
		}
		return reloadDaemon(&proxy.Executor{})
	},
}

func init() {
	proxyProfileAddCmd.Flags().StringVar(&profileAddScheme, "scheme", "http", "proxy scheme: http, https, socks5")
	proxyProfileAddCmd.Flags().StringVar(&profileAddHost, "host", "", "proxy host (required)")
	proxyProfileAddCmd.Flags().StringVar(&profileAddPort, "port", "", "proxy port")
	proxyProfileAddCmd.Flags().StringVar(&profileAddUser, "user", "", "proxy username")
	proxyProfileAddCmd.Flags().StringVar(&profileAddPass, "pass", "", "proxy password")
	proxyProfileAddCmd.Flags().StringSliceVar(&profileAddNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy, in addition to the global list (see \"proxy config\")")

	proxyProfileEditCmd.Flags().StringVar(&profileEditScheme, "scheme", "", "proxy scheme: http, https, socks5")
	proxyProfileEditCmd.Flags().StringVar(&profileEditHost, "host", "", "proxy host")
	proxyProfileEditCmd.Flags().StringVar(&profileEditPort, "port", "", "proxy port")
	proxyProfileEditCmd.Flags().StringVar(&profileEditUser, "user", "", "proxy username")
	proxyProfileEditCmd.Flags().StringVar(&profileEditPass, "pass", "", "proxy password")
	proxyProfileEditCmd.Flags().StringSliceVar(&profileEditNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy, in addition to the global list (see \"proxy config\")")

	proxyProfileEnableCmd.Flags().StringSliceVar(&profileEnableTargets, "targets", []string{"all"}, "comma-separated targets (shell,git,npm,vscode,gnome,kde,dockerd,docker-config,lxd,snap,apt,all)")
	proxyProfileEnableCmd.Flags().BoolVar(&profileEnableDryRun, "dry-run", false, "print what would change without applying it")
	proxyProfileEnableCmd.Flags().BoolVar(&profileEnableViaLocal, "via-local", false, "point targets at the local proxy (see \"proxy serve\") instead of writing the upstream and its credentials into every tool's config")

	proxyProfileDisableCmd.Flags().StringSliceVar(&profileDisableTargets, "targets", []string{"all"}, "comma-separated targets (shell,git,npm,vscode,gnome,kde,dockerd,docker-config,lxd,snap,apt,all)")
	proxyProfileDisableCmd.Flags().BoolVar(&profileDisableDryRun, "dry-run", false, "print what would change without applying it")

	proxyProfileCmd.AddCommand(proxyProfileAddCmd, proxyProfileEditCmd, proxyProfileRemoveCmd, proxyProfileListCmd, proxyProfileEnableCmd, proxyProfileDisableCmd)
	proxyCmd.AddCommand(proxyProfileCmd)
}

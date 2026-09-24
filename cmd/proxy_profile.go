package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"

	"github.com/spf13/cobra"
)

var proxyProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage saved proxy profiles",
}

// refuseReserved blocks the reserved "_current" slot from being managed like
// a user profile. It belongs to ad-hoc "proxy set --host ..." runs, which
// rewrite it wholesale, so editing or deleting it by hand only creates
// confusion.
func refuseReserved(name, verb string) error {
	if name != proxy.CurrentProfileName {
		return nil
	}
	return fmt.Errorf("%q is reserved for ad-hoc \"proxy set --host ...\" runs and cannot be %s; save a named profile instead", name, verb)
}

// visibleProfileNames returns the profiles a user should see, sorted. The
// reserved slot is an implementation detail of ad-hoc "proxy set --host ..."
// runs, not something the user saved, so it stays out of the listing.
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
	profileAddScheme            string
	profileAddHost              string
	profileAddPort              string
	profileAddUser              string
	profileAddPass              string
	profileAddNoProxy           []string
	profileAddConnCheckURL      string
	profileAddConnCheckResponse string
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

		err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
			if _, exists := pf.Get(name); exists {
				return fmt.Errorf("profile %q already exists (use \"proxy profile edit\" to change it)", name)
			}
			pf.Profiles[name] = proxy.Config{
				Scheme:                    profileAddScheme,
				Host:                      profileAddHost,
				Port:                      profileAddPort,
				Username:                  profileAddUser,
				Password:                  profileAddPass,
				NoProxy:                   profileAddNoProxy,
				ConnectivityCheckURL:      profileAddConnCheckURL,
				ConnectivityCheckResponse: profileAddConnCheckResponse,
			}
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Printf("profile %q saved\n", name)
		return nil
	},
}

// --- edit ---

var (
	profileEditScheme            string
	profileEditHost              string
	profileEditPort              string
	profileEditUser              string
	profileEditPass              string
	profileEditNoProxy           []string
	profileEditConnCheckURL      string
	profileEditConnCheckResponse string
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

		// app.SaveProfile, not a bare WithProfileLock: editing the active
		// profile has to reach a running daemon, which resolves that
		// profile's host, port, credentials and no-proxy itself.
		err := app.SaveProfile(deps(), &proxy.Executor{}, func(existing map[string]proxy.Config) (string, proxy.Config, error) {
			cfg, exists := existing[name]
			if !exists {
				return "", proxy.Config{}, fmt.Errorf("profile %q not found (see \"proxy profile list\")", name)
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
			if cmd.Flags().Changed("connectivity-check-url") {
				cfg.ConnectivityCheckURL = profileEditConnCheckURL
			}
			if cmd.Flags().Changed("connectivity-check-response") {
				cfg.ConnectivityCheckResponse = profileEditConnCheckResponse
			}

			return name, cfg, nil
		})
		if err != nil {
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

		var wasActive bool
		err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
			if _, exists := pf.Get(name); !exists {
				return fmt.Errorf("profile %q not found (see \"proxy profile list\")", name)
			}

			delete(pf.Profiles, name)
			// Both pointers have to let go of a profile that no longer
			// exists: a dangling last_profile makes "proxy on" fail with a
			// name the user can no longer see in "proxy profile list".
			wasActive = pf.ActiveProfile == name
			if wasActive {
				pf.ActiveProfile = ""
				// A forwarding mode with nothing selected is a state the
				// daemon cannot honour; drop to direct along with it.
				pf.Mode = string(proxy.ModeDirect)
			}
			if pf.LastProfile == name {
				pf.LastProfile = ""
			}
			return nil
		})
		if err != nil {
			return err
		}
		// Removing the profile the daemon is serving changes where traffic
		// goes, so it has to hear about it — otherwise it keeps proxying
		// through an upstream the user just deleted. This runs after the
		// lock above is released: reloadDaemon never touches the profile
		// file, so there is nothing to nest.
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
	profileEnableTargets    []string
	profileEnableDryRun     bool
	profileEnableNoViaLocal bool
)

var proxyProfileEnableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Apply a saved profile's proxy settings and mark it active",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		viaLocal := !profileEnableNoViaLocal
		if viaLocal {
			// Only force Route 1 (rewrite every target, sudo included) when
			// the plumbing is not already up. Once it is, forcing Route 1 on
			// every plain "profile enable" would turn the daily, sudo-free
			// profile switch into a full privileged rewrite — Route 2 exists
			// precisely to avoid that. See app.Enable's doc comment: passing
			// viaLocal=true always takes Route 1; viaLocal=false lets
			// pf.ViaLocal itself pick between Route 2 (already plumbed) and
			// Route 3 (the --no-via-local escape hatch, untouched here).
			pf, err := proxy.LoadProfiles()
			if err != nil {
				return err
			}
			viaLocal = !pf.ViaLocal
		}
		ex := &proxy.Executor{DryRun: profileEnableDryRun}
		res, err := app.Enable(deps(), ex, args[0], profileEnableTargets, viaLocal)
		if err != nil {
			return err
		}
		if res.Report != nil {
			renderReport(stdout(), res.Report)
		}
		if res.TargetsUntouched {
			if !profileEnableDryRun {
				fmt.Printf("profile %q enabled\n", args[0])
			}
			return nil
		}
		if res.Report != nil {
			return res.Report.Err()
		}
		return nil
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
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		ex := &proxy.Executor{DryRun: profileDisableDryRun}
		res, err := app.Disable(deps(), ex, name, profileDisableTargets)
		if err != nil {
			return err
		}
		if res.Report != nil {
			renderReport(stdout(), res.Report)
		}
		if res.TargetsUntouched {
			// Mirrors the enable side: nothing was written, so a result
			// table would be an empty header. Say what happened instead.
			fmt.Println("routing direct; no target was touched")
			return nil
		}
		return res.Report.Err()
	},
}

func init() {
	proxyProfileAddCmd.Flags().StringVar(&profileAddScheme, "scheme", "http", "proxy scheme: http, https, socks5")
	proxyProfileAddCmd.Flags().StringVar(&profileAddHost, "host", "", "proxy host (required)")
	proxyProfileAddCmd.Flags().StringVar(&profileAddPort, "port", "", "proxy port")
	proxyProfileAddCmd.Flags().StringVar(&profileAddUser, "user", "", "proxy username")
	proxyProfileAddCmd.Flags().StringVar(&profileAddPass, "pass", "", "proxy password")
	proxyProfileAddCmd.Flags().StringSliceVar(&profileAddNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy, in addition to the global list (see \"proxy config\")")
	proxyProfileAddCmd.Flags().StringVar(&profileAddConnCheckURL, "connectivity-check-url", "", "point NetworkManager's own connectivity check at a URL this network answers without a proxy (see \"nm-connectivity\" target); leave unset to not touch it")
	proxyProfileAddCmd.Flags().StringVar(&profileAddConnCheckResponse, "connectivity-check-response", "", "text the connectivity check URL's response body must start with (required for --connectivity-check-url to actually detect success)")

	proxyProfileEditCmd.Flags().StringVar(&profileEditScheme, "scheme", "", "proxy scheme: http, https, socks5")
	proxyProfileEditCmd.Flags().StringVar(&profileEditHost, "host", "", "proxy host")
	proxyProfileEditCmd.Flags().StringVar(&profileEditPort, "port", "", "proxy port")
	proxyProfileEditCmd.Flags().StringVar(&profileEditUser, "user", "", "proxy username")
	proxyProfileEditCmd.Flags().StringVar(&profileEditPass, "pass", "", "proxy password")
	proxyProfileEditCmd.Flags().StringSliceVar(&profileEditNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy, in addition to the global list (see \"proxy config\")")
	proxyProfileEditCmd.Flags().StringVar(&profileEditConnCheckURL, "connectivity-check-url", "", "point NetworkManager's own connectivity check at a URL this network answers without a proxy; empty clears it")
	proxyProfileEditCmd.Flags().StringVar(&profileEditConnCheckResponse, "connectivity-check-response", "", "text the connectivity check URL's response body must start with")

	proxyProfileEnableCmd.Flags().StringSliceVar(&profileEnableTargets, "targets", []string{"all"}, "comma-separated targets (shell,session-env,system-env,git,npm,vscode,gnome,kde,dockerd,docker-config,lxd,snap,apt,nm-connectivity,all)")
	proxyProfileEnableCmd.Flags().BoolVar(&profileEnableDryRun, "dry-run", false, "print what would change without applying it")
	proxyProfileEnableCmd.Flags().BoolVar(&profileEnableNoViaLocal, "no-via-local", false, "write the upstream and its credentials straight into every tool's config, instead of pointing them at the local proxy (see \"proxy serve\")")

	proxyProfileDisableCmd.Flags().StringSliceVar(&profileDisableTargets, "targets", []string{"all"}, "comma-separated targets (shell,session-env,system-env,git,npm,vscode,gnome,kde,dockerd,docker-config,lxd,snap,apt,nm-connectivity,all)")
	proxyProfileDisableCmd.Flags().BoolVar(&profileDisableDryRun, "dry-run", false, "print what would change without applying it")

	proxyProfileCmd.AddCommand(proxyProfileAddCmd, proxyProfileEditCmd, proxyProfileRemoveCmd, proxyProfileListCmd, proxyProfileEnableCmd, proxyProfileDisableCmd)
	proxyCmd.AddCommand(proxyProfileCmd)
}

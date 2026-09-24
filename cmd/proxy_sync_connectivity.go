package cmd

import (
	"time"

	"proxy-helper/internal/proxy"

	"github.com/spf13/cobra"
)

// connectivityProbeTimeout bounds how long a NetworkManager dispatcher
// event waits on this: NM runs dispatcher scripts synchronously and other
// scripts may be queued behind this one, so it must return quickly even on
// an address that is not merely down but silently dropping packets.
const connectivityProbeTimeout = 3 * time.Second

var proxySyncConnectivityCmd = &cobra.Command{
	Use:    "sync-connectivity",
	Hidden: true,
	Short:  "Internal: reconcile NetworkManager's connectivity check with live reachability",
	Long: "Not meant to be run by hand. This is what the dispatcher script " +
		"\"nm-connectivity\" installs (see \"proxy profile edit " +
		"--connectivity-check-url\") invokes on every network change: it " +
		"re-probes the active profile's connectivity check URL for real and " +
		"toggles NetworkManager's override to match, instead of leaving it " +
		"stuck pointing at whichever network it was configured on.",
	RunE: func(cmd *cobra.Command, args []string) error {
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		// Direct mode already has nothing proxied, and going direct already
		// cleared this override (see app.syncConnectivityCheck) — nothing
		// for a network change to reconcile.
		if !pf.EffectiveMode().Forwards() {
			return nil
		}
		cfg, ok := pf.Get(pf.ActiveProfile)
		if !ok || cfg.ConnectivityCheckURL == "" {
			return nil
		}
		reachable := proxy.ProbeConnectivityCheck(cfg.ConnectivityCheckURL, cfg.ConnectivityCheckResponse, connectivityProbeTimeout)
		return proxy.SyncNMConnectivity(&proxy.Executor{}, cfg, reachable)
	},
}

func init() {
	proxyCmd.AddCommand(proxySyncConnectivityCmd)
}

package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"

	"github.com/spf13/cobra"
)

var (
	servePort         int
	serveQuiet        bool
	serveDockerBridge bool
	serveInstallDry   bool
	serveUninstallDry bool
)

var proxyServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the local forward proxy that other targets point at",
	Long: "Run a forward proxy on 127.0.0.1 that chains to the active profile's " +
		"upstream. Targets configured with --via-local point here, so credentials " +
		"stay in one place and switching profiles needs no reconfiguration.\n\n" +
		"This runs in the foreground; use \"proxy serve install\" to run it as a " +
		"systemd user service.",
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := serve.NewLogger(os.Stdout, serveQuiet)

		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		// Without an explicit --port, listen where the targets were told
		// to look. Guessing 8888 here would silently strand a daemon
		// installed on another port.
		if !cmd.Flags().Changed("port") {
			servePort = pf.EffectiveLocalPort()
		}

		state, err := serve.NewState(logger)
		if err != nil {
			return err
		}

		// The flag is an opt-in on top of whatever was installed: a unit
		// written with --docker-bridge carries the flag in ExecStart, and a
		// foreground run can ask for it explicitly.
		dockerBridge := serveDockerBridge || pf.DockerBridge
		addrs, err := serve.ListenAddrs(servePort, dockerBridge)
		if err != nil {
			return err
		}
		srv := &http.Server{
			Handler:           serve.NewServer(state, logger).Handler(),
			ReadHeaderTimeout: 20 * time.Second,
		}

		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		go func() {
			for range hup {
				// A failed reload keeps the previous state; State logs it.
				_ = state.Reload()
			}
		}()

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-stop
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		}()

		// One Server, one listener per address: loopback always, plus the
		// Docker bridge when asked.
		var listeners []net.Listener
		for _, a := range addrs {
			ln, err := net.Listen("tcp", a)
			if err != nil {
				for _, open := range listeners {
					open.Close()
				}
				return fmt.Errorf("listening on %s: %w", a, err)
			}
			listeners = append(listeners, ln)
		}

		logger.Info("startup", "addrs", strings.Join(addrs, ","), "upstream", state.Describe())
		if dockerBridge {
			logger.Warn("docker_bridge_enabled",
				"detail", "every container on this machine can use this proxy; it authenticates no client")
		}

		errCh := make(chan error, len(listeners))
		for _, ln := range listeners {
			go func(ln net.Listener) { errCh <- srv.Serve(ln) }(ln)
		}
		for range listeners {
			if err := <-errCh; err != nil && err != http.ErrServerClosed {
				return err
			}
		}
		return nil
	},
}

var proxyServeInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install and start the proxy-helper systemd user service",
	RunE: func(cmd *cobra.Command, args []string) error {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolving own path: %w", err)
		}

		// Record the port before installing: "proxy set --via-local" and
		// "proxy status" read it from here, so a non-default --port has to
		// be persisted or the targets would be pointed at the wrong port.
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		if pf.LocalPort != servePort && !serveInstallDry {
			pf.LocalPort = servePort
			if err := pf.Save(); err != nil {
				return err
			}
		}

		ex := &proxy.Executor{DryRun: serveInstallDry}
		if serveDockerBridge {
			if _, err := serve.DockerBridgeAddr(); err != nil {
				return fmt.Errorf("--docker-bridge: %w", err)
			}
			if pf.DockerBridge != true && !serveInstallDry {
				pf.DockerBridge = true
				if err := pf.Save(); err != nil {
					return err
				}
			}
		}
		if err := serve.InstallUnit(ex, execPath, servePort, serveDockerBridge || pf.DockerBridge); err != nil {
			return err
		}
		if !serveInstallDry {
			addrs, err := serve.ListenAddrs(servePort, serveDockerBridge || pf.DockerBridge)
			if err != nil {
				return err
			}
			fmt.Printf("installed and started %s on %s\n", serve.UnitName, strings.Join(addrs, ", "))
			fmt.Printf("next: %s\n", nextStepHint(pf))
		}
		return nil
	},
}

// nextStepHint spells out the command that actually works from here. The
// generic "run proxy set --via-local" it used to print is not a runnable
// command: set needs to know which proxy to serve, so it wants either a
// profile or an explicit host.
func nextStepHint(pf *proxy.ProfileFile) string {
	if pf.ViaLocal {
		return "your targets already point at the local proxy; nothing else to do"
	}

	names := visibleProfileNames(pf)
	switch len(names) {
	case 0:
		return "save a profile with \"proxy profile add <name> --host <host> --port <port>\", " +
			"then run \"proxy set --profile <name> --via-local\" to point your targets at it"
	case 1:
		return fmt.Sprintf("run \"proxy set --profile %s --via-local\" to point your targets at it", names[0])
	default:
		return fmt.Sprintf("run \"proxy set --profile <name> --via-local\" to point your targets at it (profiles: %s)",
			strings.Join(names, ", "))
	}
}

var proxyServeUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop and remove the proxy-helper systemd user service",
	RunE: func(cmd *cobra.Command, args []string) error {
		ex := &proxy.Executor{DryRun: serveUninstallDry}
		if err := serve.UninstallUnit(ex); err != nil {
			return err
		}
		if !serveUninstallDry {
			fmt.Println("removed " + serve.UnitName)
			fmt.Println("note: targets configured with --via-local still point at the local proxy; run \"proxy set\" or \"proxy unset\" to change them")
		}
		return nil
	},
}

func init() {
	proxyServeCmd.Flags().IntVar(&servePort, "port", proxy.DefaultLocalPort, "port to listen on (loopback, plus the Docker bridge with --docker-bridge)")
	proxyServeCmd.Flags().BoolVar(&serveDockerBridge, "docker-bridge", false, "also listen on the Docker bridge so build containers can reach the proxy; this lets every container on the machine use it")
	proxyServeCmd.Flags().BoolVar(&serveQuiet, "quiet", false, "log only warnings and errors instead of every request")
	proxyServeInstallCmd.Flags().IntVar(&servePort, "port", proxy.DefaultLocalPort, "port the service should listen on")
	proxyServeInstallCmd.Flags().BoolVar(&serveDockerBridge, "docker-bridge", false, "also listen on the Docker bridge so build containers can reach the proxy; this lets every container on the machine use it")
	proxyServeInstallCmd.Flags().BoolVar(&serveInstallDry, "dry-run", false, "print what would change without applying it")
	proxyServeUninstallCmd.Flags().BoolVar(&serveUninstallDry, "dry-run", false, "print what would change without applying it")

	proxyServeCmd.AddCommand(proxyServeInstallCmd)
	proxyServeCmd.AddCommand(proxyServeUninstallCmd)
	proxyCmd.AddCommand(proxyServeCmd)
}

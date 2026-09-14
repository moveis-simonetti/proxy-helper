package serve

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Containers cannot reach the host's loopback: inside a container, 127.0.0.1
// is the container itself. So a daemon bound only to loopback serves every
// tool on the host but breaks the moment a build step needs the network.
//
// Binding additionally to the Docker bridge gateway fixes that, at a real
// cost: the proxy authenticates nobody, so every container on the machine can
// use it. That is why it is opt-in and never the default — the bridge address
// is not reachable from the LAN, but it is reachable from any container.

// DockerBridgeName is the interface Docker creates for its default bridge.
const DockerBridgeName = "docker0"

// DockerBridgeAddr returns the IPv4 address of the Docker bridge, which is
// the address containers use to reach services on the host. It reports an
// error when Docker has not created the bridge, which is the case on a
// machine where Docker was never started.
func DockerBridgeAddr() (string, error) {
	iface, err := net.InterfaceByName(DockerBridgeName)
	if err != nil {
		return "", fmt.Errorf("no %s interface: is Docker installed and started? (%w)", DockerBridgeName, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("reading %s addresses: %w", DockerBridgeName, err)
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "", fmt.Errorf("%s has no IPv4 address", DockerBridgeName)
}

// ListenAddrs builds the addresses the daemon should bind. Loopback is always
// present; the Docker bridge is added only when the caller asked for it.
//
// Every address is checked for being a private, non-routable one: a typo that
// bound this proxy to a LAN address would turn it into an open relay, so the
// daemon refuses rather than trusting the caller.
func ListenAddrs(port int, dockerBridge bool) ([]string, error) {
	addrs := []string{net.JoinHostPort("127.0.0.1", fmt.Sprint(port))}
	if !dockerBridge {
		return addrs, nil
	}

	bridge, err := DockerBridgeAddr()
	if err != nil {
		return nil, err
	}
	if err := refuseRoutableAddr(bridge); err != nil {
		return nil, err
	}
	return append(addrs, net.JoinHostPort(bridge, fmt.Sprint(port))), nil
}

// refuseRoutableAddr rejects anything that is not a private address, so the
// proxy can never end up listening somewhere the wider network can reach it.
func refuseRoutableAddr(host string) error {
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%q is not an IP address", host)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return nil
	}
	return fmt.Errorf("refusing to listen on %s: it is publicly routable, and this proxy does not authenticate its clients", host)
}

// dockerBridgeInterfaceName matches every bridge network Docker creates on
// the host: "docker0" for the default bridge, "br-<12 hex chars>" for every
// user-defined one (what "docker network create" and every compose project
// get). Reading interface names needs no docker CLI or socket access — the
// same reason DockerBridgeAddr above does it this way instead of shelling
// out.
var dockerBridgeInterfaceName = regexp.MustCompile(`^(docker0|br-[0-9a-f]{12})$`)

// DockerNetworkSubnets returns the CIDR of every Docker bridge network on
// this host. A failure to list interfaces is returned; a single interface
// with no usable address is just skipped, since a network can exist without
// a container ever having pulled an address from it.
//
// This exists to keep container-to-container traffic off this proxy: once
// --docker-bridge is on, any container can reach this daemon, and if a
// container's own HTTP_PROXY points here without its NO_PROXY excluding its
// own network, a call to a sibling container by IP gets forwarded to
// whatever upstream is configured instead of staying on the host — Docker
// itself never routes inter-network traffic through an HTTP proxy, so there
// is no case where forwarding it here is what anyone wanted.
// lookupDockerNetworkSubnets is DockerNetworkSubnets behind a variable so
// loadSnapshot's use of it can be swapped out in tests without depending on
// this host actually having Docker networks to discover.
var lookupDockerNetworkSubnets = DockerNetworkSubnets

func DockerNetworkSubnets() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("listing network interfaces: %w", err)
	}
	var subnets []string
	for _, iface := range ifaces {
		if !dockerBridgeInterfaceName.MatchString(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			// iface.Addrs() carries the interface's own address (e.g.
			// 172.19.0.1/16), not the network's base address; mask it down
			// to a proper CIDR (172.19.0.0/16) so it reads correctly
			// wherever the no-proxy list is displayed, not just where it is
			// matched.
			network := &net.IPNet{IP: ipnet.IP.Mask(ipnet.Mask), Mask: ipnet.Mask}
			subnets = append(subnets, network.String())
		}
	}
	return subnets, nil
}

// DockerTargets are the targets whose config is consumed from inside a
// container, and which therefore cannot use the loopback address.
var DockerTargets = map[string]bool{
	"dockerd":       true,
	"docker-config": true,
}

// IsDockerTarget reports whether a target name needs a container-reachable
// proxy address rather than loopback.
func IsDockerTarget(name string) bool {
	return DockerTargets[strings.ToLower(name)]
}

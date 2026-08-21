package serve

import (
	"fmt"
	"net"
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

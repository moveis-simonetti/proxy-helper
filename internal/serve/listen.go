package serve

import (
	"fmt"
	"net"
	"time"
)

// ProbeTimeout bounds a single address probe. These are loopback and Docker
// bridge addresses on the same machine: they answer in microseconds or not
// at all, so the timeout only has to cover a pathologically loaded host.
const ProbeTimeout = 300 * time.Millisecond

// ProbeListening returns the subset of addrs that accept a TCP connection,
// in the order given.
//
// It exists because "what the daemon listens on" was previously *derived*
// from config.json plus a live interface lookup, which is a guess about the
// running process rather than an observation of it. A unit installed without
// --docker-bridge next to a config that says docker_bridge:true made the UI
// claim an address nothing was bound to — and the reverse hid one that was.
// Dialing answers the question the user is actually asking: can something
// reach the proxy here?
//
// A dial cannot tell *which* process answered, so an unrelated program
// squatting the port reads as listening. That is an acceptable trade: the
// service state shown next to this comes from systemd, and an address that
// answers is the useful fact either way.
func ProbeListening(addrs []string, timeout time.Duration) []string {
	live := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		conn, err := net.DialTimeout("tcp", addr, timeout)
		if err != nil {
			continue
		}
		conn.Close()
		live = append(live, addr)
	}
	return live
}

// ListeningAddrs probes every address the daemon could plausibly be bound
// to on this machine — loopback, plus the Docker bridge when one exists —
// and returns those that answer.
//
// The bridge is probed regardless of the saved docker_bridge preference, on
// purpose: the preference is precisely the thing that may disagree with
// reality, so consulting it here would reintroduce the guess.
func ListeningAddrs(port int) []string {
	candidates := []string{net.JoinHostPort("127.0.0.1", fmt.Sprint(port))}
	if bridge, err := DockerBridgeAddr(); err == nil {
		candidates = append(candidates, net.JoinHostPort(bridge, fmt.Sprint(port)))
	}
	return ProbeListening(candidates, ProbeTimeout)
}

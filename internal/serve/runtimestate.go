package serve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"proxy-helper/internal/proxy"
)

// RuntimeState is what the daemon is doing right now, published for readers
// outside the process.
//
// It exists because in mode auto the effective routing is not derivable from
// config.json: it also depends on a probe verdict that lives only in the
// daemon's memory. Without this file, "proxy status" and the GUI switch
// would have to either re-probe the upstream themselves (a second opinion
// that can disagree with the one actually in force) or lie by reporting the
// configured mode as if it were the effective one.
type RuntimeState struct {
	// Running is false when there is no state file, i.e. no daemon. It is
	// not serialised — it is set by ReadRuntimeState from the file's mere
	// existence, so callers get "no daemon" as an ordinary answer instead
	// of an error to special-case.
	Running           bool       `json:"-"`
	Mode              proxy.Mode `json:"mode"`
	UpstreamAddr      string     `json:"upstream_addr,omitempty"`
	UpstreamReachable bool       `json:"upstream_reachable"`
	// Forwarding is the mode and the verdict resolved together: what the
	// daemon is really doing with traffic. This is the field a UI wants.
	Forwarding bool `json:"forwarding"`
	Port       int  `json:"port,omitempty"`
	// PID identifies the process that wrote this. A reader compares it with
	// systemd's MainPID: a file left behind by a killed daemon, or written
	// by a build that is no longer the one running, names a different PID.
	PID int `json:"pid,omitempty"`
}

// runtimeStatePath is under XDG_RUNTIME_DIR on purpose: the file describes a
// running process, so it should die with the session rather than linger in
// ~/.config claiming a daemon that is long gone.
func runtimeStatePath() (string, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR is not set")
	}
	return filepath.Join(dir, "proxy-helper", "state.json"), nil
}

// PublishRuntimeState writes the current state for readers outside the
// daemon. Called at startup, after every reload, and on every reachability
// transition.
func (s *State) PublishRuntimeState() error {
	path, err := runtimeStatePath()
	if err != nil {
		return err
	}
	snap := s.current.Load()
	rs := RuntimeState{
		Mode:              snap.mode,
		UpstreamAddr:      snap.upstreamAddr,
		UpstreamReachable: s.reachable.Load(),
		Port:              int(s.port.Load()),
		PID:               os.Getpid(),
	}
	// Resolved the same way Router() resolves it, so the file can never
	// disagree with what the request path is actually doing.
	switch snap.mode {
	case proxy.ModeUpstream:
		rs.Forwarding = snap.upstreamAddr != ""
	case proxy.ModeAuto:
		rs.Forwarding = snap.upstreamAddr != "" && rs.UpstreamReachable
	}

	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Atomic, for the same reason profiles.go writes atomically: a reader
	// takes no lock, so it must see either the old file or the new one and
	// never a half-written one.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ClearRuntimeState removes the published state. The daemon calls it on the
// way out so a stopped daemon never looks like a running one.
func ClearRuntimeState() error {
	path, err := runtimeStatePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ReadRuntimeState reports what the daemon published. A missing file is not
// an error: it is the ordinary "no daemon running" answer, and the zero
// value it returns says exactly that.
func ReadRuntimeState() (RuntimeState, error) {
	path, err := runtimeStatePath()
	if err != nil {
		return RuntimeState{}, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return RuntimeState{}, nil
	}
	if err != nil {
		return RuntimeState{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var rs RuntimeState
	if err := json.Unmarshal(data, &rs); err != nil {
		// A corrupt state file must not break status output; treat it the
		// same as no daemon.
		return RuntimeState{}, nil
	}
	rs.Running = true
	return rs, nil
}

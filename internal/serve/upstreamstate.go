package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The daemon and the window are separate processes, so "the proxy stopped
// accepting our password" has to cross a process boundary. It travels as a
// small file rather than a socket or a log scrape: the window needs one
// fact, checked occasionally, and a file survives either side restarting.
//
// This matters because the failure is otherwise invisible. A password that
// expires takes the internet down for everyone at once, on a day nobody
// chose, and the only symptom is pages not loading. Without this, the app
// that exists to manage the proxy would be the one place that could not say
// what was wrong.

// UpstreamProblem is what is wrong with the upstream proxy, if anything.
type UpstreamProblem string

const (
	// ProblemNone: the last request got through.
	ProblemNone UpstreamProblem = ""
	// ProblemRejected: the proxy answered 407. The credentials stopped
	// being accepted, which is what an expired password looks like.
	ProblemRejected UpstreamProblem = "rejected"
	// ProblemUnreachable: the proxy could not be reached at all. The
	// credentials may be perfectly good — nothing answered to check them,
	// and telling someone to re-enter a working password would send them
	// chasing the wrong thing.
	ProblemUnreachable UpstreamProblem = "unreachable"
)

// UpstreamState is what the daemon last observed about the proxy it chains to.
type UpstreamState struct {
	// Problem is empty when the last request succeeded.
	Problem UpstreamProblem `json:"problem,omitempty"`
	// Since is when the problem was first seen. It is not refreshed by
	// later failures, so the window can say how long this has been going.
	Since time.Time `json:"since,omitempty"`
	// Profile is the profile that was active at the time, so a problem
	// left over from another profile is not shown against a new one.
	Profile string `json:"profile,omitempty"`
}

// Failing reports whether anything is wrong.
func (s UpstreamState) Failing() bool { return s.Problem != ProblemNone }

var upstreamStateMu sync.Mutex

// UpstreamStatePath is where the state lives.
func UpstreamStatePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "proxy-helper", "upstream-state.json"), nil
}

// RecordUpstreamProblem notes that something is wrong with the upstream.
//
// It is idempotent on purpose: every refused request would otherwise rewrite
// the file, and Since would keep moving forward, so the window could never
// tell a problem that started an hour ago from one that started now.
func RecordUpstreamProblem(problem UpstreamProblem, profile string) {
	upstreamStateMu.Lock()
	defer upstreamStateMu.Unlock()

	current, err := readUpstreamState()
	if err == nil && current.Problem == problem && current.Profile == profile {
		return
	}
	_ = writeUpstreamState(UpstreamState{Problem: problem, Since: time.Now(), Profile: profile})
}

// RecordUpstreamOK clears the state after a request succeeds.
//
// Without this the warning would stick until someone restarted the daemon,
// long after the person fixed their password — and a warning that outlives
// its cause teaches people to ignore warnings.
func RecordUpstreamOK() {
	upstreamStateMu.Lock()
	defer upstreamStateMu.Unlock()

	if current, err := readUpstreamState(); err != nil || !current.Failing() {
		return
	}
	_ = writeUpstreamState(UpstreamState{})
}

// ReadUpstreamState reports what the daemon last observed. A missing file means
// nothing has gone wrong, which is the common case and not an error.
func ReadUpstreamState() UpstreamState {
	upstreamStateMu.Lock()
	defer upstreamStateMu.Unlock()

	state, err := readUpstreamState()
	if err != nil {
		return UpstreamState{}
	}
	return state
}

func readUpstreamState() (UpstreamState, error) {
	path, err := UpstreamStatePath()
	if err != nil {
		return UpstreamState{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return UpstreamState{}, err
	}
	var state UpstreamState
	if err := json.Unmarshal(content, &state); err != nil {
		return UpstreamState{}, err
	}
	return state, nil
}

func writeUpstreamState(state UpstreamState) error {
	path, err := UpstreamStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0600)
}

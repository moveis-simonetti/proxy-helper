// Package app holds the orchestration shared by the CLI and the GUI: which
// targets to touch, with which config, and what happened to each one.
//
// It deliberately produces no user-facing text. Every result is a value —
// an Outcome, a NoticeKind, a map of arguments — and the presentation layer
// renders it: cmd/ in English, internal/gui/ in Portuguese.
package app

import (
	"fmt"
	"strings"
)

// Outcome is what happened to one target.
type Outcome int

const (
	// OutcomeApplied means the target now carries the proxy settings.
	OutcomeApplied Outcome = iota
	// OutcomeCleared means the proxy settings were removed from the target.
	OutcomeCleared
	// OutcomeSkipped means the target is not available on this system. It is
	// never an error: a machine without KDE simply has no kwriteconfig.
	OutcomeSkipped
	// OutcomeFailed means the target was available and the operation failed.
	OutcomeFailed
)

// Result is what happened to a single target.
type Result struct {
	Target  string
	Outcome Outcome
	// Detail carries technical, language-neutral context: the file that was
	// written, the command that is missing. It is shown verbatim by both
	// front ends.
	Detail string
	// Err is the underlying failure. Its text comes from the core and from
	// external commands, so it stays in English everywhere.
	Err error
}

// NoticeKind identifies a warning without spelling it out.
type NoticeKind int

const (
	// NoticeUnreachablePassword warns that the password comes from a file or
	// an environment variable, which only the daemon reads, while the config
	// is being written straight into each tool. Args carries "source".
	NoticeUnreachablePassword NoticeKind = iota
	// NoticeDockerLoopback warns that a Docker target points at 127.0.0.1,
	// which containers cannot reach. Notice.Target carries the target name.
	NoticeDockerLoopback
	// NoticeNeedsSudo warns that a target is about to prompt for a password.
	// Notice.Target carries the target name.
	NoticeNeedsSudo
	// NoticeProfileAlreadyPlumbed reports that switching profiles changed
	// state only: the targets already point at the local daemon, so nothing
	// was written to them. Notice.Args carries "profile".
	NoticeProfileAlreadyPlumbed
	// NoticeDockerNeedsRestart warns that the dockerd target wrote (or
	// removed) a systemd drop-in that only takes effect after
	// `systemctl restart docker`, which is never done automatically because
	// it disrupts running containers. It is raised only for the dockerd
	// target, never for docker-config, which is read fresh on every command
	// and needs no restart. Notice.Args carries "op": "apply" or "clear",
	// since the two operations word the warning differently.
	NoticeDockerNeedsRestart

	// NumNoticeKinds is the number of NoticeKind values defined above. Kept
	// right next to the iota block so it tracks it automatically; a
	// presentation layer's exhaustiveness test can range over
	// NoticeKind(0)..NumNoticeKinds-1 without hardcoding the count.
	NumNoticeKinds
)

// Notice is a warning aimed at the user, carried as data so each front end
// can word it in its own language.
type Notice struct {
	Kind   NoticeKind
	Target string
	Args   map[string]string
}

// Report is everything one operation produced.
type Report struct {
	Results []Result
	Notices []Notice
}

// Add records what happened to one target.
func (r *Report) Add(res Result) { r.Results = append(r.Results, res) }

// Warn records a warning.
func (r *Report) Warn(n Notice) { r.Notices = append(r.Notices, n) }

// Failed lists the targets that failed, in the order they were attempted.
func (r *Report) Failed() []string {
	var out []string
	for _, res := range r.Results {
		if res.Outcome == OutcomeFailed {
			out = append(out, res.Target)
		}
	}
	return out
}

// Err combines the per-target failures into one error, or nil when every
// target that was available succeeded.
func (r *Report) Err() error {
	failed := r.Failed()
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("failed targets: %s", strings.Join(failed, ", "))
}

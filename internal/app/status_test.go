package app

import (
	"os"
	"strings"
	"testing"

	"proxy-helper/internal/proxy"
)

func TestCollectReportsEveryTargetInOrder(t *testing.T) {
	git := &fakeTarget{name: "git", available: true}
	kde := &fakeTarget{name: "kde", available: false}

	sts, err := Collect(depsFor(git, kde), &proxy.Executor{}, []string{"all"}, false)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(sts) != 2 {
		t.Fatalf("expected two statuses, got %d", len(sts))
	}
	if sts[0].Name != "git" || sts[1].Name != "kde" {
		t.Errorf("order must follow the resolved target list, got %+v", sts)
	}
}

// TestCollectSurvivesABrokenTarget covers the sweep guarantee: one target
// whose Status errors must not hide the other ten.
func TestCollectSurvivesABrokenTarget(t *testing.T) {
	broken := &fakeTarget{name: "snap", available: true, statusErr: os.ErrPermission}
	git := &fakeTarget{name: "git", available: true}

	sts, err := Collect(depsFor(broken, git), &proxy.Executor{}, []string{"all"}, false)
	if err != nil {
		t.Fatalf("a broken target is not a global error: %v", err)
	}
	if len(sts) != 2 {
		t.Fatalf("expected both targets reported, got %d", len(sts))
	}
	if !strings.HasPrefix(sts[0].Detail, "error: ") {
		t.Errorf("Detail must be prefixed with %q so the table marks it as a failure, got %q", "error: ", sts[0].Detail)
	}
	if !strings.Contains(sts[0].Detail, broken.statusErr.Error()) {
		t.Errorf("Detail must contain the underlying error text %q, got %q", broken.statusErr.Error(), sts[0].Detail)
	}
}

// TestElevateRereadsOnlyFlaggedTargets covers the review finding: retrying
// with sudo must not re-query targets that already answered as this user.
func TestElevateRereadsOnlyFlaggedTargets(t *testing.T) {
	snap := &fakeTarget{name: "snap", available: true}
	git := &fakeTarget{name: "git", available: true}

	sts := []proxy.Status{
		{Name: "snap", NeedsElevation: true},
		{Name: "git", Available: true, Detail: "untouched"},
	}

	out, err := Elevate(depsFor(snap, git), &proxy.Executor{}, sts)
	if err != nil {
		t.Fatalf("Elevate: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected two statuses, got %d", len(out))
	}
	if out[1] != sts[1] {
		t.Errorf("the entry that didn't need elevation must be untouched, got %+v", out[1])
	}
	if git.statusCalls != 0 {
		t.Errorf("a target that wasn't flagged must not be re-queried, got %d calls", git.statusCalls)
	}
	if snap.statusCalls != 1 {
		t.Errorf("the flagged target should be re-read exactly once, got %d calls", snap.statusCalls)
	}
	if out[0].Name != "snap" || out[0].NeedsElevation {
		t.Errorf("expected the flagged entry to be replaced with its re-read status, got %+v", out[0])
	}
}

// TestElevateNoopsWhenNothingNeedsIt ensures Elevate doesn't touch any
// target when nothing was flagged.
func TestElevateNoopsWhenNothingNeedsIt(t *testing.T) {
	git := &fakeTarget{name: "git", available: true}
	sts := []proxy.Status{{Name: "git", Available: true}}

	out, err := Elevate(depsFor(git), &proxy.Executor{}, sts)
	if err != nil {
		t.Fatalf("Elevate: %v", err)
	}
	if len(out) != 1 || out[0] != sts[0] {
		t.Errorf("expected the input returned unchanged, got %+v", out)
	}
	if git.statusCalls != 0 {
		t.Errorf("no target should be queried when nothing needs elevation, got %d calls", git.statusCalls)
	}
}

func TestNeedsElevationSpotsAnUnreadableTarget(t *testing.T) {
	yes := []proxy.Status{{Name: "git"}, {Name: "snap", NeedsElevation: true}}
	if !NeedsElevation(yes) {
		t.Error("expected NeedsElevation to be true")
	}
	no := []proxy.Status{{Name: "git"}}
	if NeedsElevation(no) {
		t.Error("expected NeedsElevation to be false")
	}
}

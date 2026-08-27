package serve

import (
	"os"
	"testing"
	"time"
)

// withTempCache points the state file at a directory the test owns, so
// these never touch the developer's real cache.
func withTempCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Windows uses LocalAppData for os.UserCacheDir.
	t.Setenv("LocalAppData", t.TempDir())
}

func TestNoStateMeansNothingIsWrong(t *testing.T) {
	withTempCache(t)
	// A missing file is the common case, not an error: most machines never
	// have a credential rejected.
	if got := ReadUpstreamState(); got.Failing() {
		t.Errorf("a machine with no state file reports a rejection: %+v", got)
	}
}

func TestRecordedProblemIsVisibleToAnotherReader(t *testing.T) {
	withTempCache(t)

	RecordUpstreamProblem(ProblemRejected, "Escritório")

	got := ReadUpstreamState()
	if !got.Failing() {
		t.Fatal("the rejection was not recorded; the window would show nothing while the internet is down")
	}
	if got.Problem != ProblemRejected {
		t.Errorf("Problem = %q, want %q", got.Problem, ProblemRejected)
	}
	if got.Profile != "Escritório" {
		t.Errorf("Profile = %q, want %q", got.Profile, "Escritório")
	}
	if got.Since.IsZero() {
		t.Error("Since is zero; the window cannot say how long this has been going")
	}
}

func TestRepeatedRejectionsKeepTheOriginalTime(t *testing.T) {
	withTempCache(t)

	RecordUpstreamProblem(ProblemRejected, "Escritório")
	first := ReadUpstreamState().Since

	// Every refused request calls this. If each one rewrote Since, a
	// problem that started an hour ago would always look like it started
	// this second.
	time.Sleep(10 * time.Millisecond)
	RecordUpstreamProblem(ProblemRejected, "Escritório")

	if got := ReadUpstreamState().Since; !got.Equal(first) {
		t.Errorf("Since moved from %v to %v on a repeated rejection", first, got)
	}
}

func TestSwitchingProfileRecordsTheNewOne(t *testing.T) {
	withTempCache(t)

	RecordUpstreamProblem(ProblemRejected, "Escritório")
	RecordUpstreamProblem(ProblemRejected, "Casa")

	// Switching profile is what someone does to work around a proxy that
	// stopped accepting them; the warning has to follow.
	if got := ReadUpstreamState(); got.Profile != "Casa" {
		t.Errorf("Profile = %q, want %q", got.Profile, "Casa")
	}
}

func TestSuccessClearsTheWarning(t *testing.T) {
	withTempCache(t)

	RecordUpstreamProblem(ProblemRejected, "Escritório")
	RecordUpstreamOK()

	// A warning that outlives its cause teaches people to ignore warnings.
	if got := ReadUpstreamState(); got.Failing() {
		t.Errorf("the warning survived a successful request: %+v", got)
	}
}

func TestClearingWhenNothingIsWrongDoesNotWrite(t *testing.T) {
	withTempCache(t)

	// Every successful request calls this. It must not write the file on
	// each one — that is a disk write per request on a busy proxy.
	RecordUpstreamOK()

	if got := ReadUpstreamState(); got.Failing() {
		t.Errorf("unexpected state: %+v", got)
	}
	path, err := UpstreamStatePath()
	if err != nil {
		t.Fatalf("resolving the path: %v", err)
	}
	if fileExists(path) {
		t.Error("a state file was created by a success on a clean machine")
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestAnUnreachableProxyIsNotReportedAsARefusal(t *testing.T) {
	withTempCache(t)

	RecordUpstreamProblem(ProblemUnreachable, "Escritório")

	// The distinction is the whole point of having a kind rather than a
	// bool: telling someone to re-enter a password that works, because the
	// proxy happens to be down, sends them chasing the wrong thing.
	got := ReadUpstreamState()
	if got.Problem != ProblemUnreachable {
		t.Errorf("Problem = %q, want %q", got.Problem, ProblemUnreachable)
	}
}

func TestAProblemThatChangesKindIsRecordedAfresh(t *testing.T) {
	withTempCache(t)

	RecordUpstreamProblem(ProblemUnreachable, "Escritório")
	first := ReadUpstreamState().Since

	time.Sleep(10 * time.Millisecond)
	RecordUpstreamProblem(ProblemRejected, "Escritório")

	got := ReadUpstreamState()
	if got.Problem != ProblemRejected {
		t.Errorf("Problem = %q, want %q", got.Problem, ProblemRejected)
	}
	// A proxy that came back up and then refused the password is a new
	// problem, so its clock starts over.
	if !got.Since.After(first) {
		t.Error("Since did not move when the kind of problem changed")
	}
}

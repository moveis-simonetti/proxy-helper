package gui

import (
	"errors"
	"strings"
	"testing"

	"proxy-helper/internal/app"
)

func TestResultRowForAllOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		res     app.Result
		wantLbl string
		wantErr string
	}{
		{
			name:    "applied",
			res:     app.Result{Target: "shell", Outcome: app.OutcomeApplied},
			wantLbl: "aplicado",
		},
		{
			name:    "cleared",
			res:     app.Result{Target: "git", Outcome: app.OutcomeCleared},
			wantLbl: "removido",
		},
		{
			name:    "skipped",
			res:     app.Result{Target: "kde", Outcome: app.OutcomeSkipped, Detail: "kwriteconfig/plasmashell not found"},
			wantLbl: "pulado",
		},
		{
			name:    "failed",
			res:     app.Result{Target: "apt", Outcome: app.OutcomeFailed, Err: errors.New("exit status 100: E: Unable to locate package")},
			wantLbl: "falhou",
			wantErr: "exit status 100: E: Unable to locate package",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := resultRowFor(tc.res)
			if row.Target != tc.res.Target {
				t.Errorf("Target = %q, want %q", row.Target, tc.res.Target)
			}
			if row.Label != tc.wantLbl {
				t.Errorf("Label = %q, want %q", row.Label, tc.wantLbl)
			}
			if row.Detail != tc.res.Detail {
				t.Errorf("Detail = %q, want %q", row.Detail, tc.res.Detail)
			}
			if row.Err != tc.wantErr {
				t.Errorf("Err = %q, want %q", row.Err, tc.wantErr)
			}
		})
	}
}

func TestResultRowsPreservesOrder(t *testing.T) {
	rep := &app.Report{Results: []app.Result{
		{Target: "shell", Outcome: app.OutcomeApplied},
		{Target: "git", Outcome: app.OutcomeCleared},
		{Target: "kde", Outcome: app.OutcomeSkipped},
		{Target: "apt", Outcome: app.OutcomeFailed, Err: errors.New("boom")},
	}}
	rows := resultRows(rep)
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	wantOrder := []string{"shell", "git", "kde", "apt"}
	for i, name := range wantOrder {
		if rows[i].Target != name {
			t.Errorf("rows[%d].Target = %q, want %q", i, rows[i].Target, name)
		}
	}
}

// TestAppliedCountExcludesSkippedAndFailed covers I1: selecting 5 targets
// where 2 are unavailable and 1 fails must not be reported as 5 applied.
func TestAppliedCountExcludesSkippedAndFailed(t *testing.T) {
	rep := &app.Report{Results: []app.Result{
		{Target: "shell", Outcome: app.OutcomeApplied},
		{Target: "git", Outcome: app.OutcomeApplied},
		{Target: "kde", Outcome: app.OutcomeSkipped},
		{Target: "gnome", Outcome: app.OutcomeSkipped},
		{Target: "apt", Outcome: app.OutcomeFailed, Err: errors.New("boom")},
	}}
	if got := appliedCount(rep); got != 2 {
		t.Errorf("appliedCount() = %d, want 2 (5 results, 2 skipped, 1 failed)", got)
	}
}

func TestNoticeTextIsPortugueseNotAppText(t *testing.T) {
	cases := []struct {
		name string
		n    app.Notice
		want []string // substrings that must appear
	}{
		{
			name: "unreachable password",
			n:    app.Notice{Kind: app.NoticeUnreachablePassword, Args: map[string]string{"source": "password_file"}},
			want: []string{"password_file", "senha", "proxy local"},
		},
		{
			name: "docker loopback",
			n:    app.Notice{Kind: app.NoticeDockerLoopback, Target: "dockerd"},
			want: []string{"dockerd", "127.0.0.1", "containers"},
		},
		{
			name: "needs sudo",
			n:    app.Notice{Kind: app.NoticeNeedsSudo, Target: "apt"},
			want: []string{"apt", "sudo"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := noticeText(tc.n)
			for _, sub := range tc.want {
				if !strings.Contains(got, sub) {
					t.Errorf("noticeText(%+v) = %q, want it to contain %q", tc.n, got, sub)
				}
			}
		})
	}
}

func TestNoticeTextsPreservesOrder(t *testing.T) {
	rep := &app.Report{Notices: []app.Notice{
		{Kind: app.NoticeNeedsSudo, Target: "apt"},
		{Kind: app.NoticeDockerLoopback, Target: "dockerd"},
	}}
	texts := noticeTexts(rep)
	if len(texts) != 2 {
		t.Fatalf("got %d texts, want 2", len(texts))
	}
	if !strings.Contains(texts[0], "apt") {
		t.Errorf("texts[0] = %q, want it to mention apt", texts[0])
	}
	if !strings.Contains(texts[1], "dockerd") {
		t.Errorf("texts[1] = %q, want it to mention dockerd", texts[1])
	}
}

// TestCollapseRepeatedLines pins the fix for the result dialog being
// dominated by nine identical dconf-WARNING lines: a run of repeated,
// non-blank lines collapses to one, annotated with the count.
func TestCollapseRepeatedLines(t *testing.T) {
	in := "a\nwarn\nwarn\nwarn\nb"
	got := collapseRepeatedLines(in)
	want := "a\nwarn (repetido 3x)\nb"
	if got != want {
		t.Errorf("collapseRepeatedLines(%q) = %q, want %q", in, got, want)
	}
}

func TestCollapseRepeatedLinesLeavesNonRepeatedAlone(t *testing.T) {
	in := "a\nb\nc"
	if got := collapseRepeatedLines(in); got != in {
		t.Errorf("collapseRepeatedLines(%q) = %q, want unchanged", in, got)
	}
}

func TestCollapseRepeatedLinesLeavesBlankRunsAlone(t *testing.T) {
	in := "a\n\n\nb"
	if got := collapseRepeatedLines(in); got != in {
		t.Errorf("collapseRepeatedLines(%q) = %q, want unchanged", in, got)
	}
}

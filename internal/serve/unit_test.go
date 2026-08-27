package serve

import (
	"os"
	"strings"
	"testing"

	"proxy-helper/internal/proxy"
)

func TestRenderUnit(t *testing.T) {
	got := RenderUnit("/usr/local/bin/proxy-helper", 8888, false)

	for _, want := range []string{
		"ExecStart=/usr/local/bin/proxy-helper proxy serve --port 8888",
		"ExecReload=/bin/kill -HUP $MAINPID",
		"Restart=always",
		"WantedBy=default.target",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit is missing %q\n---\n%s", want, got)
		}
	}
}

func TestUnitPathIsUserScoped(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/home/tester/.config")
	got, err := UnitPath()
	if err != nil {
		t.Fatalf("UnitPath: %v", err)
	}
	want := "/home/tester/.config/systemd/user/proxy-helper.service"
	if got != want {
		t.Errorf("UnitPath() = %q, want %q", got, want)
	}
}

func TestInstallUnitIsDryRunnable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ex := &proxy.Executor{DryRun: true}
	if err := InstallUnit(ex, "/usr/local/bin/proxy-helper", 8888, false); err != nil {
		t.Fatalf("InstallUnit in dry-run: %v", err)
	}
	path, _ := UnitPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("dry-run must not write the unit file")
	}
}

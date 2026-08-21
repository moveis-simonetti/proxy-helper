package serve

import (
	"strings"
	"testing"
)

func TestListenAddrsIsLoopbackOnlyByDefault(t *testing.T) {
	addrs, err := ListenAddrs(8888, false)
	if err != nil {
		t.Fatalf("ListenAddrs: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "127.0.0.1:8888" {
		t.Errorf("got %v, want exactly [127.0.0.1:8888] — the daemon authenticates no client", addrs)
	}
}

func TestRefuseRoutableAddr(t *testing.T) {
	for _, ok := range []string{"127.0.0.1", "172.17.0.1", "192.168.1.10", "10.0.0.1", "169.254.1.1"} {
		if err := refuseRoutableAddr(ok); err != nil {
			t.Errorf("%s should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"8.8.8.8", "1.1.1.1", "203.0.113.5"} {
		err := refuseRoutableAddr(bad)
		if err == nil {
			t.Errorf("%s is publicly routable and must be refused", bad)
			continue
		}
		if !strings.Contains(err.Error(), "authenticate") {
			t.Errorf("the refusal should say why: %v", err)
		}
	}
	if err := refuseRoutableAddr("not-an-ip"); err == nil {
		t.Error("a non-IP must be refused")
	}
}

func TestIsDockerTarget(t *testing.T) {
	for _, name := range []string{"dockerd", "docker-config"} {
		if !IsDockerTarget(name) {
			t.Errorf("%s reads its config from inside a container and must be treated as a Docker target", name)
		}
	}
	for _, name := range []string{"git", "npm", "apt", "shell", "gnome", "vscode", "snap", "lxd", "kde"} {
		if IsDockerTarget(name) {
			t.Errorf("%s runs on the host and must keep the loopback address", name)
		}
	}
}

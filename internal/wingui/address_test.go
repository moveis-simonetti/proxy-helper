package wingui

import "testing"

func TestConfigFromAddressReadsHostAndPort(t *testing.T) {
	got, err := ConfigFromAddress("proxy.interno:3128")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Host != "proxy.interno" || got.Port != "3128" {
		t.Errorf("got %s:%s, want proxy.interno:3128", got.Host, got.Port)
	}
	if got.PACURL != "" {
		t.Errorf("PACURL = %q, want empty for a plain address", got.PACURL)
	}
}

func TestConfigFromAddressAssumesTheUsualPort(t *testing.T) {
	// Expecting someone in management to know a proxy needs a port number
	// is a support ticket. The probe checks the guess immediately.
	got, err := ConfigFromAddress("proxy.interno")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Port != defaultProxyPort {
		t.Errorf("Port = %q, want the default %q", got.Port, defaultProxyPort)
	}
}

func TestConfigFromAddressStripsAPastedScheme(t *testing.T) {
	got, err := ConfigFromAddress("http://proxy.interno:8080/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Host != "proxy.interno" || got.Port != "8080" {
		t.Errorf("got %s:%s, want proxy.interno:8080", got.Host, got.Port)
	}
}

func TestConfigFromAddressRecognisesAPACFile(t *testing.T) {
	const pac = "http://config.interno/proxy.pac"
	got, err := ConfigFromAddress(pac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PACURL != pac {
		t.Errorf("PACURL = %q, want %q", got.PACURL, pac)
	}
	if got.Host != "" {
		t.Errorf("Host = %q; a PAC address must not also be read as a proxy host", got.Host)
	}
}

func TestConfigFromAddressDoesNotMistakeAHostNamedPacForAPACFile(t *testing.T) {
	// A proxy legitimately called "pac-proxy" is a host. Treating it as a
	// config file would break it in a way nobody could diagnose on screen.
	got, err := ConfigFromAddress("pac-proxy.interno:3128")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PACURL != "" {
		t.Errorf("PACURL = %q; the host merely has \"pac\" in its name", got.PACURL)
	}
	if got.Host != "pac-proxy.interno" {
		t.Errorf("Host = %q, want pac-proxy.interno", got.Host)
	}
}

func TestConfigFromAddressRejectsWhatItCannotRead(t *testing.T) {
	// Guessing a port for "proxy.interno:" would hide a typo, which the
	// probe would then report as the proxy being down — sending the person
	// to ask about an outage that is not happening.
	for name, addr := range map[string]string{
		"empty":             "",
		"spaces only":       "   ",
		"scheme only":       "http://",
		"empty port":        "proxy.interno:",
		"port not numeric":  "proxy.interno:porta",
		"port out of range": "proxy.interno:70000",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ConfigFromAddress(addr); err == nil {
				t.Errorf("expected an error, got %+v", got)
			}
		})
	}
}

//go:build !windows

package proxy

import (
	"os"
	"path/filepath"
)

// defaultFirefoxBaseDir is ~/.mozilla/firefox on Linux. The target is not
// registered outside Windows today, but the path keeps the package
// buildable and lets the logic be tested here.
func defaultFirefoxBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mozilla", "firefox"), nil
}

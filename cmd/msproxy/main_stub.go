//go:build !wingui

package main

import (
	"fmt"
	"os"
)

// Without the "wingui" tag this binary carries no interface. It exists so
// that "go build ./..." and "go vet ./..." keep working on a machine with
// no graphics toolchain, rather than failing on a package with no files.
func main() {
	fmt.Fprintln(os.Stderr, "this binary was built without the interface; rebuild with -tags wingui")
	os.Exit(1)
}

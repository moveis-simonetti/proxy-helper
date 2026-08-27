module proxy-helper

go 1.26.5

require (
	// Pinned to a pseudo-version of master, not the v0.6.4 tag: v0.6.4
	// fails to build with this toolchain (missing internal/callback
	// package, an upstream bug fixed after that tag). Do not "go get -u"
	// or pin back to v0.6.4 without re-checking that it compiles.
	github.com/gotk3/gotk3 v0.6.5-0.20251124190141-e7a9e823ca35
	github.com/spf13/cobra v1.10.2
	golang.org/x/net v0.58.0
)

require golang.org/x/sys v0.47.0

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)

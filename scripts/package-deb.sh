#!/usr/bin/env bash
# Build a .deb of the GUI binary, with its shared-library dependencies
# declared so that installing the package installs them too.
#
# This is the whole point of packaging instead of shipping a bare binary:
# "Depends:" is the mechanism apt uses to pull libayatana-appindicator3-1 in
# automatically, and to refuse the install (with a readable message) rather
# than let the binary fail to start on a system that cannot satisfy it.
#
# The dependency list is DERIVED, not written by hand, via dpkg-shlibdeps
# against the binary's actual NEEDED entries. Hand-written lists rot: on
# Ubuntu 24.04 libgtk-3-0 was renamed libgtk-3-0t64 for the time_t
# transition, and only the fact that the new package declares
# "Provides: libgtk-3-0" keeps an older name working at all.
#
# Built inside the same old base as scripts/build-gui.sh, for the same
# reason: glibc is backward but not forward compatible, so the floor comes
# from the oldest base we build on. Building the package on 22.04 also makes
# dpkg-shlibdeps emit version floors that Mint 21 can satisfy.
#
# The apt packages and the Go toolchain live in a prebuilt image
# (scripts/deb-build-image), not inline here — a throwaway --rm container
# used to apt-get/curl all of that on every single run. GOCACHE (this
# script's own, real one — see "go env GOCACHE" below) is mounted for the
# same reason GOMODCACHE already was: a CGO+gui build recompiling GTK
# bindings from scratch every time was most of what made this slow.
#
# Usage:
#   scripts/package-deb.sh
#   VERSION=1.2.3 BASE_IMAGE=ubuntu:20.04 scripts/package-deb.sh
set -euo pipefail

cd "$(dirname "$0")/.."

BASE_IMAGE="${BASE_IMAGE:-ubuntu:22.04}"
GO_VERSION="$(sed -n 's/^go //p' go.mod)"
DEB_ARCH="$(go env GOARCH)"

# A Debian version must start with a digit, so a bare git describe ("v0.3.0")
# needs its leading "v" stripped, and an untagged tree needs a placeholder
# that still sorts below any real release.
#
# The dev placeholder hashes the WORKING TREE, not just HEAD. Deriving it
# from the commit alone meant every rebuild of uncommitted work produced the
# same version string, and "apt install ./file.deb" then silently did
# nothing — it saw that version already installed and skipped it. No error,
# no warning, and the old files stayed on disk while the new package sat
# there looking installed. Hashing the diff and the untracked files makes an
# edited tree a different version, while an unchanged tree still rebuilds to
# the same one.
if [ -z "${VERSION:-}" ]; then
	if VERSION="$(git describe --tags --exact-match 2>/dev/null)"; then
		:
	else
		head_short="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
		tree_hash="$(
			{
				git rev-parse HEAD 2>/dev/null || true
				git diff HEAD 2>/dev/null || true
				git ls-files --others --exclude-standard -z 2>/dev/null \
					| xargs -0 -r sha1sum 2>/dev/null || true
			} | sha1sum | cut -c1-8
		)"
		VERSION="0.0.0~dev+${head_short}.${tree_hash}"
	fi
fi
VERSION="${VERSION#v}"

mkdir -p dist
GOMODCACHE="$(go env GOMODCACHE)"
GOCACHE="$(go env GOCACHE)"

echo "base image : $BASE_IMAGE"
echo "version    : $VERSION"
echo "arch       : $DEB_ARCH"

# Built once, reused every run after: apt-get and the Go toolchain download
# used to happen inside a throwaway --rm container on every single build.
# Docker's layer cache makes this a no-op (a few hundred ms) unless
# BASE_IMAGE, GO_VERSION, or scripts/deb-build-image/Dockerfile changed —
# see that file's doc comment.
IMAGE_TAG="proxy-helper-deb-build:${BASE_IMAGE//[:\/]/-}-go${GO_VERSION}"
docker build \
	--build-arg "BASE_IMAGE=$BASE_IMAGE" \
	--build-arg "GO_VERSION=$GO_VERSION" \
	-t "$IMAGE_TAG" \
	scripts/deb-build-image >/dev/null

docker run --rm \
	-v "$PWD:/src" \
	-v "$GOMODCACHE:/gomodcache" \
	-v "$GOCACHE:/gocache" \
	-w /src \
	-e "VERSION=$VERSION" \
	-e "DEB_ARCH=$DEB_ARCH" \
	-e "HOST_UID=$(id -u)" \
	-e "HOST_GID=$(id -g)" \
	"$IMAGE_TAG" \
	bash -euo pipefail -c '
		export GOMODCACHE=/gomodcache
		# The same real build cache "go build" already uses on the host —
		# mounted here for the same reason GOMODCACHE is: a CGO+gui build
		# recompiling GTK bindings from scratch on every run was most of
		# what made this script slow. Content-addressed by source+flags, so
		# sharing it with host-side non-cgo/non-gui builds is safe.
		export GOCACHE=/gocache
		export GOFLAGS=-buildvcs=false
		export CGO_ENABLED=1

		stage=/tmp/stage
		rm -rf "$stage"
		mkdir -p "$stage/DEBIAN" "$stage/usr/bin" "$stage/usr/share/applications"
		go build -tags gui -ldflags "-s -w" -o "$stage/usr/bin/proxy-helper-gui" .

		# The CLI ships alongside the GUI because the GUI depends on it: the
		# targets that need root (dockerd, snap, apt) are applied by
		# reinvoking this binary under pkexec, never by elevating a GTK
		# process. Without it, findCLIBinary() comes up empty and those
		# three targets fail with "could not find the proxy-helper CLI
		# binary" — a package that installs a program missing a quarter of
		# its function.
		#
		# CGO_ENABLED=0 (overriding the export above, which the GUI needs)
		# and no "gui" build tag: this binary must not link GTK. pkexec runs
		# it as root, and a GTK process opening a display as root is exactly
		# what the reinvocation exists to avoid.
		CGO_ENABLED=0 go build -ldflags "-s -w" -o "$stage/usr/bin/proxy-helper" .

		# Cheap guard for the rule above: catching a GTK-linked CLI here
		# beats shipping one. Mirrors the check the CI workflow runs.
		if nm "$stage/usr/bin/proxy-helper" 2>/dev/null | grep -qi gtk; then
			echo "the CLI binary linked GTK; it is reinvoked under pkexec and must not" >&2
			exit 1
		fi

		# The menu entry. Without it the program exists only as a command to
		# type, which is a strange thing to ask of a window with a tray icon.
		# Icon=network-workgroup is a theme name from adwaita-icon-theme,
		# which libgtk-3-0 already depends on, so no asset ships here.
		cp packaging/proxy-helper-gui.desktop "$stage/usr/share/applications/"
		desktop-file-validate "$stage/usr/share/applications/proxy-helper-gui.desktop"

		# Icons. The scalable SVG is the source of truth; the raster sizes
		# are generated from it rather than kept as separate files that
		# could drift. Panels and menus that do not render SVG fall back to
		# the nearest raster size, so shipping only the SVG would leave some
		# desktops with no icon at all.
		icons="$stage/usr/share/icons/hicolor"
		mkdir -p "$icons/scalable/apps" "$icons/symbolic/apps"
		cp packaging/icons/proxy-helper.svg "$icons/scalable/apps/"
		cp packaging/icons/proxy-helper-symbolic.svg "$icons/symbolic/apps/"
		cp packaging/icons/proxy-helper-off-symbolic.svg "$icons/symbolic/apps/"
		for size in 16 22 24 32 48 64 128 256; do
			mkdir -p "$icons/${size}x${size}/apps"
			rsvg-convert -w "$size" -h "$size" packaging/icons/proxy-helper.svg \
				-o "$icons/${size}x${size}/apps/proxy-helper.png"
		done

		# The systemd --user unit, shipped so "systemctl --global enable" in
		# postinst has something to find: InstallUnit (internal/serve/unit.go)
		# only ever writes a PER-USER copy, under that user'"'"'s own
		# $XDG_CONFIG_HOME — nothing before this wrote a copy anywhere a
		# --global enable can see, so it failed with "Unit ... does not
		# exist" even though it was wrapped in "|| true" and looked like it
		# worked. No --port/--docker-bridge baked into ExecStart, unlike
		# RenderUnit'"'"'s per-user copy: "proxy serve" already reads
		# EffectiveLocalPort()/DockerBridge from config.json itself when
		# those flags are absent (cmd/proxy_serve.go), so one generic unit
		# serves every port/bridge configuration without regenerating a
		# unit file for each. A user who later runs "proxy serve
		# install"/the GUI'"'"'s Salvar still gets InstallUnit'"'"'s per-user
		# copy, which systemd prefers over this one — same override order
		# every other systemd user unit follows.
		mkdir -p "$stage/usr/lib/systemd/user"
		cp packaging/proxy-helper.service "$stage/usr/lib/systemd/user/"

		# The icon theme cache has to be rebuilt for a newly installed icon
		# to be found; without this the menu entry shows a placeholder until
		# something else happens to refresh it. Guarded on the tool existing
		# so the package still installs on a system without GTK utilities.
		cat > "$stage/DEBIAN/postinst" <<\POSTINST
#!/bin/sh
set -e

if command -v gtk-update-icon-cache >/dev/null 2>&1; then
	gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database -q /usr/share/applications || true
fi

# From here on: point the machine at the local daemon before any user ever
# opens the tool. The daemon itself starts in mode "direct" with no profile
# selected — this is not "turn the proxy on", it is "make switching it on
# later instant, for every user this machine ever gets".
if command -v systemctl >/dev/null 2>&1; then
	# Every future login of any user starts the daemon on its own — this
	# is what makes "installed the .deb" and "daemon is up" the same fact
	# for anyone who logs in after today, not just the user running this
	# script.
	systemctl --global enable proxy-helper.service || true

	# Best effort for whoever is ALREADY logged in right now: a global
	# enable only takes effect on the NEXT login. Failure here is not an
	# error — the daemon still comes up the next time this user logs in.
	#
	# daemon-reload first: dpkg just dropped a new unit file on disk, and a
	# systemd --user manager that was already running before this install
	# has no reason to have noticed it yet.
	for uid in $(loginctl list-sessions --no-legend 2>/dev/null | awk "{print \$2}" | sort -u); do
		user="$(id -un "$uid" 2>/dev/null)" || continue
		[ -n "$user" ] || continue
		runuser -u "$user" -- env XDG_RUNTIME_DIR="/run/user/$uid" \
			systemctl --user daemon-reload 2>/dev/null || true
		runuser -u "$user" -- env XDG_RUNTIME_DIR="/run/user/$uid" \
			systemctl --user start proxy-helper.service 2>/dev/null || true
	done
fi

# Privileged targets: written directly, since this script already runs as
# root. No sudo prompt ever needed for these three, unlike everything else
# proxy-helper touches. --no-via-local: this runs before the daemon is
# necessarily up (systemctl --user start above is best-effort, and even
# --global enable only takes effect on the NEXT login) — via-local
# is the CLI default now, and without --no-via-local this would refuse with
# "the local proxy is not running" instead of just pointing these three at
# the loopback address the daemon will be listening on once it does start.
#
# $2 (the previously configured version, per the dpkg maintainer script
# convention) is empty only on a genuinely first install. Guarded on that:
# this writes a flat 127.0.0.1, not the Docker bridge address dockerd may
# already have been given (docker_bridge on, then a manual "proxy set" or
# the GUI pointed it at the bridge) — running this unconditionally on every
# reinstall/upgrade silently stomped that choice back to loopback every
# time, which is what the daemon self-apply pattern elsewhere in this
# codebase (see cmd/proxy_serve_selfapply.go) exists specifically to avoid
# for the targets it owns. A machine already bootstrapped once does not
# need this block run again; whatever these three targets say now is
# whatever the user (or a later "proxy set") already decided.
if command -v proxy-helper >/dev/null 2>&1 && [ -z "$2" ]; then
	proxy-helper proxy set --targets apt,system-env,dockerd --host 127.0.0.1 --port 8888 --no-via-local || true
fi

exit 0
POSTINST
		cat > "$stage/DEBIAN/prerm" <<\PRERM
#!/bin/sh
set -e

# $1 is "remove", "upgrade", "deconfigure", ... per the dpkg maintainer
# script convention. Only "remove" (which also covers the "remove" step of
# a later purge) should undo the privileged targets. On "upgrade" the
# package is about to be replaced by a new version, not removed — undoing
# the plumbing here would leave the machine with no working proxy for the
# whole span of the upgrade.
if [ "$1" = "remove" ] && command -v proxy-helper >/dev/null 2>&1; then
	proxy-helper proxy unset --targets apt,system-env,dockerd || true

	# Mirrors the "|| true" pattern postinst uses for the same enable:
	# undoing this is best effort, not a reason to fail the removal.
	# Without it, every future login keeps starting a daemon whose binary
	# this script is about to delete.
	if command -v systemctl >/dev/null 2>&1; then
		systemctl --global disable proxy-helper.service || true
	fi

	# The other side of the "best effort for whoever is ALREADY logged in
	# right now" loop postinst runs: "proxy purge" undoes, per user,
	# everything the daemon self-apply and any later "proxy set"/GUI use
	# built up — the systemd --user unit, config.json, and every USER-level
	# target (shell, session-env, git, npm, vscode, gnome, kde,
	# docker-config). Explicitly excludes apt, system-env and dockerd:
	# those are the ones already undone, once, as root, above — running
	# "proxy purge" as a plain user under runuser has no sudo TTY to
	# elevate through, so asking it to also touch the privileged targets
	# would just hang or fail, not skip cleanly. Same limit postinst
	# already accepts: only whoever is logged in right now is reachable
	# this way; anyone else simply finds the daemon gone at the next login
	# (the --global disable above), with no targets left pointing at it.
	if command -v systemctl >/dev/null 2>&1; then
		for uid in $(loginctl list-sessions --no-legend 2>/dev/null | awk "{print \$2}" | sort -u); do
			user="$(id -un "$uid" 2>/dev/null)" || continue
			[ -n "$user" ] || continue
			runuser -u "$user" -- env XDG_RUNTIME_DIR="/run/user/$uid" \
				proxy-helper proxy purge \
				--targets shell,session-env,git,npm,vscode,gnome,kde,docker-config \
				2>/dev/null || true
		done
	fi
fi

exit 0
PRERM
		chmod 0755 "$stage/DEBIAN/prerm"
		cat > "$stage/DEBIAN/postrm" <<POSTRM
#!/bin/sh
set -e
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
	gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor || true
fi
POSTRM
		chmod 0755 "$stage/DEBIAN/postinst" "$stage/DEBIAN/postrm"

		# dpkg-shlibdeps insists on a debian/control in the working
		# directory, even when it is only being asked about one binary.
		work=/tmp/shlibdeps
		rm -rf "$work"
		mkdir -p "$work/debian"
		printf "Source: proxy-helper-gui\n\nPackage: proxy-helper-gui\nArchitecture: any\n" \
			> "$work/debian/control"
		: > "$work/debian/substvars"
		( cd "$work" && dpkg-shlibdeps -O "$stage/usr/bin/proxy-helper-gui" ) \
			> /tmp/deps.txt
		depends="$(sed -n "s/^shlibs:Depends=//p" /tmp/deps.txt)"
		if [ -z "$depends" ]; then
			echo "dpkg-shlibdeps produced no dependencies; refusing to ship a package that claims none" >&2
			exit 1
		fi

		installed_kb="$(du -sk "$stage/usr" | cut -f1)"

		cat > "$stage/DEBIAN/control" <<CONTROL
Package: proxy-helper-gui
Version: $VERSION
Section: net
Priority: optional
Architecture: $DEB_ARCH
Depends: $depends
Installed-Size: $installed_kb
Maintainer: Moveis Simonetti <contato@gilbert.dev.br>
Description: Proxy settings manager for Linux workstations (GUI)
 Applies and removes proxy settings across shell rc files, git, npm,
 VS Code, GNOME, KDE, dockerd, the Docker client config, LXD, snap and
 apt in one step, and runs a local proxy daemon so the upstream
 credential lives in one place instead of eleven.
 .
 This package ships both the graphical interface and the proxy-helper
 command-line binary. The GUI needs the CLI: targets that require root
 are applied by reinvoking it under pkexec.
CONTROL

		out="/src/dist/proxy-helper-gui_${VERSION}_${DEB_ARCH}.deb"
		dpkg-deb --build --root-owner-group "$stage" "$out" >/dev/null
		chown "$HOST_UID:$HOST_GID" "$out"

		echo "--- control ---"
		dpkg-deb --field "$out"
		echo "--- contents ---"
		dpkg-deb --contents "$out" | awk "{print \$1, \$6}"
	'

echo
ls -1 dist/*.deb

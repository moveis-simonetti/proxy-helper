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

echo "base image : $BASE_IMAGE"
echo "version    : $VERSION"
echo "arch       : $DEB_ARCH"

docker run --rm \
	-v "$PWD:/src" \
	-v "$GOMODCACHE:/gomodcache" \
	-w /src \
	-e "GO_VERSION=$GO_VERSION" \
	-e "VERSION=$VERSION" \
	-e "DEB_ARCH=$DEB_ARCH" \
	-e "HOST_UID=$(id -u)" \
	-e "HOST_GID=$(id -g)" \
	"$BASE_IMAGE" \
	bash -euo pipefail -c '
		export DEBIAN_FRONTEND=noninteractive
		apt-get update -qq
		apt-get install -y -qq --no-install-recommends \
			ca-certificates curl build-essential pkg-config dpkg-dev \
			desktop-file-utils librsvg2-bin \
			libgtk-3-dev libayatana-appindicator3-dev >/dev/null

		arch="$(dpkg --print-architecture)"
		curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" \
			| tar -C /usr/local -xz
		export PATH=/usr/local/go/bin:$PATH
		export GOMODCACHE=/gomodcache
		export GOFLAGS=-buildvcs=false
		export CGO_ENABLED=1

		stage=/tmp/stage
		rm -rf "$stage"
		mkdir -p "$stage/DEBIAN" "$stage/usr/bin" "$stage/usr/share/applications"
		go build -tags gui -ldflags "-s -w" -o "$stage/usr/bin/proxy-helper-gui" .

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

		# The icon theme cache has to be rebuilt for a newly installed icon
		# to be found; without this the menu entry shows a placeholder until
		# something else happens to refresh it. Guarded on the tool existing
		# so the package still installs on a system without GTK utilities.
		cat > "$stage/DEBIAN/postinst" <<POSTINST
#!/bin/sh
set -e
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
	gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database -q /usr/share/applications || true
fi
POSTINST
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
 This package ships the graphical interface, which also includes every
 command-line subcommand.
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

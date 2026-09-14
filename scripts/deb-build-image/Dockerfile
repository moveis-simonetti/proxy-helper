# Build image for scripts/package-deb.sh. Bakes in the apt packages and the
# Go toolchain that the script used to apt-get/curl on every single run —
# Docker's own layer cache means this rebuilds (and re-downloads anything)
# only when BASE_IMAGE, GO_VERSION, or this file itself changes; every other
# build reuses the cached image instantly.
#
# Kept as a separate file rather than inlined into package-deb.sh so `docker
# build` can cache it independently of the source tree package-deb.sh bind
# mounts — a source change must never invalidate this image.
ARG BASE_IMAGE=ubuntu:22.04
FROM ${BASE_IMAGE}

ARG GO_VERSION
RUN test -n "$GO_VERSION" || { echo "GO_VERSION build-arg is required" >&2; exit 1; }

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update -qq && \
	apt-get install -y -qq --no-install-recommends \
		ca-certificates curl build-essential pkg-config dpkg-dev \
		desktop-file-utils librsvg2-bin \
		libgtk-3-dev libayatana-appindicator3-dev && \
	rm -rf /var/lib/apt/lists/*

RUN arch="$(dpkg --print-architecture)" && \
	curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" | tar -C /usr/local -xz

ENV PATH="/usr/local/go/bin:${PATH}"

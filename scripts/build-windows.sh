#!/usr/bin/env bash
# Constrói os dois binários do Windows a partir do Linux e, se o Inno Setup
# estiver disponível, o instalador.
#
# O cross-compile precisa de mingw porque o Fyne usa cgo — ao contrário do
# binário CLI, que sai com CGO_ENABLED=0. É por isso que há dois comandos de
# build diferentes aqui e não um laço.
set -euo pipefail

cd "$(dirname "$0")/.."
OUT="dist/windows"
VERSION="${MSPROXY_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}"

if ! command -v x86_64-w64-mingw32-gcc >/dev/null; then
  echo "falta o compilador cruzado: sudo apt install gcc-mingw-w64-x86-64" >&2
  exit 1
fi

mkdir -p "$OUT"

echo ">>> proxy-helper.exe (CLI e daemon, sem cgo)"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/proxy-helper.exe" .

echo ">>> MSProxy.exe (interface, com cgo via mingw)"
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -tags wingui -ldflags "-H windowsgui -s -w" -o "$OUT/MSProxy.exe" ./cmd/msproxy

ls -la "$OUT"

if command -v iscc >/dev/null; then
  echo ">>> instalador"
  MSPROXY_VERSION="$VERSION" iscc /O"$PWD/dist" "packaging/windows/msproxy.iss" \
    /DSourceDir="$PWD/$OUT"
else
  echo ">>> Inno Setup (iscc) não encontrado; só os binários foram gerados."
  echo "    Para o instalador: rode packaging/windows/msproxy.iss no Windows,"
  echo "    ou instale o innosetup via wine."
fi

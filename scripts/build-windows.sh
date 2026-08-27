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

# O icone e os dados de versao entram no .exe por um .syso, que o Go linka
# sozinho por estar no diretorio do pacote main. Regenerar a cada build
# mantem a versao do arquivo igual a do release.
echo ">>> icone e versao"
go run ./tools/mkicon packaging/icons/proxy-helper.svg packaging/windows/msproxy.ico
sed -i "s/VALUE \"FileVersion\",      \"[^\"]*\"/VALUE \"FileVersion\",      \"$VERSION\"/" packaging/windows/msproxy.rc
sed -i "s/VALUE \"ProductVersion\",   \"[^\"]*\"/VALUE \"ProductVersion\",   \"$VERSION\"/" packaging/windows/msproxy.rc
x86_64-w64-mingw32-windres -I packaging/windows -i packaging/windows/msproxy.rc \
  -O coff -o cmd/msproxy/resource_windows_amd64.syso

echo ">>> proxy-helper.exe (CLI e daemon, sem cgo)"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/proxy-helper.exe" .

echo ">>> MSProxy.exe (interface, com cgo via mingw)"
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -tags wingui -ldflags "-H windowsgui -s -w" -o "$OUT/MSProxy.exe" ./cmd/msproxy

ls -la "$OUT"

# O compilador do Inno Setup só existe para Windows, então aqui ele roda sob
# wine, num prefixo próprio para não mexer no ~/.wine de quem builda.
# scripts/install-innosetup.sh cria esse prefixo.
WINE_ISCC="${WINEPREFIX:-$HOME/.cache/proxy-helper-wine}/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe"

if command -v iscc >/dev/null; then
  echo ">>> instalador"
  MSPROXY_VERSION="$VERSION" iscc /O"$PWD/dist" \
    /DSourceDir="$PWD/$OUT" "packaging/windows/msproxy.iss"
elif [ -f "$WINE_ISCC" ] && command -v wine >/dev/null; then
  echo ">>> instalador (via wine)"
  # O ISCC roda dentro do wine, então os caminhos que ele recebe têm de ser
  # caminhos do wine: "/" é o drive Z:, e as barras são invertidas.
  win_path() { printf 'Z:%s' "$(printf '%s' "$1" | tr '/' '\\')"; }
  WINEPREFIX="${WINEPREFIX:-$HOME/.cache/proxy-helper-wine}" WINEDEBUG=-all \
    MSPROXY_VERSION="$VERSION" wine "$WINE_ISCC" \
      "/O$(win_path "$PWD/dist")" \
      "/DSourceDir=$(win_path "$PWD/$OUT")" \
      "$(win_path "$PWD/packaging/windows/msproxy.iss")"
else
  echo ">>> Inno Setup não encontrado; só os binários foram gerados."
  echo "    Para o instalador: rode scripts/install-innosetup.sh (usa wine),"
  echo "    ou compile packaging/windows/msproxy.iss num Windows."
fi

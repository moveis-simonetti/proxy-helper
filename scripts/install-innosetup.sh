#!/usr/bin/env bash
# Instala o compilador do Inno Setup num prefixo wine dedicado, para que
# scripts/build-windows.sh consiga gerar o instalador a partir do Linux.
#
# Prefixo próprio e não o ~/.wine padrão: isto instala software Windows, e
# quem builda não deveria ter o seu ambiente wine alterado por um script de
# empacotamento. Para desfazer, apague o diretório.
set -euo pipefail

VERSION="${INNOSETUP_VERSION:-6.7.3}"
URL="https://github.com/jrsoftware/issrc/releases/download/is-${VERSION//./_}/innosetup-${VERSION}.exe"
export WINEPREFIX="${WINEPREFIX:-$HOME/.cache/proxy-helper-wine}"
ISCC="$WINEPREFIX/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe"

if [ -f "$ISCC" ]; then
  echo "Inno Setup já instalado em $WINEPREFIX"
  exit 0
fi

for tool in wine curl; do
  command -v "$tool" >/dev/null || { echo "falta o $tool" >&2; exit 1; }
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo ">>> baixando Inno Setup $VERSION"
curl -fsSL -o "$tmp/innosetup.exe" "$URL"

echo ">>> criando o prefixo wine em $WINEPREFIX"
WINEDEBUG=-all wineboot -i >/dev/null 2>&1
wineserver -w

echo ">>> instalando"
WINEDEBUG=-all wine "$tmp/innosetup.exe" \
  /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- >/dev/null 2>&1
wineserver -w

[ -f "$ISCC" ] || { echo "a instalação terminou mas o ISCC.exe não apareceu" >&2; exit 1; }
echo "pronto: $ISCC"

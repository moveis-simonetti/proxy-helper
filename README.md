# proxy-helper

CLI para configurar e limpar configurações de proxy em shells e ferramentas de desenvolvimento de uma vez só.

Inspirado no [linux-proxy-configuration-helper](https://gitlab.com/brlin/linux-proxy-configuration-helper).

## Instalação

Baixe o binário da [última release](https://github.com/moveis-simonetti/proxy-helper/releases/latest)
(publicada automaticamente a cada tag `vX.Y.Z`, veja `.github/workflows/release.yml`):

```
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "arquitetura não suportada: $ARCH" >&2; exit 1 ;;
esac

curl -fsSLO "https://github.com/moveis-simonetti/proxy-helper/releases/latest/download/proxy-helper-linux-$ARCH"
```

Para conferir a integridade do binário baixado contra o `checksums.txt` da release
(faça isso antes de renomear/mover o arquivo, o `checksums.txt` referencia o nome original):

```
curl -fsSLO "https://github.com/moveis-simonetti/proxy-helper/releases/latest/download/checksums.txt"
sha256sum --check --ignore-missing checksums.txt
```

Torne executável e instale no PATH:

```
chmod +x "proxy-helper-linux-$ARCH"
sudo mv "proxy-helper-linux-$ARCH" /usr/local/bin/proxy-helper
```

Confirme que está tudo certo:

```
proxy-helper --help
```

Para baixar uma versão específica em vez da última, troque `latest/download` por
`download/vX.Y.Z` na URL (ex: `.../releases/download/v0.1.0/proxy-helper-linux-amd64`).

## Build

Alternativamente, para compilar a partir do código-fonte (requer Go, veja a versão em `go.mod`):

```
go build -o proxy-helper .
```

## Targets

Todo comando que mexe em configurações de proxy aceita `--targets`, uma lista
separada por vírgula (ou `all`, o padrão):

- `shell` — `~/.bashrc` / `~/.zshrc`
- `git`
- `npm`
- `vscode` — `settings.json` do VS Code e forks que usam o mesmo formato (Cursor, Antigravity)
- `gnome` — configurações de proxy do sistema GNOME
- `kde` — proxy do KIO (KDE Plasma), via `kwriteconfig`
- `dockerd` — o daemon do Docker
- `docker-config` — `~/.docker/config.json` (lado cliente, usado por `docker build`/`docker run`)
- `lxd` — o daemon do LXD, via `lxc config`
- `snap`
- `apt`

Targets não disponíveis no sistema atual (ex: `gnome` fora de uma sessão
GNOME, `kde` fora do Plasma, `snap`/`lxd` onde o pacote correspondente não
está instalado) são pulados automaticamente.

`proxy unset --targets gnome` também limpa o cache de proxy do PackageKit
(usado por GNOME Software/Discover) quando presente, contornando um bug
onde ele mantém o proxy antigo mesmo depois do proxy do sistema ser
desligado; isso pode pedir sudo mesmo sem `snap`/`apt` no `--targets`.

## Uso pontual

```
proxy-helper proxy set --host 10.0.0.5 --port 8080 [--scheme http|https|socks5] \
  [--user USER] [--pass PASS] [--no-proxy localhost,127.0.0.1] \
  [--targets shell,git,npm,...] [--dry-run]

proxy-helper proxy unset [--targets ...] [--dry-run]

proxy-helper proxy status [--targets ...] [-y|--yes] [--no-sudo]
```

`--dry-run` mostra o que seria alterado sem escrever nada. `proxy status`
mostra se cada target está com proxy configurado; alguns targets (ex: `snap`)
precisam de `sudo` para uma leitura precisa e vão perguntar, a menos que
`-y`/`--no-sudo` seja passado.

## No-proxy global

Além do `--no-proxy` de cada `proxy set`/perfil, existe uma lista global de
hosts que sempre ficam de fora do proxy, independente de qual perfil (se
algum) estiver ativo. O padrão é `host.docker.internal,localhost,127.0.0.1`.
Essa lista é mesclada (sem duplicatas) com o `--no-proxy` de cada aplicação.

```
# Ver a lista global efetiva
proxy-helper proxy config show

# Trocar a lista global
proxy-helper proxy config set --no-proxy host.docker.internal,localhost,127.0.0.1,.local

# Voltar para o padrão
proxy-helper proxy config reset-no-proxy
```

## Perfis de proxy

Digitar `--host`/`--port`/`--user`/`--pass` toda vez cansa, então as
configurações de proxy podem ser salvas como perfis nomeados. Os perfis
ficam em `~/.config/proxy-helper/config.json` (permissão 0600; as
credenciais são guardadas em texto puro, então trate esse arquivo como
qualquer outro segredo).

```
# Salvar um perfil
proxy-helper proxy profile add trabalho \
  --scheme http --host 10.0.0.5 --port 8080 \
  --user vinicius --pass '...' \
  --no-proxy localhost,127.0.0.1,.local

proxy-helper proxy profile add vpn-casa --scheme socks5 --host 127.0.0.1 --port 1080

# Listar os perfis salvos e ver qual está ativo
proxy-helper proxy profile list

# Habilitar um perfil: aplica aos targets e marca como ativo
# (só um perfil fica ativo por vez)
proxy-helper proxy profile enable trabalho

# Desabilitar: limpa o proxy dos targets e desmarca como ativo
proxy-helper proxy profile disable trabalho
# (o nome é opcional — "proxy profile disable" desabilita o que estiver ativo)

# Editar campos de um perfil existente (só os flags passados são alterados)
proxy-helper proxy profile edit trabalho --port 3128

# Remover um perfil
proxy-helper proxy profile remove vpn-casa
```

`profile enable`/`profile disable` aceitam os mesmos flags `--targets` e
`--dry-run` que `proxy set`/`proxy unset`.

Para aplicar um perfil salvo uma única vez sem alterar qual está marcado
como ativo, use `--profile` no `proxy set` (mutuamente exclusivo com
`--host`):

```
proxy-helper proxy set --profile vpn-casa --targets git,npm
```

## Importar de um PAC (proxy auto-config)

Em vez de digitar host/porta na mão, dá pra importar de uma URL de PAC (o
`.pac` que browsers usam via WPAD, ex: `http://192.168.111.70/proxy.pac`). O
arquivo é baixado e as entradas `PROXY`/`HTTPS`/`SOCKS[5] host:port` que ele
retorna são extraídas (PAC é JavaScript arbitrário; isso não avalia o script,
só procura essas diretivas — cobre a grande maioria dos PACs reais).

```
# Aplica direto aos targets, como "proxy set"
proxy-helper proxy import http://192.168.111.70/proxy.pac --user vinicius --pass '...'

# Salva como perfil em vez de aplicar
proxy-helper proxy import http://192.168.111.70/proxy.pac \
  --user vinicius --pass '...' --save-profile trabalho
```

`--user`/`--pass` são as credenciais do proxy (não da URL do PAC). Se a
própria URL do `.pac` exigir autenticação, informe-as na URL:
`http://user:senha@192.168.111.70/proxy.pac`.

Se o PAC listar mais de um proxy (fallbacks, regras por host), o comando
mostra as opções encontradas e pede pra escolher uma com `--index N`.
Aceita os mesmos `--no-proxy`, `--targets` e `--dry-run` de `proxy set`.

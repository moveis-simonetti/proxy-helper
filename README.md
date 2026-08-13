# proxy-helper

CLI para configurar e limpar configurações de proxy em shells e ferramentas de desenvolvimento de uma vez só.

## Build

```
go build -o proxy-helper .
```

## Targets

Todo comando que mexe em configurações de proxy aceita `--targets`, uma lista
separada por vírgula (ou `all`, o padrão):

- `shell` — `~/.bashrc` / `~/.zshrc`
- `git`
- `npm`
- `gnome` — configurações de proxy do sistema GNOME
- `dockerd` — o daemon do Docker
- `docker-config` — `~/.docker/config.json` (lado cliente, usado por `docker build`/`docker run`)
- `snap`
- `apt`

Targets não disponíveis no sistema atual (ex: `gnome` fora de uma sessão
GNOME, `snap` onde o snapd não está instalado) são pulados automaticamente.

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

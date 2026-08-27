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
`download/vX.Y.Z` na URL (ex: `.../releases/download/v0.3.0/proxy-helper-linux-amd64`).

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
# Com o encanamento do "proxy serve" montado (--via-local), esse mesmo
# comando não toca em target nenhum: só troca o perfil ativo e recarrega
# o daemon. Veja "Proxy local" abaixo.

# Desabilitar: limpa o proxy dos targets e desmarca como ativo
proxy-helper proxy profile disable trabalho
# (o nome é opcional — "proxy profile disable" desabilita o que estiver ativo;
#  ele lembra qual era, então "proxy on" depois restaura esse perfil)

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

## Proxy local (`proxy serve`)

Em vez de escrever o proxy real (host, porta, usuário e senha) em cada um
dos targets, dá pra rodar um proxy local em `127.0.0.1:8888` que encadeia
no proxy real. Os targets passam a apontar para essa URL fixa e sem
credencial; quem sabe o proxy verdadeiro é só o daemon, lendo o
`config.json`.

Isso separa duas coisas que hoje ficam misturadas:

- **O encanamento** — instalar o daemon e apontar os targets pra ele. Feito
  uma vez por máquina, exige sudo (a maioria dos targets exige).
- **O estado do proxy** — qual perfil está ativo, ou se está desligado.
  Muda todo dia, é instantâneo e nunca precisa de sudo, porque não toca em
  target nenhum: só grava o `config.json` e manda o daemon recarregar.

| Ação | Comando | Frequência | Precisa de sudo |
|---|---|---|---|
| Instalar o serviço | `proxy serve install` | uma vez | não |
| Apontar os targets para ele | `proxy set --host … --via-local` | uma vez | sim |
| Desligar o proxy | `proxy off` | diário | não |
| Religar | `proxy on` | diário | não |
| Trocar de proxy | `proxy profile enable <nome>` | diário | não |
| Desfazer o encanamento | `proxy unset` | raro | sim |

Com o encanamento montado, `proxy profile enable <nome>` deixa de
reconfigurar os targets: ele só grava `active_profile` no `config.json` e
manda o daemon recarregar. Isso é o que torna a troca diária instantânea e
sem sudo — e é o que garante que a credencial do perfil **nunca** seja
escrita no `~/.gitconfig`, no `~/.npmrc`, no `apt.conf.d` ou em qualquer
outro target. Sem o encanamento (quem não usa o daemon), o comportamento
antigo continua valendo: `profile enable` aplica a config real aos targets.

A regra que separa os dois eixos: **`unset` desfaz o encanamento** (volta
os targets a não apontar mais pro daemon); **`off` só manda o daemon rotear
tudo direto**, sem tocar em target algum. Um daemon com perfil vazio é
inofensivo — é só um proxy que faz `DIRECT` pra tudo.

```
# Uma vez por máquina
proxy-helper proxy serve install
proxy-helper proxy set --profile trabalho --via-local

# No dia a dia, sem sudo
proxy-helper proxy off                     # tudo direto
proxy-helper proxy on                      # volta pro último perfil
proxy-helper proxy on vpn-casa             # ou troca pra outro perfil
proxy-helper proxy profile enable trabalho # idem, via profile

# Raro: tirar o encanamento por completo
proxy-helper proxy unset --targets ...
```

`proxy set --via-local` (e `proxy profile enable --via-local`) recusam
rodar se o daemon não estiver instalado e ativo, e dizem exatamente o que
rodar (`proxy serve install`). `proxy serve uninstall` remove o serviço.

A porta padrão é 8888. Para usar outra, passe `--port` no
`proxy serve install`: ela fica gravada em `local_port` no `config.json`, e
tanto `--via-local` quanto o `proxy status` passam a usar essa porta.
`proxy unset` (de todos os targets) desfaz o encanamento; um `unset`
parcial, de alguns targets só, mantém o aviso — os outros ainda apontam
pro loopback.

### Credenciais

Com o serviço rodando via `systemd --user`, a forma recomendada de guardar
a senha é `password_file`, porque **a unit não herda o ambiente do shell
interativo** — uma `PROXY_HELPER_PASSWORD` exportada no `.bashrc` não chega
até o daemon.

```bash
printf '%s' 'minha-senha' > ~/.config/proxy-helper/senha
chmod 600 ~/.config/proxy-helper/senha
```

E no perfil, o campo `"password_file"` apontando pro arquivo:

```json
{
  "profiles": {
    "trabalho": {
      "scheme": "http",
      "host": "10.0.0.5",
      "port": "8080",
      "user": "vinicius",
      "password_file": "/home/voce/.config/proxy-helper/senha"
    }
  }
}
```

A ordem de precedência, a primeira que resolver vence:

1. `password_file` — arquivo com só a senha; o daemon recusa ler se a
   permissão permitir grupo ou outros (exige `0600`/`0400`).
2. `password_env` — nome de uma variável de ambiente a consultar.
3. `PROXY_HELPER_PASSWORD` — variável global padrão.
4. `pass` — o campo legado direto no `config.json`. Continua funcionando,
   mas é depreciado.

### Navegadores

Com os targets apontando pro daemon local, o pop-up de autenticação do
Chrome/Firefox some: o navegador fala com o loopback, que não pede
credencial, e é o daemon quem injeta o `Proxy-Authorization` no hop
seguinte, contra o proxy real.

### SOCKS5 em apt, npm e docker

`apt`, `npm` e `docker` não falam SOCKS5. Com um perfil `--scheme socks5`
por trás do daemon, esses targets passam a conseguir usar esse upstream
mesmo assim — eles falam HTTP com o loopback, e é o daemon quem faz a
conversão pro SOCKS5 real.

### `proxy logs`

Lê o log do serviço a partir do `journald` e renderiza:

```
proxy-helper proxy logs                  # últimas 200 entradas
proxy-helper proxy logs -f               # segue ao vivo, linha a linha
proxy-helper proxy logs --since 10m
proxy-helper proxy logs -n 500
proxy-helper proxy logs --host github.com
proxy-helper proxy logs --errors         # só requisições que falharam
proxy-helper proxy logs --direct         # só o que saiu sem passar pelo proxy
proxy-helper proxy logs --json           # JSON cru do journal, para jq
proxy-helper proxy logs --stats          # resumo agregado
```

Saída renderizada:

```
14:22:01  CONNECT  200  142ms  1.2 MB  github.com:443       -> proxy.corp:8080
14:22:03  GET      200   38ms  4.1 kB  registry.npmjs.org   -> proxy.corp:8080
14:22:04  GET      200    2ms   890 B  gitlab.interno       -> DIRECT
14:22:09  CONNECT  502  310ms      —   api.stripe.com:443   x upstream refused: 407
```

E `--stats`:

```
requests: 1000  (proxied 940, direct 60)
errors:   12 (1.2%)
traffic:  184.3 MB

top hosts by requests:
  github.com             210  92.1 MB
  registry.npmjs.org     180  40.4 MB
  ...
```

`-f`/`--follow` imprime cada entrada assim que ela chega, linha a linha,
até você interromper com Ctrl-C. Como o resumo agregado só faz sentido
sobre um lote fechado, `--stats` e `--follow` são mutuamente exclusivos.

Eventos de ciclo de vida do daemon (`startup`, `reload`, `reload_failed`)
não aparecem na tabela — ela é só de requisições. Eles continuam no
`--json`, que devolve o journal cru.

### Docker e containers

Containers **não alcançam o `127.0.0.1` do host** — lá dentro, `127.0.0.1` é o
próprio container. Isso torna o `--via-local` parcialmente quebrado para
Docker, e de um jeito confuso: `docker pull` funciona (o `dockerd` roda no
host), mas todo `RUN` do build que precise de rede morre com
`Failed to connect to 127.0.0.1 port 8888`.

A saída é `--docker-bridge`, que faz o daemon escutar **também** no gateway da
bridge do Docker:

```bash
proxy-helper proxy serve install --docker-bridge
proxy-helper proxy set --profile trabalho --via-local
sudo systemctl restart docker
```

Os targets `dockerd` e `docker-config` passam a receber o endereço da bridge
(algo como `172.17.0.1:8888`); os outros nove continuam no loopback. Confira
com `proxy status`.

O `systemctl restart docker` é obrigatório: o `dockerd` só lê o proxy no
arranque, e é o passo que mais se esquece.

> **O custo, em uma frase:** com `--docker-bridge`, **qualquer container da
> máquina pode usar o proxy** — ele não autentica clientes. Não fica exposto à
> rede local, só aos containers. Por isso é opt-in, o daemon recusa escutar em
> qualquer endereço publicamente roteável, e loga um aviso no arranque.

Sem a flag, o `proxy set --via-local` avisa que os builds vão falhar em vez de
deixar você descobrir no meio de um deploy.

### Modo de falha

Se o serviço parar com o encanamento ativo (`--via-local`), tudo que
depende do proxy passa a falhar com `connection refused`, porque os
targets continuam apontando pro loopback e não há mais nada escutando lá.
`proxy status` avisa isso no topo da saída:

```
daemon: INACTIVE - targets point at 127.0.0.1:8888 and will fail; run "systemctl --user start proxy-helper.service"
```

A unit sobe com `Restart=always`, então esse cenário deve ser raro na
prática — mas a saída do `status` já dá o comando exato pra religar.

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

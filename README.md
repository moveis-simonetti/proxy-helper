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
- `session-env` — variáveis de ambiente da sessão gráfica, para aplicativos
  de janela (veja abaixo)
- `system-env` — variáveis de proxy em `/etc/environment`, para todo login
  PAM da máquina, inclusive o `root` (veja abaixo); exige `sudo`
- `git`
- `npm`
- `vscode` — `settings.json` do VS Code e forks que usam o mesmo formato
  (Cursor, Antigravity); detecta as instalações nativa, Snap
  (`~/snap/code/current/.config`) e Flatpak (`~/.var/app/…/config`), e
  configura cada uma separadamente
- `gnome` — configurações de proxy do sistema GNOME
- `kde` — proxy do KIO (KDE Plasma), via `kwriteconfig`
- `dockerd` — o daemon do Docker
- `docker-config` — `~/.docker/config.json` (lado cliente, usado por `docker build`/`docker run`)
- `lxd` — o daemon do LXD, via `lxc config`
- `snap`
- `apt`
- `nm-connectivity` — a checagem de conectividade do NetworkManager (veja
  abaixo); exige `sudo`, e só faz algo se `connectivity_check_url` estiver
  configurado no perfil

Targets não disponíveis no sistema atual (ex: `gnome` fora de uma sessão
GNOME, `kde` fora do Plasma, `snap`/`lxd` onde o pacote correspondente não
está instalado) são pulados automaticamente.

### `session-env` e aplicativos de janela

O target `shell` alcança apenas terminais: `~/.bashrc` e `~/.zshrc` são lidos
quando você abre um shell, não quando o GNOME lança um aplicativo pelo menu.
Aplicativos como 1Password, Discord e Postman têm componentes nativos que
leem `HTTPS_PROXY` do próprio ambiente e ignoram as configurações do GNOME —
sem `session-env`, eles ficam sem internet enquanto o resto do sistema
funciona.

`session-env` fecha esse buraco escrevendo em duas camadas:

- `~/.config/environment.d/50-proxy-helper.conf`, lido pelo `systemd --user`
  no login, para a configuração sobreviver a reinicializações;
- `systemctl --user set-environment` e `dbus-update-activation-environment`,
  que valem imediatamente, para aplicativos abertos a partir de agora.

**Aplicativos já em execução não são afetados.** O ambiente de um processo é
fixado no momento em que ele é iniciado e não pode ser reescrito de fora —
limitação do sistema operacional, não da ferramenta. Depois de aplicar (ou de
trocar a porta do daemon), feche e abra os aplicativos de janela para que eles
peguem a configuração nova. Quando a sessão viva discorda do perfil, o
`proxy status` avisa:

```
session-env  true  true  http://127.0.0.1:8888 — profile uses port 9090; re-apply, then relaunch GUI apps
```

Fora de uma sessão gráfica (servidor, container, SSH puro) não há
`systemd --user` para conversar, e o target é pulado como qualquer outro
indisponível.

### `system-env` e o `root`

`shell` e `session-env` só alcançam o usuário que rodou o comando — nada que
o `root` leia é tocado. Numa rede que só sai por proxy, isso deixa
`sudo apt update`, `sudo curl` e afins sem internet: o `sudo` roda com
`env_reset` e descarta `HTTP_PROXY`/`HTTPS_PROXY` do ambiente.

`system-env` fecha esse buraco escrevendo um bloco marcado em
`/etc/environment`, lido pelo `pam_env` na criação de qualquer sessão PAM —
login no console, `ssh root@host`, `su -`, `sudo -i` e, no Debian/Ubuntu
(onde `/etc/pam.d/sudo` inclui `pam_env`), também o `sudo <comando>` comum.
Só as linhas do bloco são gerenciadas; o resto do arquivo (`PATH`, `LANG`, …)
fica intacto.

**É um arquivo do sistema, não por usuário.** Toda conta da máquina passa a
receber essas variáveis no login, não só quem aplicou. Para uma máquina
inteira atrás de um proxy isso costuma ser o desejado, mas é um alcance maior
que o dos outros targets de ambiente — por isso é um target separado e
explícito, e exige `sudo`.

Como em `session-env`, **processos já em execução não são afetados**, e o
systemd de _serviços de sistema_ não lê `/etc/environment` (o `dockerd`, por
exemplo, é tratado pelo target `dockerd`).

`proxy unset --targets gnome` também limpa o cache de proxy do PackageKit
(usado por GNOME Software/Discover) quando presente, contornando um bug
onde ele mantém o proxy antigo mesmo depois do proxy do sistema ser
desligado; isso pode pedir sudo mesmo sem `snap`/`apt` no `--targets`.

### `nm-connectivity` e o ícone de "conectividade limitada"

Numa rede que só sai por proxy, o NetworkManager mostra "conectividade
limitada" mesmo com tudo funcionando: a checagem de conectividade dele
(`nmcli general status`) faz uma requisição HTTP direta, sem proxy, e não
tem nenhum jeito de configurar um proxy para ela — nem por
`NetworkManager.conf`, nem pelas configurações de proxy por conexão
(`proxy.method`/`proxy.pac-script`, que servem só para distribuir a config a
outros consumidores via D-Bus, não para a própria checagem do NetworkManager
usar). Testado e confirmado nesta sessão.

A única correção real é apontar a checagem para algo que a rede em questão
responda **sem** proxy — o que é sempre específico de cada rede (não dá para
descobrir isso automaticamente). Se você tiver algo assim (por exemplo, o
próprio host do proxy respondendo em uma porta sem passar pelo túnel de
proxy), configure no perfil:

```
proxy profile edit "Escritório Simonetti" \
  --connectivity-check-url "http://192.168.111.70/" \
  --connectivity-check-response "<html>"
```

`--connectivity-check-response` precisa bater com o **início** do corpo da
resposta (é assim que o NetworkManager decide sucesso/falha). Depois de
configurar, rode `proxy profile enable "Escritório Simonetti"` (editar o
perfil sozinho não reaplica os targets) para escrever
`/etc/NetworkManager/conf.d/95-proxy-helper-connectivity.conf` e recarregar o
NetworkManager.

Sem `connectivity_check_url` configurado (o padrão), esse target não faz
nada — nenhum `sudo`, nenhum arquivo escrito.

**É específico da rede, não da máquina.** A URL só responde sem proxy
*naquela* rede; em qualquer outra (casa, hotspot, outro escritório), a
checagem vai falhar de novo e o ícone volta a mostrar "limitada" — mesmo com
internet de verdade — porque a configuração continua apontando para uma URL
que só existe na rede antiga. Não tem como isso ser automático: se você usa
mais de uma rede que precisa de proxy, configure
`--connectivity-check-url`/`--connectivity-check-response` em cada perfil
correspondente, e troque de perfil (`proxy profile enable <nome>`, não só
`proxy set`) ao trocar de rede — trocar de perfil já limpa a checagem
antiga automaticamente, mesmo que o novo perfil não configure uma própria.

## Uso pontual

```
proxy-helper proxy set --host 10.0.0.5 --port 8080 [--scheme http|https|socks5] \
  [--user USER] [--pass PASS] [--no-proxy localhost,127.0.0.1] \
  [--targets shell,session-env,git,...] [--dry-run]

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
# Com o encanamento do "proxy serve" montado, esse mesmo
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
| Montar tudo de uma vez | `proxy setup --host … --port …` | uma vez | sim |
| (ou, passo a passo) instalar o serviço | `proxy serve install` | uma vez | não |
| (…) apontar os targets para ele | `proxy set --host …` (é o padrão) | uma vez | sim |
| Trocar o modo de roteamento | `proxy mode auto\|upstream\|direct` | diário | não |
| Desligar o proxy | `proxy off` (= `proxy mode direct`) | diário | não |
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
tudo direto**, sem tocar em target algum. Um daemon em modo `direct` é
inofensivo — é só um proxy que faz `DIRECT` pra tudo.

### `proxy setup`

`proxy setup` faz numa tacada o arranjo que o resto da ferramenta pressupõe:
instala e sobe o daemon, aponta todos os targets para ele e deixa o modo em
`auto`. É seguro reexecutar — um daemon já ativo é deixado em paz, e
`--dry-run` mostra o plano inteiro sem mexer em nada.

Ele existe porque esse arranjo antes se montava à mão com três comandos na
ordem certa. Hoje `proxy set`/`proxy profile enable` já apontam pro daemon
por padrão — o detalhe que antes ninguém descobria sozinho virou o
comportamento normal —, mas `proxy setup` continua sendo o jeito de instalar
o serviço e montar tudo numa tacada só, sem precisar lembrar da ordem dos
três comandos.

```
proxy-helper proxy setup --host 10.0.0.5 --port 8080 --profile trabalho
proxy-helper proxy setup --profile trabalho          # reusa um perfil salvo
proxy-helper proxy setup --mode upstream --host …    # sem o fallback do auto
```

### Quando o daemon não está de pé

Apontar todos os targets para um daemon local é o que torna a alternância
instantânea e sem sudo — e é também o que faz desse daemon um ponto único de
falha. Se ele não estiver escutando, **a máquina inteira fica sem rede**.

O `Restart=always` da unit cobre a queda comum. Para o resto, o `proxy status`
avisa em primeiro lugar, e a aba Status da GUI mostra uma faixa vermelha:

```
  WARNING: every target points at 127.0.0.1:8888, but nothing is listening there.
           Until the daemon is back, this machine has no network access at all.
           Restart it:  systemctl --user restart proxy-helper.service
           Or take the targets off it:  proxy-helper proxy unset --targets all
```

O aviso vem de uma sondagem TCP na porta, não do `systemctl is-active`: um
daemon que perdeu a porta para outro processo, ou que está no meio de um
reinício, aparece como *active* e não serve para nada.

Se o daemon não subir de jeito nenhum, `proxy unset --targets all` é a saída
— devolve os targets ao estado direto e a máquina volta a ter rede.

### Modos de roteamento

O que o daemon faz com uma requisição é o campo `mode` do `config.json`:

| Modo | Comportamento |
|---|---|
| `auto` | encaminha enquanto o upstream responde; cai para direto quando ele para |
| `upstream` | sempre encaminha — nunca deixa tráfego sair por fora do proxy |
| `direct` | nunca encaminha; manda tudo direto |

```
proxy-helper proxy mode            # mostra o modo atual
proxy-helper proxy mode direct     # equivale a "proxy off"
proxy-helper proxy mode auto
```

`auto` é para o notebook que troca de rede: sem ele, sair da rede corporativa
com o proxy ligado deixa a máquina sem internet até alguém lembrar de
desligar. Em compensação, `auto` **manda o tráfego pela saída direta** quando
o upstream cai — quem não pode aceitar isso deve fixar `upstream`, que
continua encaminhando mesmo com o upstream fora do ar.

O perfil selecionado **não** é apagado ao desligar: `active_profile` diz qual
perfil está escolhido e `mode` diz se ele está em uso. Por isso o seletor
continua mostrando o perfil enquanto o tráfego vai direto, e um `proxy on`
depois não precisa adivinhar nada.

Configs criadas antes do campo `mode` são migradas na primeira leitura: com um
perfil ativo viram `upstream` (comportamento idêntico ao anterior); desligadas,
viram `direct` com o perfil que estava guardado voltando a aparecer selecionado.

```
# Uma vez por máquina — instala o daemon, aponta os alvos e deixa o modo pronto
proxy-helper proxy setup --host 10.0.0.5 --port 8080 --profile trabalho

# (equivale a fazer isto na mão, na ordem certa)
proxy-helper proxy serve install
proxy-helper proxy set --profile trabalho

# No dia a dia, sem sudo
proxy-helper proxy off                     # tudo direto (= proxy mode direct)
proxy-helper proxy mode auto               # encaminha só quando o upstream responde
proxy-helper proxy on                      # volta a encaminhar
proxy-helper proxy on vpn-casa             # ou troca pra outro perfil
proxy-helper proxy profile enable trabalho # idem, via profile

# Raro: tirar o encanamento por completo
proxy-helper proxy unset --targets ...
```

Por padrão (sem `--no-via-local`), `proxy set` e `proxy profile enable`
recusam rodar se o daemon não estiver instalado e ativo, e dizem exatamente
o que rodar (`proxy serve install`). Quem não roda o daemon usa
`--no-via-local` como escape hatch: volta ao comportamento antigo, gravando
a credencial direto em cada target. `proxy serve uninstall` remove o
serviço.

A porta padrão é 8888. Para usar outra, passe `--port` no
`proxy serve install`: ela fica gravada em `local_port` no `config.json`, e
tanto o roteamento via daemon (o padrão) quanto o `proxy status` passam a
usar essa porta.
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
próprio container. Isso torna o roteamento via daemon parcialmente quebrado
para Docker, e de um jeito confuso: `docker pull` funciona (o `dockerd` roda
no host), mas todo `RUN` do build que precise de rede morre com
`Failed to connect to 127.0.0.1 port 8888`.

A saída é `--docker-bridge`, que faz o daemon escutar **também** no gateway da
bridge do Docker:

```bash
proxy-helper proxy serve install --docker-bridge
proxy-helper proxy set --profile trabalho
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

Sem a flag `--docker-bridge`, o `proxy set` avisa que os builds vão falhar em
vez de deixar você descobrir no meio de um deploy.

#### Node.js dentro de containers

O `docker-config` injeta as variáveis de proxy nos containers, mas o Docker só
repassa as chaves que ele conhece (`httpProxy`, `httpsProxy`, `noProxy`,
`ftpProxy`, `allProxy`) — não há como acrescentar outras. Isso importa para
Node, que tem dois comportamentos diferentes:

- **`npm`, `yarn`, `pnpm`, `axios` e afins funcionam.** Essas ferramentas leem
  `HTTPS_PROXY` por conta própria.
- **O `fetch()` global do Node ignora `HTTPS_PROXY`** e falha com
  `ENETUNREACH`. Ele só respeita a variável quando `NODE_USE_ENV_PROXY=1`
  também está no ambiente — e essa não é repassada pelo Docker.

Para Node 22 e 24, resolva na imagem:

```dockerfile
ENV NODE_USE_ENV_PROXY=1
```

No Node 20 a variável não existe (foi adicionada no 22), então só a própria
aplicação pode rotear o `fetch`:

```js
// no topo do ponto de entrada, antes do primeiro fetch()
import { setGlobalDispatcher, EnvHttpProxyAgent } from "undici";
setGlobalDispatcher(new EnvHttpProxyAgent());
```

`EnvHttpProxyAgent` (e não `ProxyAgent`) porque ele respeita `NO_PROXY`,
mantendo o tráfego interno fora do proxy. Funciona igual em todas as versões
do Node, então serve como solução única para quem mantém imagens variadas.

### Modo de falha

Se o serviço parar com o encanamento ativo (o caso comum, já que é o
padrão), tudo que depende do proxy passa a falhar com `connection refused`,
porque os targets continuam apontando pro loopback e não há mais nada
escutando lá.
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
